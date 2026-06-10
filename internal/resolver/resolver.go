package resolver

import (
	"context"
	"net/http"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
)

type Resolver interface {
	Resolve(ctx context.Context, identifier string) (*model.Player, error)
	Name() string
}

func GetResolverByName(name string, timeout time.Duration, userAgent string) Resolver {
	switch name {
	case constant.ResolverNameMojang:
		return NewMojangResolver(timeout, userAgent)
	case constant.ResolverNamePlayerDB:
		return NewPlayerDBResolver(timeout, userAgent)
	case constant.ResolverNameAshcon:
		return NewAshconResolver(timeout, userAgent)
	case constant.ResolverNameMowojang:
		return NewMowojangResolver(timeout, userAgent)
	default:
		return nil
	}
}

// BaseResolver provides common functionality for all resolvers
type BaseResolver struct {
	name      string
	client    *http.Client
	userAgent string
}

// NewBaseResolver creates a new base resolver with configured HTTP client
func NewBaseResolver(name string, timeout time.Duration, userAgent string) BaseResolver {
	return BaseResolver{
		name:      name,
		userAgent: userAgent,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Name returns the resolver name
func (b *BaseResolver) Name() string {
	return b.name
}

// Client returns the HTTP client
func (b *BaseResolver) Client() *http.Client {
	return b.client
}

// UserAgent returns the user agent string
func (b *BaseResolver) UserAgent() string {
	return b.userAgent
}
