package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
