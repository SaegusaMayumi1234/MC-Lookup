package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/cache"
	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
)

// panicResolver panics on every Resolve call, simulating a bug in a
// third-party resolver implementation.
type panicResolver struct{ name string }

func (p panicResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	panic("simulated resolver bug in " + p.name)
}

func (p panicResolver) Name() string { return p.name }

// okResolver always succeeds.
type okResolver struct{ name string }

func (o okResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	return &model.Player{UUID: "069a79f444e94726a91bef990e6b5624", Username: identifier}, nil
}

func (o okResolver) Name() string { return o.name }

// nopCache is a no-op cache.Cache.
type nopCache struct{}

func (nopCache) SetPlayerCache(prefix string, p *cache.PlayerData, resolver string, ttl time.Duration) {
}

func (nopCache) GetPlayerCache(prefix string, identifier string) (*cache.Entry, bool) {
	return nil, false
}

func (nopCache) SetNotFoundCache(prefix, identifier string, ttl time.Duration) {}

func (nopCache) GetNotFoundCache(prefix, identifier string) (time.Duration, bool) {
	return 0, false
}

func (nopCache) Close() error { return nil }

// newTestService builds a PlayerService with a nil logger (falls back to
// slog.Default()) and negative caching disabled.
func newTestService(resolvers []resolver.Resolver, strategy string) *PlayerService {
	return NewPlayerService(
		resolvers,
		strategy,
		1,
		nopCache{},
		time.Minute,
		"test:",
		false,
		"nf:",
		time.Minute,
		nil,
	)
}

func TestRaceStrategyRecoversFromPanickingResolver(t *testing.T) {
	svc := newTestService(
		[]resolver.Resolver{
			panicResolver{name: "boom"},
			okResolver{name: "good"},
		},
		constant.StrategyRace,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error != nil {
		t.Fatalf("expected success from the healthy resolver, got error: %v", result.Error)
	}
	if result.Player == nil {
		t.Fatal("expected a player on success")
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
}

func TestRaceStrategyAllResolversPanic(t *testing.T) {
	svc := newTestService(
		[]resolver.Resolver{
			panicResolver{name: "boom-a"},
			panicResolver{name: "boom-b"},
		},
		constant.StrategyRace,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error == nil {
		t.Fatal("expected an aggregate error when every resolver panics")
	}

	var resErr *resolver.ResolverError
	if !errors.As(result.Error, &resErr) {
		t.Fatalf("expected a *resolver.ResolverError, got %T", result.Error)
	}
	if resErr.Code != constant.CodeUpstreamError {
		t.Errorf("expected code %q, got %q", constant.CodeUpstreamError, resErr.Code)
	}
	// Both panicking resolvers must appear as ordinary failed results —
	// proof that their panics were converted into errors, not crashes.
	for _, name := range []string{"boom-a", "boom-b"} {
		if _, ok := resErr.Details[name]; !ok {
			t.Errorf("expected details entry for resolver %q, got %v", name, resErr.Details)
		}
	}
}

func TestDoResolveTrackedReleasesCounterOnPanic(t *testing.T) {
	svc := newTestService([]resolver.Resolver{panicResolver{name: "boom"}}, constant.StrategyFallback)

	func() {
		defer func() { _ = recover() }() // the API layer's recovery middleware does this in production
		_ = svc.doResolveTracked(context.Background(), "Notch")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.WaitInflightResolves(ctx); err != nil {
		t.Fatalf("inflight counter leaked after a recovered panic: %v", err)
	}
}

// Fallback runs resolvers inside the singleflight closure; a panic escaping it
// would crash the whole process (x/sync/singleflight re-panics unrecoverably
// for DoChan callers). safeResolve must convert it into a failed result.
func TestFallbackStrategyRecoversFromPanickingResolver(t *testing.T) {
	svc := newTestService(
		[]resolver.Resolver{
			panicResolver{name: "boom"},
			okResolver{name: "good"},
		},
		constant.StrategyFallback,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error != nil {
		t.Fatalf("expected success from the healthy resolver, got error: %v", result.Error)
	}
	if result.Player == nil {
		t.Fatal("expected a player on success")
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
}

func TestFallbackStrategyAllResolversPanic(t *testing.T) {
	svc := newTestService(
		[]resolver.Resolver{
			panicResolver{name: "boom-a"},
			panicResolver{name: "boom-b"},
		},
		constant.StrategyFallback,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error == nil {
		t.Fatal("expected an aggregate error when every resolver panics")
	}

	var resErr *resolver.ResolverError
	if !errors.As(result.Error, &resErr) {
		t.Fatalf("expected a *resolver.ResolverError, got %T", result.Error)
	}
	if resErr.Code != constant.CodeUpstreamError {
		t.Errorf("expected code %q, got %q", constant.CodeUpstreamError, resErr.Code)
	}
	for _, name := range []string{"boom-a", "boom-b"} {
		if _, ok := resErr.Details[name]; !ok {
			t.Errorf("expected details entry for resolver %q, got %v", name, resErr.Details)
		}
	}
}
