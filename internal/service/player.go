// Package service implements player resolution.
package service

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/saegusamayumi1234/mc-lookup/internal/cache"
	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
)

// ResolveResult is the outcome of one player resolve.
type ResolveResult struct {
	Player    *model.Player
	Resolver  string
	CacheHit  bool
	CacheTTL  time.Duration
	Coalesced bool
}

// newError converts a resolver error into the service error type.
func newError(resErr *resolver.ResolverError) *Error {
	if resErr == nil {
		return nil
	}

	return &Error{
		Code:       resErr.Code,
		Message:    resErr.Message,
		StatusCode: resErr.StatusCode,
		Cause:      resErr,
	}
}

// PlayerService resolves players through resolvers with caching and singleflight.
type PlayerService struct {
	resolvers        []resolver.Resolver
	strategy         string
	notFoundMinCount int
	cache            cache.Cache
	cachePrefix      string
	cacheTTL         time.Duration
	negativeCacheTTL time.Duration
	sfGroup          singleflight.Group
	inflightResolves sync.WaitGroup
	logger           *slog.Logger
}

// PlayerCacheEntry is the cached player payload.
type PlayerCacheEntry struct {
	Player   *model.Player `json:"player"`
	Resolver string        `json:"resolver"`
}

// NewPlayerService builds a PlayerService; strategy and resolvers are validated by config.
func NewPlayerService(
	resolvers []resolver.Resolver,
	strategy string,
	notFoundMinCount int,
	c cache.Cache,
	cachePrefix string,
	cacheTTL time.Duration,
	negativeCacheTTL time.Duration,
	logger *slog.Logger,
) *PlayerService {
	if logger == nil {
		logger = slog.Default()
	}

	return &PlayerService{
		resolvers:        resolvers,
		strategy:         strategy,
		notFoundMinCount: notFoundMinCount,
		cache:            c,
		cacheTTL:         cacheTTL,
		cachePrefix:      cachePrefix,
		negativeCacheTTL: negativeCacheTTL,
		logger:           logger,
	}
}

