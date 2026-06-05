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
	Resolver  string // Which resolver provided this data
}