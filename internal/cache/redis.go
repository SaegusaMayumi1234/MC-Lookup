package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type RedisClient struct {
	client *redis.Client
	prefix string
	hits   int64
	misses int64
}

// redisEntry is the serialized form of a cache entry
type redisEntry struct {
	Player   *PlayerData `json:"player"`
	CachedAt int64       `json:"cached_at"`
	Resolver string      `json:"resolver"`
}


// New creates a new Redis client and verifies the connection.
func NewRedisClient(ctx context.Context, cfg RedisConfig) (*RedisClient, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return &RedisClient{client: client}, nil
}

func (rc *RedisClient) Get(key string) (*Entry, bool) {
	ctx := context.Background()
	fullKey := rc.prefix + key

	// Get value and TTL in pipeline
	pipe := rc.client.Pipeline()
	getCmd := pipe.Get(ctx, fullKey)
	ttlCmd := pipe.TTL(ctx, fullKey)
	_, err := pipe.Exec(ctx)

	if err != nil {
		atomic.AddInt64(&rc.misses, 1)
		return nil, false
	}

	data, err := getCmd.Bytes()
	if err != nil {
		atomic.AddInt64(&rc.misses, 1)
		return nil, false
	}

	var re redisEntry
	if err := json.Unmarshal(data, &re); err != nil {
		atomic.AddInt64(&rc.misses, 1)
		return nil, false
	}

	ttl := ttlCmd.Val()
	entry := &Entry{
		Player:    re.Player,
		CachedAt:  time.Unix(re.CachedAt, 0),
		ExpiresAt: time.Now().Add(ttl),
		Resolver:  re.Resolver,
	}

	atomic.AddInt64(&rc.hits, 1)
	return entry, true
}

// Set stores a player in Redis cache
func (rc *RedisClient) Set(key string, p *PlayerData, resolver string, ttl time.Duration) {
	ctx := context.Background()
	fullKey := rc.prefix + key

	re := redisEntry{
		Player:   p,
		CachedAt: time.Now().Unix(),
		Resolver: resolver,
	}

	data, err := json.Marshal(re)
	if err != nil {
		return
	}

	rc.client.Set(ctx, fullKey, data, ttl)
}

// Delete removes an entry from Redis cache
func (rc *RedisClient) Delete(key string) {
	ctx := context.Background()
	fullKey := rc.prefix + key
	rc.client.Del(ctx, fullKey)
}

// Stats returns cache statistics
func (rc *RedisClient) Stats() Stats {
	ctx := context.Background()
	
	// Get approximate key count
	var size int64
	iter := rc.client.Scan(ctx, 0, rc.prefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		size++
	}

	hits := atomic.LoadInt64(&rc.hits)
	misses := atomic.LoadInt64(&rc.misses)
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return Stats{
		Hits:    hits,
		Misses:  misses,
		Size:    size,
		HitRate: hitRate,
	}
}

// Close closes the Redis connection.
func (rc *RedisClient) Close() error {
	return rc.client.Close()
}