// WaitInflightResolves blocks until in-flight leader resolves finish or ctx is done.
func (s *PlayerService) WaitInflightResolves(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.inflightResolves.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// flightResult boxes one resolve outcome for the singleflight leader and followers.
type flightResult struct {
	result *ResolveResult
	err    *Error
}

// Resolve resolves a username or UUID, short-circuiting on cache hits.
func (s *PlayerService) Resolve(ctx context.Context, identifier string) (*ResolveResult, *Error) {
	if !model.IsValidIdentifier(identifier) {
		return nil, newError(resolver.NewInvalidInputError("", identifier))
	}

	key := model.NormalizeIdentifier(identifier)

	if hit := s.getPlayerCache(ctx, key); hit != nil {
		return s.cachedOutcome(hit)
	}

	ch := s.sfGroup.DoChan(key, func() (any, error) {
		return s.flight(key, identifier, ctx), nil
	})

	select {
	case r := <-ch:
		return s.unwrap(r)
	case <-ctx.Done():
		// Prefer a ready shared result over a lost cancellation race.
		select {
		case r := <-ch:
			return s.unwrap(r)
		default:
			return nil, newError(resolver.NewTimeoutError("", ctx.Err()))
		}
	}
}

// flight is the singleflight closure: re-probe cache, then resolve detached.
func (s *PlayerService) flight(key, identifier string, callerCtx context.Context) flightResult {
	// Detach so the leader finishes and caches even if its caller leaves.
	ctx := context.WithoutCancel(callerCtx)

	// Re-probe so followers inherit a verdict the leader just cached.
	if hit := s.getPlayerCache(ctx, key); hit != nil {
		result, err := s.cachedOutcome(hit)
		return flightResult{result: result, err: err}
	}

	result, err := s.doResolveTracked(ctx, identifier)
	return flightResult{result: result, err: err}
}

// unwrap converts the shared flight result into the caller outcome.
func (s *PlayerService) unwrap(r singleflight.Result) (*ResolveResult, *Error) {
	fr, ok := r.Val.(flightResult)
	if !ok {
		s.logger.Error("unexpected singleflight result", "value", r.Val)
		return nil, internalErrorResult()
	}

	if fr.err != nil {
		return fr.result, fr.err
	}

	if fr.result == nil {
		s.logger.Error("missing singleflight result", "value", r.Val)
		return nil, internalErrorResult()
	}

	res := *fr.result
	if r.Shared && !res.CacheHit {
		res.Coalesced = true
	}
	return &res, nil
}

// doResolveTracked runs doResolve under the inflight counter for graceful shutdown.
func (s *PlayerService) doResolveTracked(ctx context.Context, identifier string) (*ResolveResult, *Error) {
	s.inflightResolves.Add(1)
	defer s.inflightResolves.Done()
	return s.doResolve(ctx, identifier)
}

// doResolve dispatches to the configured strategy; config guarantees a known strategy.
func (s *PlayerService) doResolve(ctx context.Context, identifier string) (*ResolveResult, *Error) {
	if s.strategy == constant.StrategyRace {
		return s.resolveRace(ctx, identifier)
	}
	return s.resolveFallback(ctx, identifier)
}

// resolveRace queries all resolvers concurrently; first success wins.
func (s *PlayerService) resolveRace(ctx context.Context, identifier string) (*ResolveResult, *Error) {
	resolveCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type attempt struct {
		player   *model.Player
		resolver string
		err      *resolver.ResolverError
	}

	// Buffered to the resolver count, so sends never block.
	resultCh := make(chan attempt, len(s.resolvers))

	var wg sync.WaitGroup
	for _, r := range s.resolvers {
		wg.Add(1)
		go func(res resolver.Resolver) {
			defer wg.Done()

			p, err := s.safeResolve(resolveCtx, res, identifier)

			var resErr *resolver.ResolverError
			if err != nil {
				resErr = wrapResolverError(res.Name(), err)
			}

			resultCh <- attempt{player: p, resolver: res.Name(), err: resErr}
		}(r)
	}
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	resErrs := make([]*resolver.ResolverError, 0, len(s.resolvers))
	notFoundCount := 0
	for res := range resultCh {
		if res.err == nil && res.player != nil {
			cancel()
			return s.freshResult(ctx, res.player, res.resolver), nil
		}
		if res.err != nil {
			if res.err.Code == constant.CodeNotFound {
				notFoundCount++
			}
			resErrs = append(resErrs, res.err)

			if s.notFoundMinCount > 0 && notFoundCount >= s.notFoundMinCount {
				cancel()
				return s.failedResult(ctx, identifier, resErrs)
			}
		}
	}

	return s.failedResult(ctx, identifier, resErrs)
}

// resolveFallback tries resolvers in order; first success wins.
func (s *PlayerService) resolveFallback(ctx context.Context, identifier string) (*ResolveResult, *Error) {
	resErrs := make([]*resolver.ResolverError, 0, len(s.resolvers))
	notFoundCount := 0

	for _, res := range s.resolvers {
		player, err := s.safeResolve(ctx, res, identifier)
		if err == nil && player != nil {
			return s.freshResult(ctx, player, res.Name()), nil
		}

		resErr := wrapResolverError(res.Name(), err)
		if resErr.Code == constant.CodeNotFound {
			notFoundCount++
		}
		resErrs = append(resErrs, resErr)

		if s.notFoundMinCount > 0 && notFoundCount >= s.notFoundMinCount {
			break
		}
	}

	return s.failedResult(ctx, identifier, resErrs)
}

// safeResolve recover panic and reject malformed data
func (s *PlayerService) safeResolve(ctx context.Context, res resolver.Resolver, identifier string) (p *model.Player, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in resolver", "resolver", res.Name(), "panic", r, "stack", string(debug.Stack()))
			p, err = nil, fmt.Errorf("resolver panicked: %v", r)
		}
	}()

	player, resolveErr := res.Resolve(ctx, identifier)
	if resolveErr != nil {
		return nil, resolveErr
	}

	if validateErr := model.ValidatePlayer(player); validateErr != nil {
		return nil, fmt.Errorf("upstream returned invalid player data: %w", validateErr)
	}

	player.UUID = model.NormalizeUUID(player.UUID)

	// Cross-check UUID lookups
	if model.IsUUID(identifier) && player.UUID != model.NormalizeUUID(identifier) {
		return nil, fmt.Errorf("upstream resolved UUID %q for requested UUID %q", player.UUID, model.NormalizeUUID(identifier))
	}

	return player, nil
}

// wrapResolverError normalizes a failure into *ResolverError; errors.As preserves typed codes.
func wrapResolverError(resolverName string, err error) *resolver.ResolverError {
	if err == nil {
		return resolver.NewUpstreamError(resolverName, 0, errors.New("resolver returned no player"))
	}

	var resErr *resolver.ResolverError
	if errors.As(err, &resErr) {
		return resErr
	}

	return resolver.NewUpstreamError(resolverName, 0, err)
}

// freshResult normalizes the player, caches it, and returns a fresh resolver result.
func (s *PlayerService) freshResult(ctx context.Context, player *model.Player, resolverName string) *ResolveResult {
	s.setPlayerCache(ctx, player, resolverName)

	return &ResolveResult{
		Player:   player,
		Resolver: resolverName,
		CacheTTL: s.cacheTTL,
	}
}

// failedResult aggregates resolver errors; a conclusive not-found is negative-cached.
func (s *PlayerService) failedResult(ctx context.Context, identifier string, resErrs []*resolver.ResolverError) (*ResolveResult, *Error) {
	agg := resolver.AggregateErrors(resErrs, s.notFoundMinCount)

	result := &ResolveResult{}
	if agg.FinalCode == constant.CodeNotFound {
		result.Resolver = strings.Join(notFoundReporters(resErrs), ",")
		result.CacheTTL = s.negativeCacheTTL
		s.setPlayerNotFoundCache(ctx, identifier, result.Resolver)
	}

	return result, &Error{
		Code:       agg.FinalCode,
		Message:    agg.FinalMessage,
		StatusCode: agg.FinalStatus,
	}
}

