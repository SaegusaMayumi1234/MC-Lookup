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

type ResolveResult struct {
	Player    *model.Player
	Resolver  string
	CacheHit  bool
	CacheTTL  time.Duration
	CacheAge  time.Duration
	Coalesced bool
	Error     error
}

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

type PlayerCacheEntry struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Resolver string `json:"resolver"`
}

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
	strategy = strings.ToLower(strings.TrimSpace(strategy))

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

// WaitInflightResolves blocks until all in-flight leader resolves have finished,
// or until ctx is done.
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

// Resolve attempts to resolve a player identifier using the configured strategy and resolvers.
func (s *PlayerService) Resolve(ctx context.Context, identifier string) *ResolveResult {
	if !model.IsValidIdentifier(identifier) {
		// Service-level validation; no resolver is involved, so the error carries no resolver name.
		return &ResolveResult{
			Error: resolver.NewInvalidInputError("", identifier),
		}
	}

	normalizedKey := model.NormalizeIdentifier(identifier)

	if hit := s.getPlayerCache(normalizedKey); hit != nil {
		return s.cachedResolveResult(hit)
	}

	resultCh := s.sfGroup.DoChan(normalizedKey, func() (any, error) {
		resolveCtx := context.WithoutCancel(ctx)
		if hit := s.getPlayerCache(normalizedKey); hit != nil {
			return s.cachedResolveResult(hit), nil
		}

		result := s.doResolveTracked(resolveCtx, identifier)

		return result, nil
	})

	select {
	case <-ctx.Done():
		return &ResolveResult{Error: ctx.Err()}
	case result := <-resultCh:
		if result.Err != nil {
			s.logger.Error("singleflight resolve failed", "error", result.Err)
			return &ResolveResult{
				Error: &resolver.ResolverError{
					Code:       constant.CodeInternalServerError,
					Message:    "Internal server error",
					StatusCode: http.StatusInternalServerError,
				},
			}
		}

		res, ok := result.Val.(*ResolveResult)
		if !ok {
			s.logger.Error("unexpected singleflight result type", "value", result.Val)
			return &ResolveResult{
				Error: &resolver.ResolverError{
					Code:       constant.CodeInternalServerError,
					Message:    "Internal server error",
					StatusCode: http.StatusInternalServerError,
				},
			}
		}

		callerResult := *res
		if result.Shared && !callerResult.CacheHit {
			callerResult.Coalesced = true
		}
		return &callerResult
	}
}

// doResolveTracked runs doResolve while tracking it in inflightResolves for graceful shutdown; both Add and deferred Done live here.
func (s *PlayerService) doResolveTracked(ctx context.Context, identifier string) *ResolveResult {
	s.inflightResolves.Add(1)
	defer s.inflightResolves.Done()
	return s.doResolve(ctx, identifier)
}

// safeResolve calls res.Resolve and converts panics into errors; a panic escaping the singleflight closure crashes the process.
func (s *PlayerService) safeResolve(ctx context.Context, res resolver.Resolver, identifier string) (p *model.Player, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in resolver",
				"resolver", res.Name(), "panic", r, "stack", string(debug.Stack()))
			p = nil
			err = fmt.Errorf("resolver panicked: %v", r)
		}
	}()

	return res.Resolve(ctx, identifier)
}

// doResolve dispatches player resolution to the configured strategy.
func (s *PlayerService) doResolve(ctx context.Context, identifier string) *ResolveResult {
	if len(s.resolvers) == 0 {
		return noResolversResult()
	}

	switch s.strategy {
	case constant.StrategyFallback:
		return s.doResolveFallback(ctx, identifier)
	case constant.StrategyRace:
		return s.doResolveRace(ctx, identifier)
	default:
		return &ResolveResult{
			Error: &resolver.ResolverError{
				Code:       constant.CodeInternalServerError,
				Message:    fmt.Sprintf("invalid strategy: %q", s.strategy),
				StatusCode: http.StatusInternalServerError,
			},
		}
	}
}

func (s *PlayerService) doResolveRace(ctx context.Context, identifier string) *ResolveResult {
	resolveCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		player   *model.Player
		resolver string
		err      *resolver.ResolverError
	}

	resultCh := make(chan result, len(s.resolvers))
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

			select {
			case resultCh <- result{player: p, resolver: res.Name(), err: resErr}:
			case <-resolveCtx.Done():
			}
		}(r)
	}

	// Close result channel when all goroutines complete
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	var errors []*resolver.ResolverError

	for res := range resultCh {
		if res.err == nil && res.player != nil {
			cancel()

			return s.newSuccessfulResolveResult(res.player, res.resolver)
		}

		if res.err != nil {
			errors = append(errors, res.err)
		}
	}

	return s.aggregateResolveErrors(identifier, errors)
}

func (s *PlayerService) doResolveFallback(ctx context.Context, identifier string) *ResolveResult {
	errors := make([]*resolver.ResolverError, 0, len(s.resolvers))

	for _, res := range s.resolvers {
		player, err := s.safeResolve(ctx, res, identifier)

		if err == nil && player != nil {
			return s.newSuccessfulResolveResult(player, res.Name())
		}

		errors = append(errors, wrapResolverError(res.Name(), err))
	}

	return s.aggregateResolveErrors(identifier, errors)
}

