package resolver

import (
	"context"
	"net/http"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/model"
)

type Resolver interface {
	Resolve(ctx context.Context, identifier string) (*model.Player, error)
	Name() string
}

// GetResolvers returns all registered resolvers
func GetResolvers(timeout time.Duration, userAgent string) []Resolver {
	return []Resolver{
		NewMojangResolver(timeout, userAgent),
		NewPlayerDBResolver(timeout, userAgent),
		NewAshconResolver(timeout, userAgent),
		NewMowojangResolver(timeout, userAgent),
	}
}

// ResolverNames returns the names of all registered resolvers
func ResolverNames(timeout time.Duration, userAgent string) []string {
	resolvers := GetResolvers(timeout, userAgent)
	names := make([]string, len(resolvers))
	for i, r := range resolvers {
		names[i] = r.Name()
	}
	return names
}

// BaseResolver provides common functionality for all resolvers
type BaseResolver struct {
	name       string
	client     *http.Client
	userAgent  string
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
