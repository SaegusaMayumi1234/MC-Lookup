package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
	"github.com/saegusamayumi1234/mc-lookup/internal/service"
)

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

	// Set cache headers
	if result.CacheHit {
		w.Header().Set("X-Cache-Status", "HIT")
		w.Header().Set("X-Cache-TTL", strconv.FormatInt(int64(result.CacheTTL.Seconds()), 10))
	} else {
		w.Header().Set("X-Cache-Status", "MISS")
		w.Header().Set("X-Resolver", result.Resolver)
	}

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
				Resolver:   resErr.Details,
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
