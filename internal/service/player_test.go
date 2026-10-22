package service

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
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

// notFoundResolver always reports a conclusive NOT_FOUND.
type notFoundResolver struct{ name string }

func (n notFoundResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	return nil, resolver.NewNotFoundError(n.name, identifier)
}

func (n notFoundResolver) Name() string { return n.name }

// countingResolver counts Resolve calls and always reports NOT_FOUND.
type countingResolver struct {
	name  string
	calls int
}

func (c *countingResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	c.calls++
	return nil, resolver.NewNotFoundError(c.name, identifier)
}

func (c *countingResolver) Name() string { return c.name }

// nopCache is a no-op cache.Cache.
type nopCache struct{}

func (nopCache) SetCacheBulk(keys []string, data any, ttl time.Duration) error {
	return nil
}

func (nopCache) GetCache(key string) (jsontext.Value, time.Time, time.Duration, bool, error) {
	return nil, time.Time{}, 0, false, nil
}

func (nopCache) Close() error { return nil }

// The test doubles must keep satisfying the service's cache interface.
var _ cache.Cache = nopCache{}
var _ cache.Cache = (*stubCache)(nil)

// newTestService builds a PlayerService with a nil logger (falls back to
// slog.Default()), a 1-minute player cache, and negative caching disabled
// (zero TTL).
func newTestService(resolvers []resolver.Resolver, strategy string) *PlayerService {
	return NewPlayerService(
		resolvers,
		strategy,
		1,
		nopCache{},
		"test:",
		time.Minute,
		0,
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

// stubWrite records one SetCacheBulk call for assertions. A nil payload is a
// not-found verdict, a non-nil one is a player entry.
type stubWrite struct {
	keys []string
	data any
}

// stubCache is a scripted cache.Cache for probe-path tests: GetCache returns
// a fixed payload (nil scripted payload = a miss), and probe/write calls are
// recorded for assertions.
type stubCache struct {
	nopCache
	raw []byte

	probes int
	writes []stubWrite
}

func (s *stubCache) GetCache(key string) (jsontext.Value, time.Time, time.Duration, bool, error) {
	s.probes++
	if s.raw == nil {
		return nil, time.Time{}, 0, false, nil
	}
	return jsontext.Value(s.raw), time.Now(), 30 * time.Second, true, nil
}

func (s *stubCache) SetCacheBulk(keys []string, data any, ttl time.Duration) error {
	s.writes = append(s.writes, stubWrite{keys: keys, data: data})
	return nil
}

// playerWrites counts player-entry writes (non-nil payloads).
func (s *stubCache) playerWrites() int {
	n := 0
	for _, w := range s.writes {
		if w.data != nil {
			n++
		}
	}
	return n
}

// notFoundWrites counts not-found verdict writes (nil payloads).
func (s *stubCache) notFoundWrites() int {
	n := 0
	for _, w := range s.writes {
		if w.data == nil {
			n++
		}
	}
	return n
}

// cachedPlayerJSON returns the raw payload a stored player entry would have.
func cachedPlayerJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(&PlayerCacheEntry{
		UUID:     "069a79f4-44e9-4726-a91b-ef990e6b5624",
		Username: "notch",
		Resolver: "stored",
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A not-found verdict is written into the same keyspace as player entries:
// the write prefix must equal the service cache prefix, not a separate
// negative namespace.
func TestNotFoundVerdictWrittenToPlayerKeyspace(t *testing.T) {
	c := &stubCache{}
	svc := NewPlayerService(
		[]resolver.Resolver{notFoundResolver{name: "nf"}},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	result := svc.Resolve(context.Background(), "Notch")

	var resErr *resolver.ResolverError
	if !errors.As(result.Error, &resErr) {
		t.Fatalf("expected a *resolver.ResolverError, got %T", result.Error)
	}
	if resErr.Code != constant.CodeNotFound {
		t.Errorf("expected code %q, got %q", constant.CodeNotFound, resErr.Code)
	}
	if c.notFoundWrites() != 1 {
		t.Fatal("expected a not-found verdict to be cached")
	}
	if got := c.writes[0].keys[0]; got != "test:notch" {
		t.Errorf("expected the verdict to share the player keyspace %q, got %q", "test:notch", got)
	}
}

// A cached not-found verdict (null payload) yields a 404 cache hit without
// contacting any resolver, in a single cache probe.
func TestCachedNotFoundShortCircuitsBeforeResolvers(t *testing.T) {
	upstream := &countingResolver{name: "counted"}
	c := &stubCache{raw: []byte("null")}
	svc := NewPlayerService(
		[]resolver.Resolver{upstream},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	result := svc.Resolve(context.Background(), "Notch")

	var resErr *resolver.ResolverError
	if !errors.As(result.Error, &resErr) {
		t.Fatalf("expected a *resolver.ResolverError, got %T", result.Error)
	}
	if resErr.Code != constant.CodeNotFound {
		t.Errorf("expected code %q, got %q", constant.CodeNotFound, resErr.Code)
	}
	if !result.CacheHit {
		t.Error("expected the 404 to be a cache hit")
	}
	if upstream.calls != 0 {
		t.Errorf("expected resolvers to be short-circuited, got %d calls", upstream.calls)
	}
}

// With negative caching disabled (zero negative TTL), a cached not-found
// verdict sharing the player keyspace must be ignored and resolution must
// fall through to the resolvers.
func TestNegativeDisabledIgnoresCachedVerdict(t *testing.T) {
	c := &stubCache{raw: []byte("null")}
	svc := NewPlayerService(
		[]resolver.Resolver{okResolver{name: "good"}},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		0,
		nil,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error != nil {
		t.Fatalf("expected the disabled negative cache to fall through to resolvers, got error: %v", result.Error)
	}
	if result.Player == nil {
		t.Fatal("expected a player on success")
	}
	if result.CacheHit {
		t.Error("expected a resolver result, not a cache hit")
	}
}

// A positive entry (player payload) yields the stored player without
// contacting any resolver.
func TestCachedPlayerShortCircuitsBeforeResolvers(t *testing.T) {
	upstream := &countingResolver{name: "counted"}
	c := &stubCache{raw: cachedPlayerJSON(t)}
	svc := NewPlayerService(
		[]resolver.Resolver{upstream},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		0,
		nil,
	)

	result := svc.Resolve(context.Background(), "notch")

	if result.Error != nil {
		t.Fatalf("expected a positive cache hit, got error: %v", result.Error)
	}
	if !result.CacheHit {
		t.Error("expected the player to be a cache hit")
	}
	if result.Resolver != "stored" {
		t.Errorf("expected resolver %q, got %q", "stored", result.Resolver)
	}
	if result.Player == nil || result.Player.Username != "notch" {
		t.Errorf("expected the stored player, got %+v", result.Player)
	}
	if upstream.calls != 0 {
		t.Errorf("expected resolvers to be short-circuited, got %d calls", upstream.calls)
	}
}

// With positive caching disabled (zero cache TTL), a cached player entry
// sharing the keyspace must be ignored and resolution must fall through to
// the resolvers.
func TestPositiveDisabledIgnoresCachedEntry(t *testing.T) {
	c := &stubCache{raw: cachedPlayerJSON(t)}
	svc := NewPlayerService(
		[]resolver.Resolver{okResolver{name: "good"}},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		0,
		time.Minute,
		nil,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error != nil {
		t.Fatalf("expected the disabled player cache to fall through to resolvers, got error: %v", result.Error)
	}
	if result.Player == nil {
		t.Fatal("expected a player on success")
	}
	if result.CacheHit {
		t.Error("expected a resolver result, not a cache hit")
	}
}

// With caching fully disabled (both TTLs zero), no cache probe is made at
// all — no wasted Redis roundtrip for a feature that is off.
func TestFullyDisabledCachingSkipsProbe(t *testing.T) {
	c := &stubCache{raw: cachedPlayerJSON(t)}
	upstream := &okResolver{name: "good"}
	svc := NewPlayerService(
		[]resolver.Resolver{upstream},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		0,
		0,
		nil,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error != nil {
		t.Fatalf("expected a resolver result, got error: %v", result.Error)
	}
	if result.CacheHit {
		t.Error("expected a resolver result, not a cache hit")
	}
	if c.probes != 0 {
		t.Errorf("expected no cache probe with caching fully disabled, got %d", c.probes)
	}
}

// With positive caching disabled (zero cache TTL), a successful resolve must
// not write a player entry.
func TestPositiveDisabledSkipsPlayerWrite(t *testing.T) {
	c := &stubCache{}
	svc := NewPlayerService(
		[]resolver.Resolver{okResolver{name: "good"}},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		0,
		time.Minute,
		nil,
	)

	result := svc.Resolve(context.Background(), "Notch")

	if result.Error != nil {
		t.Fatalf("expected success, got error: %v", result.Error)
	}
	if c.playerWrites() != 0 {
		t.Errorf("expected no player cache write with caching disabled, got %d", c.playerWrites())
	}
}
