package service

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/saegusamayumi1234/mc-lookup/internal/cache"
	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
)

type ResolveResult struct {
	Player      *model.Player
	Resolver    string
	CacheHit    bool
	CacheTTL    time.Duration
	CacheAge    time.Duration
	Coalesced   bool
	Error       error
}

type PlayerService struct {
	resolvers []resolver.Resolver
	cache     cache.Cache
	cacheTTL  time.Duration
	sfGroup   singleflight.Group
}

func NewPlayerService(resolvers []resolver.Resolver, c cache.Cache, cacheTTL time.Duration) *PlayerService {
	return &PlayerService{
		resolvers: resolvers,
		cache:     c,
		cacheTTL:  cacheTTL,
	}
}

// Resolve looks up a player by username or UUID using concurrent resolvers, caching and singleflight to prevent duplicate work. 
// It returns the first successful result or an aggregated error if all fail.
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

// doResolve performs the actual resolution against external APIs
func (s *PlayerService) doResolve(ctx context.Context, identifier string) *ResolveResult {
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

			res.player.UUID = model.NormalizeUUID(res.player.UUID)

			s.cachePlayer(res.player, res.resolver)

			return &ResolveResult{
				Player:   res.player,
				Resolver: res.resolver,
				CacheHit: false,
			}
		}

		if res.err != nil {
			errors = append(errors, res.err)
		}
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

func buildErrorDetails(errors []*resolver.ResolverError) map[string]string {
	details := make(map[string]string)
	for _, err := range errors {
		details[err.Resolver] = err.Message
	}
	return details
}

func (s *PlayerService) getCachedResult(key string) *ResolveResult {
	entry, found := s.cache.Get(key)
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

	s.cache.Set(model.NormalizeIdentifier(player.UUID), cacheData, resolverName, s.cacheTTL)
	s.cache.Set(model.NormalizeIdentifier(player.Username), cacheData, resolverName, s.cacheTTL)
}

func (s *PlayerService) GetCacheStats() cache.Stats {
	return s.cache.Stats()
}
