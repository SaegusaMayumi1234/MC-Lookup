package cache

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type RedisClient struct {
	client *redis.Client
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

// Close closes the Redis connection.
func (rc *RedisClient) CloseRedisClient() error {
	return rc.client.Close()
}
