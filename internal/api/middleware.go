package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
)

// LoggingMiddleware logs HTTP requests
func LoggingMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Wrap response writer to capture status code
			wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

			next.ServeHTTP(wrapped, r)

			logger.Info("HTTP request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", wrapped.statusCode,
				"duration", time.Since(start),
			)
		})
	}
}

// RecoveryMiddleware recovers from panics and returns 500
func RecoveryMiddleware(logger *slog.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					logger.Error("PANIC", "error", err)

					w.WriteHeader(http.StatusInternalServerError)

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(ErrorResponse{
						BaseResponse: BaseResponse{
							Success: false,
						},
						Error: ErrorResult{
							Code:    constant.CodeInternalServerError,
							Message: http.StatusText(http.StatusInternalServerError),
						},
					})
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}