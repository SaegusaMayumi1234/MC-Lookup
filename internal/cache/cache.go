package cache

import (
	"encoding/json/jsontext"
	"time"
)

type Cache interface {
	// SetCacheBulk sets multiple cache entries with the same data and TTL.
	SetCacheBulk([]string, any, time.Duration) error

	// GetCache retrieves a cache entry by key. It returns the raw cached payload, the time it was cached, the TTL, a boolean indicating if it was found, and an error if any occurred.
	GetCache(key string) (jsontext.Value, time.Time, time.Duration, bool, error)

	// Close closes the cache connection (for Redis)
	Close() error
}
