package api

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/saegusamayumi1234/mc-lookup/docs"
	"github.com/saegusamayumi1234/mc-lookup/internal/service"
)

type RouterDeps struct {
	Services Services
	Logger   *slog.Logger
	Env      string
}

type Services struct {
	Players *service.PlayerService
}

func NewRouter(d RouterDeps) *chi.Mux {
	r := chi.NewRouter()

	r.Use(LoggingMiddleware(d.Logger))
	r.Use(RecoveryMiddleware(d.Logger))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{"*"},
		AllowedMethods: []string{"GET", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Content-Type"},
		ExposedHeaders: []string{"X-Cache-Status", "X-Resolver", "X-Coalesced"},
		MaxAge:         300,
	}))
	r.Use(middleware.RequestID)

	r.NotFound(NotFoundHandler(d.Logger))
	r.MethodNotAllowed(MethodNotAllowedHandler(d.Logger))

	playerHandler := NewPlayerHandler(d.Services.Players)

	if d.Env == "dev" {
		docsHandler := NewDocsHandler(docs.SwaggerHTML, docs.OpenAPIYAML)
		r.Get("/", docsHandler.ServeSwaggerUI)
		r.Get("/api/openapi.yaml", docsHandler.ServeOpenAPI)
	}

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/player/{identifier}", playerHandler.GetPlayer)
	})

	return r
}
