package api

import (
	"context"
	"encoding/json/jsontext"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
	"github.com/saegusamayumi1234/mc-lookup/internal/model"
	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
	"github.com/saegusamayumi1234/mc-lookup/internal/service"
)

// writeError must pass the service's typed message through to the response body.
func TestWriteErrorPassesServiceErrorMessage(t *testing.T) {
	h := NewPlayerHandler(nil) // service is not touched by writeError

	rec := httptest.NewRecorder()
	h.writeError(rec, &service.Error{
		Code:       "TEST_CODE",
		Message:    "typed message",
		StatusCode: http.StatusBadGateway,
	})

	body := rec.Body.String()
	if !strings.Contains(body, "typed message") {
		t.Fatalf("expected typed message in response body, got: %s", body)
	}
	if rec.Code != http.StatusBadGateway {
		t.Errorf("expected status %d, got %d", http.StatusBadGateway, rec.Code)
	}
}

// nfResolver always reports NOT_FOUND.
type nfResolver struct{ name string }

func (f nfResolver) Resolve(context.Context, string) (*model.Player, error) {
	return nil, resolver.NewNotFoundError(f.name, "Notch")
}

func (f nfResolver) Name() string { return f.name }

// rlResolver always reports RATE_LIMITED.
type rlResolver struct{ name string }

func (r rlResolver) Resolve(context.Context, string) (*model.Player, error) {
	return nil, resolver.NewRateLimitError(r.name, nil)
}

func (r rlResolver) Name() string { return r.name }

// nopRedis is a no-op cache.Cache for handler-level service construction.
type nopRedis struct{}

func (nopRedis) SetCacheBulk(context.Context, []string, any, time.Duration) error {
	return nil
}

func (nopRedis) GetCache(context.Context, string) (jsontext.Value, time.Time, time.Duration, bool, error) {
	return nil, time.Time{}, 0, false, nil
}

func (nopRedis) Close() error { return nil }

// A not-found verdict must carry the comma-joined reporters in the X-Resolver
// header, while the body stays free of per-resolver details.
func TestGetPlayerNotFoundMovesReportersIntoResolverHeader(t *testing.T) {
	svc := service.NewPlayerService(
		[]resolver.Resolver{nfResolver{name: "mojang"}, nfResolver{name: "playerdb"}},
		constant.StrategyFallback,
		2,
		nopRedis{},
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	r := chi.NewRouter()
	r.Get("/api/v1/player/{identifier}", NewPlayerHandler(svc).GetPlayer)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/player/Notch", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
	if got := rec.Header().Get("X-Resolver"); got != "mojang,playerdb" {
		t.Errorf("expected X-Resolver %q, got %q", "mojang,playerdb", got)
	}

	body := rec.Body.String()
	if strings.Contains(body, "details") || strings.Contains(body, "mojang") {
		t.Errorf("expected no per-resolver details in the body, got: %s", body)
	}
	if !strings.Contains(body, `"message":"Player not found"`) {
		t.Errorf("expected the generic not-found message in the body, got: %s", body)
	}
}

// Non-not-found errors keep the X-Resolver header empty and carry no details.
func TestGetPlayerRateLimitedOmitsResolverHeader(t *testing.T) {
	svc := service.NewPlayerService(
		[]resolver.Resolver{rlResolver{name: "mojang"}, rlResolver{name: "playerdb"}},
		constant.StrategyFallback,
		2,
		nopRedis{},
		"test:",
		time.Minute,
		time.Minute,
		nil,
	)

	r := chi.NewRouter()
	r.Get("/api/v1/player/{identifier}", NewPlayerHandler(svc).GetPlayer)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/player/Notch", nil))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status %d, got %d", http.StatusTooManyRequests, rec.Code)
	}
	if got := rec.Header().Get("X-Resolver"); got != "" {
		t.Errorf("expected no X-Resolver header on a rate-limited verdict, got %q", got)
	}
	if strings.Contains(rec.Body.String(), "details") {
		t.Errorf("expected no details in the body, got: %s", rec.Body.String())
	}
}
