package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/saegusamayumi1234/mc-lookup/internal/resolver"
)

// TestWriteErrorGenericBranchHidesRawError guards the generic error branch:
// raw error text must never reach the response body — only the fixed message.
func TestWriteErrorGenericBranchHidesRawError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewPlayerHandler(logger, nil) // service is not touched by writeError

	rawErr := errors.New("secret internal detail: redis vps3:6379 dial failed")

	rec := httptest.NewRecorder()
	h.writeError(rec, rawErr)

	body := rec.Body.String()
	if strings.Contains(body, "secret internal detail") {
		t.Fatalf("raw error text leaked to the response body: %s", body)
	}
	if !strings.Contains(body, "Internal server error") {
		t.Fatalf("expected fixed message in response body, got: %s", body)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected status %d, got %d", http.StatusInternalServerError, rec.Code)
	}
}

// ResolverError results must keep passing their typed message through.
func TestWriteErrorPassesResolverErrorMessage(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewPlayerHandler(logger, nil)

	rec := httptest.NewRecorder()
	h.writeError(rec, &resolver.ResolverError{
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