func (s *PlayerService) newSuccessfulResolveResult(player *model.Player, resolverName string) *ResolveResult {
	player.UUID = model.NormalizeUUID(player.UUID)
	s.setPlayerCache(player, resolverName)

	return &ResolveResult{
		Player:   player,
		Resolver: resolverName,
		CacheHit: false,
		CacheAge: 0,
		CacheTTL: s.cacheTTL,
	}
}

func wrapResolverError(resolverName string, err error) *resolver.ResolverError {
	if err == nil {
		return resolver.NewUpstreamError(resolverName, 0, errors.New("resolver returned no player"))
	}

	if re, ok := err.(*resolver.ResolverError); ok {
		return re
	}

	return resolver.NewUpstreamError(resolverName, 0, err)
}

func (s *PlayerService) aggregateResolveErrors(identifier string, errors []*resolver.ResolverError) *ResolveResult {
	if len(errors) == 0 {
		return noResolversResult()
	}

	aggErr := resolver.AggregateErrors(errors, s.notFoundMinCount)

	if aggErr.FinalCode == constant.CodeNotFound {
		s.setPlayerNotFoundCache(identifier)
	}

	return &ResolveResult{
		Error: &resolver.ResolverError{
			Code:       aggErr.FinalCode,
			Message:    aggErr.FinalMessage,
			StatusCode: aggErr.FinalStatus,
			Details:    buildErrorDetails(errors),
		},
	}
}

func noResolversResult() *ResolveResult {
	return &ResolveResult{
		Error: &resolver.ResolverError{
			Code:       constant.CodeNoResolversAvailable,
			Message:    "No resolvers available",
			StatusCode: http.StatusInternalServerError,
		},
	}
}

func buildErrorDetails(errors []*resolver.ResolverError) map[string]string {
	details := make(map[string]string)
	for _, err := range errors {
		// Skip empty resolver names so the JSON details map never gets a "" key.
		if err.Resolver == "" {
			continue
		}
		details[err.Resolver] = err.Message
	}
	return details
}

type playerCacheHit struct {
	entry    *PlayerCacheEntry
	cachedAt time.Time
	ttl      time.Duration
}

func (s *PlayerService) cachedResolveResult(hit *playerCacheHit) *ResolveResult {
	if hit.entry == nil {
		return &ResolveResult{
			Error: &resolver.ResolverError{
				Code:       constant.CodeNotFound,
				Message:    "Player not found (cached)",
				StatusCode: http.StatusNotFound,
			},
			CacheHit: true,
			CacheTTL: hit.ttl,
			CacheAge: time.Since(hit.cachedAt),
		}
	}

	return &ResolveResult{
		Player: &model.Player{
			UUID:     hit.entry.UUID,
			Username: hit.entry.Username,
		},
		Resolver: hit.entry.Resolver,
		CacheHit: true,
		CacheTTL: hit.ttl,
		CacheAge: time.Since(hit.cachedAt),
	}
}

func (s *PlayerService) getPlayerCache(identifier string) *playerCacheHit {
	if s.cacheTTL <= 0 && s.negativeCacheTTL <= 0 {
		return nil
	}

	key := s.cachePrefix + model.NormalizeIdentifier(identifier)

	raw, cachedAt, ttl, found, err := s.cache.GetCache(key)
	if err != nil {
		s.logger.Warn("player cache probe failed", "key", key, "error", err)
		return nil
	}
	if !found {
		return nil
	}

	// Negative verdicts are stored with a null payload.
	if len(raw) == 0 || string(raw) == "null" {
		if s.negativeCacheTTL <= 0 {
			return nil
		}
		return &playerCacheHit{cachedAt: cachedAt, ttl: ttl}
	}

	if s.cacheTTL <= 0 {
		return nil
	}

	var entry PlayerCacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		s.logger.Warn("corrupt player cache entry", "key", key, "error", err)
		return nil
	}

	return &playerCacheHit{entry: &entry, cachedAt: cachedAt, ttl: ttl}
}

func (s *PlayerService) setPlayerCache(player *model.Player, resolverName string) {
	if s.cacheTTL <= 0 {
		return
	}

	cacheData := &PlayerCacheEntry{
		UUID:     player.UUID,
		Username: player.Username,
		Resolver: resolverName,
	}

	keys := []string{
		s.cachePrefix + model.NormalizeIdentifier(player.UUID),
		s.cachePrefix + model.NormalizeIdentifier(player.Username),
	}

	if err := s.cache.SetCacheBulk(keys, cacheData, s.cacheTTL); err != nil {
		s.logger.Warn("player cache write failed", "keys", keys, "error", err)
	}
}

func (s *PlayerService) setPlayerNotFoundCache(identifier string) {
	if s.negativeCacheTTL <= 0 {
		return
	}

	keys := []string{
		s.cachePrefix + model.NormalizeIdentifier(identifier),
	}

	if err := s.cache.SetCacheBulk(keys, nil, s.negativeCacheTTL); err != nil {
		s.logger.Warn("not-found cache write failed", "keys", keys, "error", err)
	}
}
