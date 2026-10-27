package resolver

import (
	"net/http"
	"testing"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
)

// Reaching the NOT_FOUND quorum is conclusive even when other resolvers failed
// differently, and the message stays generic: no resolver counts.
func TestAggregateErrorsNotFoundQuorumUsesGenericMessage(t *testing.T) {
	errs := []*ResolverError{
		NewNotFoundError("mojang", "Notch"),
		NewRateLimitError("playerdb", nil),
		NewNotFoundError("ashcon", "Notch"),
	}

	agg := AggregateErrors(errs, 2)

	if agg.FinalCode != constant.CodeNotFound {
		t.Errorf("expected code %q, got %q", constant.CodeNotFound, agg.FinalCode)
	}
	if agg.FinalStatus != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, agg.FinalStatus)
	}
	if agg.FinalMessage != "Player not found" {
		t.Errorf("expected the generic not-found message, got %q", agg.FinalMessage)
	}
}

// Below the quorum, a NOT_FOUND minority must not decide: rate limiting still
// takes the final verdict.
func TestAggregateErrorsBelowQuorumKeepsRateLimited(t *testing.T) {
	errs := []*ResolverError{
		NewNotFoundError("mojang", "Notch"),
		NewRateLimitError("playerdb", nil),
		NewRateLimitError("ashcon", nil),
	}

	agg := AggregateErrors(errs, 2)

	if agg.FinalCode != constant.CodeRateLimited {
		t.Errorf("expected code %q, got %q", constant.CodeRateLimited, agg.FinalCode)
	}
	if agg.FinalStatus != http.StatusTooManyRequests {
		t.Errorf("expected status %d, got %d", http.StatusTooManyRequests, agg.FinalStatus)
	}
}
