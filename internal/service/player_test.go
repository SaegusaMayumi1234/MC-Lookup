package service

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"sort"
	"strings"
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

func (nopCache) SetCacheBulk(context.Context, []string, any, time.Duration) error {
	return nil
}

func (nopCache) GetCache(context.Context, string) (jsontext.Value, time.Time, time.Duration, bool, error) {
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

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected success from the healthy resolver, got error: %v", resErr)
	}
	if result == nil || result.Player == nil {
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

	_, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil {
		t.Fatal("expected an aggregate error when every resolver panics")
	}

	if resErr.Code != constant.CodeUpstreamError {
		t.Errorf("expected code %q, got %q", constant.CodeUpstreamError, resErr.Code)
	}
	// Both panicking resolvers must have been consulted — proof that their
	// panics were converted into errors, not crashes.
	if resErr.Message != "All resolver attempts failed" {
		t.Errorf("expected the all-failed aggregate message, got %q", resErr.Message)
	}
}

func TestDoResolveTrackedReleasesCounterOnPanic(t *testing.T) {
	svc := newTestService([]resolver.Resolver{panicResolver{name: "boom"}}, constant.StrategyFallback)

	func() {
		defer func() { _ = recover() }() // the API layer's recovery middleware does this in production
		_, _ = svc.doResolveTracked(context.Background(), "Notch")
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

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected success from the healthy resolver, got error: %v", resErr)
	}
	if result == nil || result.Player == nil {
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

	_, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil {
		t.Fatal("expected an aggregate error when every resolver panics")
	}

	if resErr.Code != constant.CodeUpstreamError {
		t.Errorf("expected code %q, got %q", constant.CodeUpstreamError, resErr.Code)
	}
	if resErr.Message != "All resolver attempts failed" {
		t.Errorf("expected the all-failed aggregate message, got %q", resErr.Message)
	}
}

// stubWrite records one SetCacheBulk call for assertions. A nil Player inside
// the entry is a not-found verdict, a non-nil one is a player entry.
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

func (s *stubCache) GetCache(context.Context, string) (jsontext.Value, time.Time, time.Duration, bool, error) {
	s.probes++
	if s.raw == nil {
		return nil, time.Time{}, 0, false, nil
	}
	return jsontext.Value(s.raw), time.Now(), 30 * time.Second, true, nil
}

func (s *stubCache) SetCacheBulk(_ context.Context, keys []string, data any, _ time.Duration) error {
	s.writes = append(s.writes, stubWrite{keys: keys, data: data})
	return nil
}

// playerWrites counts player-entry writes (non-nil player payload).
func (s *stubCache) playerWrites() int {
	n := 0
	for _, w := range s.writes {
		if e, ok := w.data.(*PlayerCacheEntry); ok && e.Player != nil {
			n++
		}
	}
	return n
}

// notFoundWrites counts not-found verdict writes (nil player payload).
func (s *stubCache) notFoundWrites() int {
	n := 0
	for _, w := range s.writes {
		if e, ok := w.data.(*PlayerCacheEntry); ok && e.Player == nil {
			n++
		}
	}
	return n
}

// cachedPlayerJSON returns the raw payload a stored player entry would have.
func cachedPlayerJSON(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(&PlayerCacheEntry{
		Player:   &model.Player{UUID: dashedTestUUID, Username: "notch"},
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

	_, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil {
		t.Fatalf("expected the not-found verdict to surface as an error")
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

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil {
		t.Fatal("expected the cached not-found verdict to return an error")
	}
	if resErr.Code != constant.CodeNotFound {
		t.Errorf("expected code %q, got %q", constant.CodeNotFound, resErr.Code)
	}

	// Contract check: a cached not-found verdict pairs a non-nil result
	// stub (cache headers) with the typed error. A bare null payload carries
	// no reporter list, so Resolver stays empty.
	if result == nil || !result.CacheHit || result.Player != nil {
		t.Errorf("expected a cache-header stub with the cached-404 error, got %+v", result)
	}
	if result != nil && result.Resolver != "" {
		t.Errorf("expected no reporter list from a bare null payload, got %q", result.Resolver)
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

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected the disabled negative cache to fall through to resolvers, got error: %v", resErr)
	}
	if result == nil || result.Player == nil {
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

	result, resErr := svc.Resolve(context.Background(), "notch")

	if resErr != nil {
		t.Fatalf("expected a positive cache hit, got error: %v", resErr)
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

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected the disabled player cache to fall through to resolvers, got error: %v", resErr)
	}
	if result == nil || result.Player == nil {
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

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected a resolver result, got error: %v", resErr)
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

	_, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected success, got error: %v", resErr)
	}
	if c.playerWrites() != 0 {
		t.Errorf("expected no player cache write with caching disabled, got %d", c.playerWrites())
	}
}

// cannedPlayerResolver always resolves to the same canned player record,
// including a deliberately invalid one when a test needs malformed output.
type cannedPlayerResolver struct {
	name   string
	player *model.Player
}

func (c cannedPlayerResolver) Resolve(context.Context, string) (*model.Player, error) {
	return c.player, nil
}

func (c cannedPlayerResolver) Name() string { return c.name }

const dashedTestUUID = "069a79f4-44e9-4726-a91b-ef990e6b5624"

// cannedNotch returns a valid player whose UUID is stored undashed: the
// service must complete normalization before validating.
func cannedNotch(name string) cannedPlayerResolver {
	return cannedPlayerResolver{
		name:   name,
		player: &model.Player{UUID: "069a79f444e94726a91bef990e6b5624", Username: "Notch"},
	}
}

// A resolver that returns a structurally invalid record — empty fields, a nil
// player with nil error, an unnormalizable UUID — must lose the race to a
// valid resolver, and only the valid record may reach the cache.
func TestMalformedResolverIsSkippedInRace(t *testing.T) {
	c := &stubCache{}
	svc := NewPlayerService(
		[]resolver.Resolver{
			cannedPlayerResolver{name: "nil-player", player: nil},
			cannedPlayerResolver{name: "empty-fields", player: &model.Player{}},
			cannedPlayerResolver{name: "garbage-uuid", player: &model.Player{UUID: "zzzz79f4", Username: "Notch"}},
			cannedNotch("good"),
		},
		constant.StrategyRace,
		1,
		c,
		"test:",
		time.Minute,
		0,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected the valid resolver to win the race, got error: %v", resErr)
	}
	if result.Player == nil || result.Player.Username != "Notch" {
		t.Errorf("expected the valid player, got %+v", result.Player)
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
	if c.playerWrites() != 1 {
		t.Fatalf("expected exactly one player cache write, got %d", c.playerWrites())
	}
	written, ok := c.writes[0].data.(*PlayerCacheEntry)
	if !ok {
		t.Fatalf("expected a *PlayerCacheEntry write, got %T", c.writes[0].data)
	}
	if written.Player == nil || written.Player.UUID != dashedTestUUID {
		t.Errorf("expected the normalized UUID to be cached, got %+v", written.Player)
	}
}

// Same as the race case in fallback: an invalid record must not win, the next
// resolver is tried instead.
func TestMalformedResolverIsSkippedInFallback(t *testing.T) {
	c := &stubCache{}
	svc := NewPlayerService(
		[]resolver.Resolver{
			cannedPlayerResolver{name: "empty-fields", player: &model.Player{}},
			cannedNotch("good"),
		},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		0,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected fallback to reach the valid resolver, got error: %v", resErr)
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
	if c.playerWrites() != 1 {
		t.Fatalf("expected exactly one player cache write, got %d", c.playerWrites())
	}
	written, ok := c.writes[0].data.(*PlayerCacheEntry)
	if !ok {
		t.Fatalf("expected a *PlayerCacheEntry write, got %T", c.writes[0].data)
	}
	if written.Resolver != "good" {
		t.Errorf("expected the valid resolver to be cached, got %q", written.Resolver)
	}
}

// When no resolver produces a valid record, the outcome is UPSTREAM_ERROR, and
// nothing is written to the cache.
func TestAllMalformedResolversAggregateAsUpstreamError(t *testing.T) {
	c := &stubCache{}
	svc := newTestService(
		[]resolver.Resolver{
			cannedPlayerResolver{name: "empty-fields", player: &model.Player{}},
			cannedPlayerResolver{name: "garbage-uuid", player: &model.Player{UUID: "zzzz79f4", Username: "Notch"}},
		},
		constant.StrategyFallback,
	)

	_, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil {
		t.Fatal("expected an aggregate error when every resolver returns invalid data")
	}
	if resErr.Code != constant.CodeUpstreamError {
		t.Errorf("expected code %q, got %q", constant.CodeUpstreamError, resErr.Code)
	}
	if c.playerWrites() != 0 {
		t.Errorf("expected no player cache write, got %d", c.playerWrites())
	}
}

// In race mode the first *valid* record wins, so a malformed resolver's
// failure must not abort the race for the others.
func TestMalformedResolverFailureKeepsRaceAlive(t *testing.T) {
	svc := newTestService(
		[]resolver.Resolver{
			cannedPlayerResolver{name: "empty-fields", player: &model.Player{}},
			cannedNotch("good"),
		},
		constant.StrategyRace,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected the race to still find a valid resolver, got error: %v", resErr)
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
}

// UUIDs are immutable, so a UUID lookup that resolves to a different UUID
// proves the upstream served a foreign record: the result must be treated as
// an upstream failure, and nothing is cached.
func TestUUIDLookupRejectsMismatchedUUID(t *testing.T) {
	c := &stubCache{}
	requested := "069a79f4-44e9-4726-a91b-ef990e6b5624"
	svc := NewPlayerService(
		[]resolver.Resolver{
			cannedPlayerResolver{name: "mismatched", player: &model.Player{
				UUID:     "069a79f4-44e9-4726-a91b-ef990e6b5625",
				Username: "Notch",
			}},
		},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		0,
		nil,
	)

	_, resErr := svc.Resolve(context.Background(), requested)

	if resErr == nil {
		t.Fatal("expected the mismatched UUID to surface as an error")
	}
	if resErr.Code != constant.CodeUpstreamError {
		t.Errorf("expected code %q, got %q", constant.CodeUpstreamError, resErr.Code)
	}
	if c.playerWrites() != 0 {
		t.Errorf("expected no player cache write, got %d", c.playerWrites())
	}
}

// UUID lookups resolving to the requested UUID are valid regardless of the
// returned name.
func TestUUIDLookupAcceptsRequestedUUID(t *testing.T) {
	svc := newTestService(
		[]resolver.Resolver{
			cannedPlayerResolver{name: "matching", player: &model.Player{
				UUID:     dashedTestUUID,
				Username: "Renamed",
			}},
		},
		constant.StrategyFallback,
	)

	result, resErr := svc.Resolve(context.Background(), dashedTestUUID)

	if resErr != nil {
		t.Fatalf("expected success, got error: %v", resErr)
	}
	if result.Resolver != "matching" {
		t.Errorf("expected resolver %q, got %q", "matching", result.Resolver)
	}
	if result.Player == nil || result.Player.Username != "Renamed" {
		t.Errorf("expected the resolver's username, got %+v", result.Player)
	}
}

// Username lookups get NO name cross-check on purpose: upstreams propagate
// name changes at different speeds, so a fresh rename may still resolve to the
// previous name. That result is valid and must be served and cached.
func TestUsernameLookupDoesNotCrossCheckName(t *testing.T) {
	c := &stubCache{}
	svc := NewPlayerService(
		[]resolver.Resolver{
			cannedPlayerResolver{name: "stale-name", player: &model.Player{
				UUID:     dashedTestUUID,
				Username: "OldName",
			}},
		},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		0,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "NewName")

	if resErr != nil {
		t.Fatalf("expected the stale name to be accepted, got error: %v", resErr)
	}
	if result.Player == nil || result.Player.Username != "OldName" {
		t.Errorf("expected the stale username to be served, got %+v", result.Player)
	}
	if c.playerWrites() != 1 {
		t.Errorf("expected the stale-name result to be cached, got %d", c.playerWrites())
	}
}

// A cache entry that parses but violates the player shape must be treated as
// a miss: resolution falls through to the resolvers and repairs the keyspace
// with a valid write. A corrupt positive must never degrade into a negative
// verdict.
func TestCorruptCacheEntryShapeIgnored(t *testing.T) {
	c := &stubCache{raw: []byte(`{"player":{"uuid":"069a79f4-44e9-4726-a91b-ef990e6b5624","username":""},"resolver":"stored"}`)}
	svc := NewPlayerService(
		[]resolver.Resolver{cannedNotch("good")},
		constant.StrategyFallback,
		1,
		c,
		"test:",
		time.Minute,
		0,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected the invalid entry to be treated as a miss, got error: %v", resErr)
	}
	if result.CacheHit {
		t.Error("expected a resolver result, not a cache hit")
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
	if c.playerWrites() != 1 {
		t.Errorf("expected the re-resolve to overwrite the corrupt entry, got %d writes", c.playerWrites())
	}
}

// rateLimitedResolver always reports RATE_LIMITED.
type rateLimitedResolver struct{ name string }

func (r rateLimitedResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	return nil, resolver.NewRateLimitError(r.name, nil)
}

func (r rateLimitedResolver) Name() string { return r.name }

// blockingResolver blocks until its context is cancelled, then records that it
// observed the cancellation.
type blockingResolver struct {
	name      string
	cancelled chan struct{}
}

func (b *blockingResolver) Resolve(ctx context.Context, identifier string) (*model.Player, error) {
	<-ctx.Done()
	close(b.cancelled)
	return nil, resolver.NewTimeoutError(b.name, ctx.Err())
}

func (b *blockingResolver) Name() string { return b.name }

// In race mode, once the NOT_FOUND quorum reports, the verdict is conclusive:
// resolution must return immediately and cancel the stragglers instead of
// waiting for every resolver to finish.
func TestRaceFastReturnOnNotFoundQuorum(t *testing.T) {
	blocked := &blockingResolver{name: "blocked", cancelled: make(chan struct{})}
	svc := NewPlayerService(
		[]resolver.Resolver{
			notFoundResolver{name: "nf-a"},
			notFoundResolver{name: "nf-b"},
			blocked,
		},
		constant.StrategyRace,
		2,
		nopCache{},
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil || resErr.Code != constant.CodeNotFound {
		t.Fatalf("expected a NOT_FOUND verdict once the quorum reported it, got %v", resErr)
	}
	if resErr.Message != "Player not found" {
		t.Errorf("expected the generic not-found message, got %q", resErr.Message)
	}
	if result == nil {
		t.Fatal("expected a result stub with the error")
	}
	// Race arrival order is non-deterministic, so compare as a set.
	reporters := strings.Split(result.Resolver, ",")
	sort.Strings(reporters)
	if len(reporters) != 2 || reporters[0] != "nf-a" || reporters[1] != "nf-b" {
		t.Errorf("expected the not-found reporters in Resolver, got %q", result.Resolver)
	}

	select {
	case <-blocked.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the blocking resolver to be cancelled after the quorum was reached")
	}
}

// A single NOT_FOUND below the quorum must not decide the race: the healthy
// resolver still wins.
func TestRaceWaitsForSuccessWhenQuorumNotReached(t *testing.T) {
	svc := NewPlayerService(
		[]resolver.Resolver{
			notFoundResolver{name: "nf-a"},
			rateLimitedResolver{name: "rl"},
			okResolver{name: "good"},
		},
		constant.StrategyRace,
		2,
		nopCache{},
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected the healthy resolver to win below the quorum, got error: %v", resErr)
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
}

// In fallback mode, reaching the NOT_FOUND quorum must stop the walk: the
// resolvers after the quorum are never consulted.
func TestFallbackStopsAtNotFoundQuorum(t *testing.T) {
	counted := &countingResolver{name: "counted"}
	svc := NewPlayerService(
		[]resolver.Resolver{
			notFoundResolver{name: "nf-a"},
			notFoundResolver{name: "nf-b"},
			counted,
			okResolver{name: "good"},
		},
		constant.StrategyFallback,
		2,
		nopCache{},
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil || resErr.Code != constant.CodeNotFound {
		t.Fatalf("expected a NOT_FOUND verdict once the quorum reported it, got %v", resErr)
	}
	if result == nil || result.Resolver != "nf-a,nf-b" {
		t.Errorf("expected the not-found reporters in Resolver, got %+v", result)
	}
	if counted.calls != 0 {
		t.Errorf("expected fallback to stop at the quorum, got %d later resolver calls", counted.calls)
	}
}

// Below the quorum, fallback must keep walking: a NOT_FOUND followed by other
// failures must not prevent the later success.
func TestFallbackContinuesWhenQuorumNotReached(t *testing.T) {
	svc := NewPlayerService(
		[]resolver.Resolver{
			notFoundResolver{name: "nf-a"},
			rateLimitedResolver{name: "rl"},
			okResolver{name: "good"},
		},
		constant.StrategyFallback,
		2,
		nopCache{},
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr != nil {
		t.Fatalf("expected fallback to reach the healthy resolver below the quorum, got error: %v", resErr)
	}
	if result.Resolver != "good" {
		t.Errorf("expected resolver %q, got %q", "good", result.Resolver)
	}
}

// A conclusive not-found verdict carries the comma-joined reporter list in the
// result stub and in the negative cache entry (for the cached-404 header).
func TestNotFoundVerdictCachesReporterList(t *testing.T) {
	c := &stubCache{}
	svc := NewPlayerService(
		[]resolver.Resolver{
			notFoundResolver{name: "mojang"},
			notFoundResolver{name: "playerdb"},
		},
		constant.StrategyFallback,
		2,
		c,
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil || resErr.Code != constant.CodeNotFound {
		t.Fatalf("expected a NOT_FOUND verdict, got %v", resErr)
	}
	if result == nil || result.Resolver != "mojang,playerdb" {
		t.Errorf("expected the not-found reporters in Resolver, got %+v", result)
	}

	written, ok := c.writes[0].data.(*PlayerCacheEntry)
	if !ok {
		t.Fatalf("expected a *PlayerCacheEntry write, got %T", c.writes[0].data)
	}
	if written.Player != nil {
		t.Errorf("expected a nil player payload on the negative verdict, got %+v", written.Player)
	}
	if written.Resolver != "mojang,playerdb" {
		t.Errorf("expected the reporter list to be cached, got %q", written.Resolver)
	}
}

// A cached negative verdict stores the reporter list, so cached 404s can set
// the X-Resolver header too.
func TestCachedNotFoundCarriesReporterList(t *testing.T) {
	upstream := &countingResolver{name: "counted"}
	c := &stubCache{raw: []byte(`{"player":null,"resolver":"mojang,playerdb"}`)}
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

	result, resErr := svc.Resolve(context.Background(), "Notch")

	if resErr == nil || resErr.Code != constant.CodeNotFound {
		t.Fatalf("expected the cached not-found verdict to return an error, got %v", resErr)
	}
	if result == nil || result.Resolver != "mojang,playerdb" {
		t.Errorf("expected the cached reporter list in Resolver, got %+v", result)
	}
	if upstream.calls != 0 {
		t.Errorf("expected resolvers to be short-circuited, got %d calls", upstream.calls)
	}
}
