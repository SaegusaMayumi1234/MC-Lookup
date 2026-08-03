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
	resolvers   []resolver.Resolver
	strategy    string
	cache       cache.Cache
	cacheTTL    time.Duration
	cachePrefix string
	sfGroup     singleflight.Group
}

func NewPlayerService(resolvers []resolver.Resolver, strategy string, c cache.Cache, cacheTTL time.Duration, cachePrefix string) *PlayerService {
	strategy = strings.ToLower(strings.TrimSpace(strategy))

	return &PlayerService{
		resolvers:   resolvers,
		strategy:    strategy,
		cache:       c,
		cacheTTL:    cacheTTL,
		cachePrefix: cachePrefix,
	}
}

// Resolve attempts to resolve a player identifier using the configured strategy and resolvers.
func (s *PlayerService) Resolve(ctx context.Context, identifier string) *ResolveResult {
	if !model.IsValidIdentifier(identifier) {
		return &ResolveResult{
			Error: resolver.NewInvalidInputError(identifier),
		}
	}

	normalizedKey := model.NormalizeIdentifier(identifier)

	if result := s.getCachedResult(normalizedKey); result != nil {
		return result
	}

	result, err, shared := s.sfGroup.Do(normalizedKey, func() (any, error) {
		if cached := s.getCachedResult(normalizedKey); cached != nil {
			return cached, nil
		}

		return s.doResolve(ctx, identifier), nil
	})

	if err != nil {
		return &ResolveResult{
			Error: &resolver.ResolverError{
				Code:       constant.CodeInternalServerError,
				Message:    err.Error(),
				StatusCode: http.StatusInternalServerError,
			},
		}
	}

	res := result.(*ResolveResult)

	if shared && !res.CacheHit {
		res.Coalesced = true
	}

	return res
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

	return aggregateResolveErrors(errors)
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

	return aggregateResolveErrors(errors)
}

func (s *PlayerService) newSuccessfulResolveResult(player *model.Player, resolverName string) *ResolveResult {
	player.UUID = model.NormalizeUUID(player.UUID)
	s.cachePlayer(player, resolverName)

	return &ResolveResult{
		Player:   player,
		Resolver: resolverName,
		CacheHit: false,
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

func aggregateResolveErrors(errors []*resolver.ResolverError) *ResolveResult {
	if len(errors) == 0 {
		return noResolversResult()
	}

	aggErr := resolver.AggregateErrors(errors)

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

func (s *PlayerService) cachePlayer(player *model.Player, resolverName string) {
	cacheData := &cache.PlayerData{
		UUID:     player.UUID,
		Username: player.Username,
	}

	s.cache.SetPlayerCache(s.cachePrefix, cacheData, resolverName, s.cacheTTL)
}
