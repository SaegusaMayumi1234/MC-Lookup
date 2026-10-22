package cache

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
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
	Data     jsontext.Value `json:"data,omitempty"`
	CachedAt int64          `json:"cached_at"`
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

func (rc *RedisClient) SetCacheBulk(keys []string, data any, ttl time.Duration) error {
	if len(keys) == 0 {
		return nil
	}

	// Marshal the data into JSON for storage in Redis
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}

	re := redisEntry{
		Data:     jsontext.Value(payload),
		CachedAt: time.Now().Unix(),
	}

	value, err := json.Marshal(re)
	if err != nil {
		return err
	}

	ctx := context.Background()
	_, err = rc.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, key := range keys {
			pipe.Set(ctx, key, value, ttl)
		}
		return nil
	})

	return err
}

func (rc *RedisClient) GetCache(key string) (data jsontext.Value, cachedAt time.Time, ttl time.Duration, found bool, err error) {
	var getCmd *redis.StringCmd
	var ttlCmd *redis.DurationCmd

	ctx := context.Background()
	_, err = rc.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		getCmd = pipe.Get(ctx, key)
		ttlCmd = pipe.TTL(ctx, key)
		return nil
	})

	if err != nil && !errors.Is(err, redis.Nil) {
		return data, time.Time{}, 0, false, err
	}

	raw, getErr := getCmd.Bytes()
	if errors.Is(getErr, redis.Nil) {
		return data, time.Time{}, 0, false, nil
	}
	if getErr != nil {
		return data, time.Time{}, 0, false, getErr
	}

	ttl, ttlErr := ttlCmd.Result()
	if ttlErr != nil {
		return data, time.Time{}, 0, false, ttlErr
	}

	if ttl < 0 {
		// Redis reports -1 for keys with no expiry and -2 for missing keys.
		// Never serve legacy entries that were accidentally stored persistently.
		if ttl == -1 {
			_ = rc.client.Del(ctx, key).Err()
		}
		return nil, time.Time{}, 0, false, nil
	}

	var entry redisEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, time.Time{}, 0, false, err
	}

	return entry.Data, time.Unix(entry.CachedAt, 0), ttl, true, nil
}

// Close closes the Redis connection.
func (rc *RedisClient) Close() error {
	return rc.client.Close()
}
