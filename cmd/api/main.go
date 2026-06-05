package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/saegusamayumi1234/mc-lookup/internal/cache"
	"github.com/saegusamayumi1234/mc-lookup/internal/config"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		// AddSource: true,
	}))

	slog.SetDefault(logger)

	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		return
	}

	cache, err := cache.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		logger.Error("Failed to connect to Redis", "error", err)
		return
	}
	
	logger.Info("redis connected", "redis", cache)
}