// internalErrorResult is the fail-closed outcome for unexpected failures; details go to logs.
func internalErrorResult() *Error {
	return &Error{
		Code:       constant.CodeInternalServerError,
		Message:    "Internal server error",
		StatusCode: http.StatusInternalServerError,
	}
}

// notFoundReporters returns the deduplicated names of resolvers that reported NOT_FOUND
func notFoundReporters(resErrs []*resolver.ResolverError) []string {
	seen := make(map[string]struct{}, len(resErrs))
	names := make([]string, 0, len(resErrs))
	for _, err := range resErrs {
		if err.Code != constant.CodeNotFound || err.Resolver == "" {
			continue
		}
		if _, dup := seen[err.Resolver]; dup {
			continue
		}
		seen[err.Resolver] = struct{}{}
		names = append(names, err.Resolver)
	}
	return names
}

type playerCacheHit struct {
	entry *PlayerCacheEntry
	ttl   time.Duration
}

func (s *PlayerService) cachedOutcome(hit *playerCacheHit) (*ResolveResult, *Error) {
	if hit.entry.Player == nil {
		return &ResolveResult{
			CacheHit: true,
			CacheTTL: hit.ttl,
			Resolver: hit.entry.Resolver,
		}, &Error{
			Code:       constant.CodeNotFound,
			Message:    "Player not found",
			StatusCode: http.StatusNotFound,
		}
	}

	return &ResolveResult{
		Player:   hit.entry.Player,
		Resolver: hit.entry.Resolver,
		CacheHit: true,
		CacheTTL: hit.ttl,
	}, nil
}

// getPlayerCache probes the shared keyspace; nil means miss.
func (s *PlayerService) getPlayerCache(ctx context.Context, key string) *playerCacheHit {
	if s.cacheTTL <= 0 && s.negativeCacheTTL <= 0 {
		return nil
	}

	cacheKey := s.cachePrefix + key

	raw, _, ttl, found, err := s.cache.GetCache(ctx, cacheKey)
	if err != nil {
		s.logger.Warn("player cache probe failed", "key", cacheKey, "error", err)
		return nil
	}
	if !found {
		return nil
	}

	// An empty payload cannot name reporters but still marks a negative verdict.
	if len(raw) == 0 {
		if s.negativeCacheTTL <= 0 {
			return nil
		}
		return &playerCacheHit{entry: &PlayerCacheEntry{}, ttl: ttl}
	}

	var entry PlayerCacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		s.logger.Warn("corrupt player cache entry", "key", cacheKey, "error", err)
		return nil
	}

	// A nil player payload is a not-found verdict.
	if entry.Player == nil {
		if s.negativeCacheTTL <= 0 {
			return nil
		}
		return &playerCacheHit{entry: &entry, ttl: ttl}
	}

	if s.cacheTTL <= 0 {
		return nil
	}

	// Parseable does not mean valid: treat a foreign or buggy entry as a miss.
	if err := model.ValidatePlayer(entry.Player); err != nil {
		s.logger.Warn("corrupt player cache entry", "key", cacheKey, "error", err)
		return nil
	}

	return &playerCacheHit{entry: &entry, ttl: ttl}
}

// setPlayerCache writes the player under both identifier keys; ctx must outlive the request.
func (s *PlayerService) setPlayerCache(ctx context.Context, player *model.Player, resolverName string) {
	if s.cacheTTL <= 0 {
		return
	}

	data := &PlayerCacheEntry{
		Player:   &model.Player{UUID: player.UUID, Username: player.Username},
		Resolver: resolverName,
	}

	keys := []string{
		s.cachePrefix + model.NormalizeIdentifier(player.UUID),
		s.cachePrefix + model.NormalizeIdentifier(player.Username),
	}

	if err := s.cache.SetCacheBulk(ctx, keys, data, s.cacheTTL); err != nil {
		s.logger.Warn("player cache write failed", "keys", keys, "error", err)
	}
}

// setPlayerNotFoundCache writes a null verdict under the identifier key.
func (s *PlayerService) setPlayerNotFoundCache(ctx context.Context, identifier string, notFoundBy string) {
	if s.negativeCacheTTL <= 0 {
		return
	}

	keys := []string{s.cachePrefix + model.NormalizeIdentifier(identifier)}

	data := &PlayerCacheEntry{Resolver: notFoundBy}

	if err := s.cache.SetCacheBulk(ctx, keys, data, s.negativeCacheTTL); err != nil {
		s.logger.Warn("not-found cache write failed", "keys", keys, "error", err)
	}
}
