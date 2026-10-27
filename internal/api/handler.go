package api

import (
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/service"
)

// PlayerHandler handles HTTP requests related to player resolution.
type PlayerHandler struct {
	service *service.PlayerService
}

func NewPlayerHandler(service *service.PlayerService) *PlayerHandler {
	return &PlayerHandler{
		service: service,
	}
}

func (h *PlayerHandler) GetPlayer(w http.ResponseWriter, r *http.Request) {
	identifier := chi.URLParam(r, "identifier")

	result, resErr := h.service.Resolve(r.Context(), identifier)
	if r.Context().Err() != nil {
		return
	}

	if result != nil {
		if result.CacheHit {
			w.Header().Set("X-Cache-Status", "HIT")
		} else {
			w.Header().Set("X-Cache-Status", "MISS")
		}

		if result.CacheTTL > 0 {
			maxAge := max(int64(result.CacheTTL/time.Second), 0)
			cacheControl := fmt.Sprintf("public, max-age=%d", maxAge)
			w.Header().Set("Cache-Control", cacheControl)
		}

		if result.Resolver != "" {
			w.Header().Set("X-Resolver", result.Resolver)
		}

		if result.Coalesced {
			w.Header().Set("X-Coalesced", "true")
		}
	}

	if resErr != nil {
		h.writeError(w, resErr)
		return
	}

	h.writeJSON(w, http.StatusOK, PlayerResponse{
		BaseResponse: BaseResponse{
			Success: true,
		},
		Data: &PlayerData{
			UUID:     result.Player.UUID,
			Username: result.Player.Username,
		},
	})
}

func (h *PlayerHandler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.MarshalWrite(w, data)
}

func (h *PlayerHandler) writeError(w http.ResponseWriter, resErr *service.Error) {
	errResp := ErrorResponse{
		BaseResponse: BaseResponse{
			Success: false,
		},
		Error: ErrorResult{
			Code:    resErr.Code,
			Message: resErr.Message,
		},
	}

	h.writeJSON(w, resErr.StatusCode, errResp)
}

// DocsHandler serves the embedded Swagger UI and OpenAPI spec.
type DocsHandler struct {
	swaggerHTML []byte
	openapiYAML []byte
}

func NewDocsHandler(swaggerHTML, openapiYAML []byte) *DocsHandler {
	return &DocsHandler{
		swaggerHTML: swaggerHTML,
		openapiYAML: openapiYAML,
	}
}

func (h *DocsHandler) ServeSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(h.swaggerHTML)
}

func (h *DocsHandler) ServeOpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-yaml")
	w.Write(h.openapiYAML)
}

func NotFoundHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Warn("endpoint not found",
			"method", r.Method,
			"path", r.URL.Path,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)

		json.MarshalWrite(w, ErrorResponse{
			BaseResponse: BaseResponse{
				Success: false,
			},
			Error: ErrorResult{
				Code:    constant.CodeEndpointNotFound,
				Message: "Endpoint Not Found",
			},
		})
	}
}

func MethodNotAllowedHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Warn("method not allowed",
			"method", r.Method,
			"path", r.URL.Path,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)

		json.MarshalWrite(w, ErrorResponse{
			BaseResponse: BaseResponse{
				Success: false,
			},
			Error: ErrorResult{
				Code:    constant.CodeMethodNotAllowed,
				Message: http.StatusText(http.StatusMethodNotAllowed),
			},
		})
	}
}
