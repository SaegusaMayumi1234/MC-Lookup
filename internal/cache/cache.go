package cache

import (
	"time"
)

type PlayerData struct {
	UUID     string
	Username string
}

type Entry struct {
	Player    *PlayerData
	CachedAt  time.Time
	ExpiresAt time.Time
	Resolver  string
}

// TTL returns the remaining time-to-live
func (e *Entry) TTL() time.Duration {
	remaining := time.Until(e.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (e *Entry) Age() time.Duration {
	return time.Since(e.CachedAt)
}

// IsExpired checks if the entry has expired
func (e *Entry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// Stats holds cache statistics
type Stats struct {
	Hits       int64
	Misses     int64
	Size       int64
	HitRate    float64
}

// Cache defines the interface for player caching
type Cache interface {
	// Get retrieves a player from cache by key (normalized identifier)
	// Returns the entry and true if found and not expired, nil and false otherwise
	Get(key string) (*Entry, bool)

	// Set stores a player in cache with the given TTL
	Set(key string, p *PlayerData, resolver string, ttl time.Duration)

	// Delete removes an entry from cache
	Delete(key string)

	// Stats returns cache statistics
	Stats() Stats

	// Close closes the cache connection (for Redis)
	Close() error
}
