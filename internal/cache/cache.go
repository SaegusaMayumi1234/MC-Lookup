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

// Cache defines the interface for player caching
type Cache interface {
	// Set stores a player in cache with the given TTL
	SetPlayerCache(prefix string, p *PlayerData, resolver string, ttl time.Duration)

	// Get retrieves a player from cache by key (normalized identifier)
	GetPlayerCache(prefix string, identifier string) (*Entry, bool)

	// Close closes the cache connection (for Redis)
	Close() error
}
