package main

import (
	"context"
	"fmt"
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

	if err := run(logger); err != nil {
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// load config
	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		return err
	}
	logger.Info("config loaded", "app_name", cfg.App.Name, "env", cfg.App.Env)

	for _, warning := range cfg.Warnings() {
		logger.Warn("config warning", "message", warning)
	}

	// connect to redis cache
	redisCache, err := cache.NewRedisClient(ctx, cache.RedisConfig(cfg.Redis), logger)
	if err != nil {
		logger.Error("Failed to connect to Redis", "error", err)
		return err
	}
	logger.Info("redis connected")

	defer func() {
		if err := redisCache.Close(); err != nil {
			logger.Warn("redis close failed", "error", err)
		}
	}()

	// register resolvers from config
	resolvers := make([]resolver.Resolver, 0, len(cfg.Resolver.List))
	for _, name := range cfg.Resolver.List {
		res := resolver.GetResolverByName(name, cfg.Resolver.GetResolverTimeout(), cfg.Resolver.UserAgent)
		if res == nil {
			logger.Error("Resolver listed in config is not registered", "resolver", name)
			return fmt.Errorf("unregistered resolver %q", name)
		}
		resolvers = append(resolvers, res)
	}
	logger.Info("Resolvers initialized", "strategy", cfg.Resolver.Strategy, "count", len(resolvers), "names", cfg.Resolver.List)

	playerService := service.NewPlayerService(
		resolvers,
		cfg.Resolver.Strategy,
		cfg.Resolver.NotFoundMinCount,
		redisCache,
		cfg.Cache.Prefix,
		cfg.Cache.GetCacheTTL(),
		cfg.Cache.GetNegativeTTL(),
		logger,
	)

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
		IdleTimeout:  time.Duration(cfg.Api.IdleTimeout) * time.Second,
	}

	serverErr := make(chan error, 1)

	go func() {
		logger.Info("Server listening", "url", fmt.Sprintf("http://localhost:%d", cfg.Api.Port))
		logger.Info("API docs available", "url", fmt.Sprintf("http://localhost:%d/", cfg.Api.Port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// log server error and quit on failure
	select {
	case err := <-serverErr:
		logger.Error("Server error", "error", err)
		return err
	case <-quit:
	}

	logger.Info("Shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// wait for http server shutdown
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server forced to shutdown", "error", err)
		return err
	}

	// wait for all inflight leader resolves to finish
	if err := playerService.WaitInflightResolves(shutdownCtx); err != nil {
		logger.Warn("Shutdown budget expired while resolves were in flight", "error", err)
	}

	logger.Info("Server stopped")
	return nil
}
