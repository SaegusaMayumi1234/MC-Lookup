package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/saegusamayumi1234/mc-lookup/internal/service"
)

type RouterDeps struct {
    Services Services
	Logger   *slog.Logger
    Env      string
}

type Services struct {
    // Health  service.HealthService
    Players *service.PlayerService
}

func NewRouter(d RouterDeps) *chi.Mux {
	r := chi.NewRouter()

	// Global middleware
	r.Use(LoggingMiddleware(d.Logger))
	r.Use(RecoveryMiddleware(d.Logger))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		MaxAge:           300,
	}))
	// r.Use(middleware.RealIP)
	r.Use(middleware.RequestID)

	// Handlers
	// docsHandler := NewDocsHandler(openapiPath)
	playerHandler := NewPlayerHandler(d.Services.Players)

	// // Documentation routes
	// r.Get("/", docsHandler.ServeSwaggerUI)
	// r.Get("/api/openapi.yaml", docsHandler.ServeOpenAPI)

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/player", func(r chi.Router) {
			r.Get("/{identifier}", playerHandler.GetPlayer)
		})
	})

	return r
}