package cache

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
)

type RedisConfig struct {
	Addr       string
	Password   string
	DB         int
	MaxRetries int
}

type RedisClient struct {
	client *redis.Client
	logger *slog.Logger
}

// redisEntry is the serialized form of a cache entry
type redisEntry struct {
	Player   *PlayerData `json:"player"`
	CachedAt int64       `json:"cached_at"`
	Resolver string      `json:"resolver"`
}

// NewRedisClient creates a new Redis client and verifies the connection.
func NewRedisClient(ctx context.Context, cfg RedisConfig, logger *slog.Logger) (*RedisClient, error) {
	if logger == nil {
		logger = slog.Default()
	}

	client := redis.NewClient(&redis.Options{
		Addr:       cfg.Addr,
		Password:   cfg.Password,
		DB:         cfg.DB,
		MaxRetries: cfg.MaxRetries,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}

	return &RedisClient{client: client, logger: logger}, nil
}

func (rc *RedisClient) SetPlayerCache(prefix string, p *PlayerData, resolver string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}

	ctx := context.Background()
	usernameKey := prefix + model.NormalizeIdentifier(p.Username)
	uuidKey := prefix + model.NormalizeIdentifier(p.UUID)

	re := redisEntry{
		Player:   p,
		CachedAt: time.Now().Unix(),
		Resolver: resolver,
	}

	data, err := json.Marshal(re)
	if err != nil {
		rc.logger.Warn("redis cache marshal failed", "error", err)
		return
	}

	pipe := rc.client.Pipeline()
	pipe.Set(ctx, usernameKey, data, ttl)
	pipe.Set(ctx, uuidKey, data, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		rc.logger.Warn("redis cache write failed", "error", err)
	}
}

func (rc *RedisClient) GetPlayerCache(prefix string, identifier string) (*Entry, bool) {
	ctx := context.Background()
	key := prefix + model.NormalizeIdentifier(identifier)

	pipe := rc.client.Pipeline()
	getCmd := pipe.Get(ctx, key)
	ttlCmd := pipe.TTL(ctx, key)
	_, err := pipe.Exec(ctx)

	// redis.Nil means the key is missing: a normal cache miss, not a failure.
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			rc.logger.Warn("redis cache read failed", "error", err)
		}
		return nil, false
	}

	data, err := getCmd.Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			rc.logger.Warn("redis cache read failed", "error", err)
		}
		return nil, false
	}

	ttl := ttlCmd.Val()
	if ttl < 0 {
		// Redis reports -1 for keys with no expiry and -2 for missing keys.
		// Never serve legacy entries that were accidentally stored persistently.
		if ttl == -1 {
			_ = rc.client.Del(ctx, key).Err()
		}
		return nil, false
	}

	var re redisEntry
	if err := json.Unmarshal(data, &re); err != nil {
		rc.logger.Warn("redis cache decode failed", "key", key, "error", err)
		return nil, false
	}

	entry := &Entry{
		Player:    re.Player,
		CachedAt:  time.Unix(re.CachedAt, 0),
		ExpiresAt: time.Now().Add(ttl),
		Resolver:  re.Resolver,
	}

	return entry, true
}

func (rc *RedisClient) SetNotFoundCache(prefix, identifier string, ttl time.Duration) {
	if ttl <= 0 {
		return
	}

	ctx := context.Background()
	key := prefix + model.NormalizeIdentifier(identifier)

	if err := rc.client.Set(ctx, key, "true", ttl).Err(); err != nil {
		rc.logger.Warn("redis cache write failed", "error", err)
	}
}

func (rc *RedisClient) GetNotFoundCache(prefix, identifier string) (time.Duration, bool) {
	ctx := context.Background()
	key := prefix + model.NormalizeIdentifier(identifier)

	ttl, err := rc.client.TTL(ctx, key).Result()
	if err != nil {
		rc.logger.Warn("redis cache read failed", "error", err)
		return 0, false
	}

	if ttl < 0 {
		// Redis reports -1 for keys with no expiry and -2 for missing keys.
		// Never serve legacy entries that were accidentally stored persistently.
		if ttl == -1 {
			_ = rc.client.Del(ctx, key).Err()
		}
		return 0, false
	}

	return ttl, true
}

// Close closes the Redis connection.
func (rc *RedisClient) Close() error {
	return rc.client.Close()
}
