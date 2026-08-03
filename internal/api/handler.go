package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
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

	result := h.service.Resolve(r.Context(), identifier)

	if result.Error != nil {
		h.writeError(w, result.Error)
		return
	}

	cacheControl := fmt.Sprintf("public, max-age=%d", int64(result.CacheTTL.Seconds()))
	cacheStatus := "MISS"
	if result.CacheHit {
		cacheStatus = "HIT"
	}

	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("X-Cache-Status", cacheStatus)
	w.Header().Set("X-Resolver", result.Resolver)

	// Indicate if this request was coalesced with another in-flight request
	if result.Coalesced {
		w.Header().Set("X-Coalesced", "true")
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
	json.NewEncoder(w).Encode(data)
}

func (h *PlayerHandler) writeError(w http.ResponseWriter, err error) {
	errResp := ErrorResponse{
		BaseResponse: BaseResponse{
			Success: false,
		},
	}

	if resErr, ok := err.(*resolver.ResolverError); ok {
		errResp.Error = ErrorResult{
			Code:    resErr.Code,
			Message: resErr.Message,
		}

		// Add resolver details if available
		if len(resErr.Details) > 0 {
			resolversTried := make([]string, 0, len(resErr.Details))
			for name := range resErr.Details {
				resolversTried = append(resolversTried, name)
			}

			errResp.Error.Details = &ErrorDetail{
				Resolver: resErr.Details,
			}
		}

		h.writeJSON(w, resErr.StatusCode, errResp)
		return
	}

	// Generic error
	errResp.Error = ErrorResult{
		Code:    constant.CodeInternalServerError,
		Message: err.Error(),
	}

	h.writeJSON(w, http.StatusInternalServerError, errResp)
}

func NotFoundHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Warn("endpoint not found",
			"method", r.Method,
			"path", r.URL.Path,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)

		json.NewEncoder(w).Encode(ErrorResponse{
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

func MethodNotAllowedHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger.Warn("method not allowed",
			"method", r.Method,
			"path", r.URL.Path,
		)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)

		json.NewEncoder(w).Encode(ErrorResponse{
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
