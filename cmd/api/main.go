package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/api"
	"github.com/saegusamayumi1234/mc-lookup/internal/cache"
	"github.com/saegusamayumi1234/mc-lookup/internal/config"
	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
	"github.com/saegusamayumi1234/mc-lookup/internal/service"
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
	logger.Info("config loaded", "app_name", cfg.App.Name, "env", cfg.App.Env)

	cache, err := cache.NewRedisClient(ctx, cache.RedisConfig(cfg.Redis))
	if err != nil {
		logger.Error("Failed to connect to Redis", "error", err)
		return
	}
	logger.Info("redis connected")

	resolvers := make([]resolver.Resolver, 0, len(cfg.Resolver.List))
	for _, name := range cfg.Resolver.List {
		res := resolver.GetResolverByName(name, cfg.Resolver.GetResolverTimeout(), cfg.Resolver.UserAgent)
		if res == nil {
			logger.Error("Resolver listed in config is not registered", "resolver", name)
			return
		}
		resolvers = append(resolvers, res)
	}
	logger.Info("Resolvers initialized", "strategy", cfg.Resolver.Strategy, "count", len(resolvers), "names", cfg.Resolver.List)

	playerService := service.NewPlayerService(resolvers, cfg.Resolver.Strategy, cache, cfg.Cache.GetCacheTTL(), cfg.Cache.Prefix)

	router := api.NewRouter(api.RouterDeps{
		Services: api.Services{
			Players: playerService,
		},
		Logger: logger,
		Env:    cfg.App.Env,
	})

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Api.Port),
		Handler:      router,
		ReadTimeout:  time.Duration(cfg.Api.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(cfg.Api.WriteTimeout) * time.Second,
	}

	go func() {
		logger.Info("Server listening", "url", fmt.Sprintf("http://localhost:%d", cfg.Api.Port))
		logger.Info("API docs available", "url", fmt.Sprintf("http://localhost:%d/", cfg.Api.Port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")

	// Graceful shutdown with 30 second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	log.Println("Server stopped")
}
