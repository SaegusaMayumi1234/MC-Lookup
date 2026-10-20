package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	resolvers            []resolver.Resolver
	strategy             string
	notFoundMinCount     int
	cache                cache.Cache
	cacheTTL             time.Duration
	cachePrefix          string
	negativeCacheEnabled bool
	negativeCachePrefix  string
	negativeCacheTTL     time.Duration
	sfGroup              singleflight.Group
	inflightResolves     sync.WaitGroup
}

func NewPlayerService(
	resolvers []resolver.Resolver,
	strategy string,
	notFoundMinCount int,
	c cache.Cache,
	cacheTTL time.Duration,
	cachePrefix string,
	negativeCacheEnabled bool,
	negativeCachePrefix string,
	negativeCacheTTL time.Duration,
) *PlayerService {
	strategy = strings.ToLower(strings.TrimSpace(strategy))

	return &PlayerService{
		resolvers:            resolvers,
		strategy:             strategy,
		notFoundMinCount:     notFoundMinCount,
		cache:                c,
		cacheTTL:             cacheTTL,
		cachePrefix:          cachePrefix,
		negativeCacheEnabled: negativeCacheEnabled,
		negativeCachePrefix:  negativeCachePrefix,
		negativeCacheTTL:     negativeCacheTTL,
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

	if result := s.getCachedResult(normalizedKey); result != nil {
		return result
	}

	if result := s.getCachedNotFound(normalizedKey); result != nil {
		return result
	}

	resultCh := s.sfGroup.DoChan(normalizedKey, func() (any, error) {
		resolveCtx := context.WithoutCancel(ctx)
		if cached := s.getCachedResult(normalizedKey); cached != nil {
			return cached, nil
		}
		if cached := s.getCachedNotFound(normalizedKey); cached != nil {
			return cached, nil
		}

		// track inflightResolves for graceful shutdown.
		s.inflightResolves.Add(1)
		result := s.doResolve(resolveCtx, identifier)
		s.inflightResolves.Done()

		return result, nil
	})

	select {
	case <-ctx.Done():
		return &ResolveResult{Error: ctx.Err()}
	case result := <-resultCh:
		if result.Err != nil {
			return &ResolveResult{
				Error: &resolver.ResolverError{
					Code:       constant.CodeInternalServerError,
					Message:    result.Err.Error(),
					StatusCode: http.StatusInternalServerError,
				},
			}
		}

		res := result.Val.(*ResolveResult)

		callerResult := *res
		if result.Shared && !callerResult.CacheHit {
			callerResult.Coalesced = true
		}
		return &callerResult
	}
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

			p, err := res.Resolve(resolveCtx, identifier)

			var resErr *resolver.ResolverError
			if err != nil {
				if re, ok := err.(*resolver.ResolverError); ok {
					resErr = re
				} else {
					resErr = resolver.NewUpstreamError(res.Name(), 0, err)
				}
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
		player, err := res.Resolve(ctx, identifier)

		if err == nil && player != nil {
			return s.newSuccessfulResolveResult(player, res.Name())
		}

		errors = append(errors, wrapResolverError(res.Name(), err))
	}

	return s.aggregateResolveErrors(identifier, errors)
}

func (s *PlayerService) newSuccessfulResolveResult(player *model.Player, resolverName string) *ResolveResult {
	player.UUID = model.NormalizeUUID(player.UUID)
	s.cachePlayer(player, resolverName)

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

	if s.negativeCacheEnabled && s.negativeCacheTTL > 0 && aggErr.FinalCode == constant.CodeNotFound {
		s.cache.SetNotFoundCache(s.negativeCachePrefix, identifier, s.negativeCacheTTL)
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

func (s *PlayerService) getCachedResult(key string) *ResolveResult {
	entry, found := s.cache.GetPlayerCache(s.cachePrefix, key)
	if !found {
		return nil
	}

	return &ResolveResult{
		Player: &model.Player{
			UUID:     entry.Player.UUID,
			Username: entry.Player.Username,
		},
		Resolver: entry.Resolver,
		CacheHit: true,
		CacheTTL: entry.TTL(),
		CacheAge: entry.Age(),
	}
}

// getCachedNotFound returns a cached not-found verdict, or nil on miss/when
// negative caching is disabled.
func (s *PlayerService) getCachedNotFound(key string) *ResolveResult {
	if !s.negativeCacheEnabled {
		return nil
	}

	ttl, found := s.cache.GetNotFoundCache(s.negativeCachePrefix, key)
	if !found {
		return nil
	}

	return &ResolveResult{
		Error: &resolver.ResolverError{
			Code:       constant.CodeNotFound,
			Message:    "Player not found (cached)",
			StatusCode: http.StatusNotFound,
		},
		CacheHit: true,
		CacheTTL: ttl,
	}
}

func (s *PlayerService) cachePlayer(player *model.Player, resolverName string) {
	cacheData := &cache.PlayerData{
		UUID:     player.UUID,
		Username: player.Username,
	}

	s.cache.SetPlayerCache(s.cachePrefix, cacheData, resolverName, s.cacheTTL)
}
