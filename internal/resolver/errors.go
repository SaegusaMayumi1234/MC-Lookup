package resolver

import (
	"fmt"
	"net/http"

	"github.com/saegusamayumi1234/mc-lookup/internal/constant"
)

// ResolverError represents a typed error from a resolver
type ResolverError struct {
	Code       string
	Message    string
	StatusCode int
	Resolver   string
	Cause      error
	Details    map[string]string
}

// Error implements the error interface
func (e *ResolverError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %s (cause: %v)", e.Resolver, e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Resolver, e.Code, e.Message)
}

// Unwrap returns the underlying error
func (e *ResolverError) Unwrap() error {
	return e.Cause
}

// NewNotFoundError creates a not found error
func NewNotFoundError(resolver string, identifier string) *ResolverError {
	return &ResolverError{
		Code:       constant.CodeNotFound,
		Message:    fmt.Sprintf("Player '%s' not found", identifier),
		StatusCode: http.StatusNotFound,
		Resolver:   resolver,
	}
}

// NewRateLimitError creates a rate limit error
func NewRateLimitError(resolver string, cause error) *ResolverError {
	return &ResolverError{
		Code:       constant.CodeRateLimited,
		Message:    "Rate limit exceeded",
		StatusCode: http.StatusTooManyRequests,
		Resolver:   resolver,
		Cause:      cause,
	}
}

func NewUpstreamError(resolver string, statusCode int, cause error) *ResolverError {
	message := fmt.Sprintf("Upstream API returned status %d", statusCode)
	if statusCode <= 0 {
		message = "Upstream request failed"
	}
	return &ResolverError{
		Code:       constant.CodeUpstreamError,
		Message:    message,
		StatusCode: http.StatusBadGateway,
		Resolver:   resolver,
		Cause:      cause,
	}
}

// NewTimeoutError creates a timeout error
func NewTimeoutError(resolver string, cause error) *ResolverError {
	return &ResolverError{
		Code:       constant.CodeTimeout,
		Message:    "Request timed out",
		StatusCode: http.StatusGatewayTimeout,
		Resolver:   resolver,
		Cause:      cause,
	}
}

// NewInvalidInputError creates an invalid input error
func NewInvalidInputError(resolver, identifier string) *ResolverError {
	return &ResolverError{
		Code:       constant.CodeInvalidInput,
		Message:    fmt.Sprintf("Invalid identifier '%s': must be a valid Minecraft username (3-16 chars) or UUID", identifier),
		StatusCode: http.StatusBadRequest,
		Resolver:   resolver,
	}
}

// MapHTTPStatusToError maps HTTP status codes to appropriate resolver errors
func MapHTTPStatusToError(resolver string, statusCode int, identifier string) *ResolverError {
	switch statusCode {
	case http.StatusNotFound, http.StatusNoContent:
		return NewNotFoundError(resolver, identifier)
	case http.StatusTooManyRequests:
		return NewRateLimitError(resolver, nil)
	case http.StatusBadRequest:
		return NewInvalidInputError(resolver, identifier)
	default:
		if statusCode >= 500 {
			return NewUpstreamError(resolver, statusCode, nil)
		}
		return NewUpstreamError(resolver, statusCode, fmt.Errorf("unexpected status: %d", statusCode))
	}
}

type AggregatedError struct {
	FinalCode    string
	FinalStatus  int
	FinalMessage string
}

func (e *AggregatedError) Error() string {
	return e.FinalMessage
}

// AggregateErrors analyzes errors from all resolvers and returns a unified error.
// The NOT_FOUND quorum is checked before RATE_LIMITED, TIMEOUT, and UPSTREAM_ERROR.
func AggregateErrors(errs []*ResolverError, notFoundMinCount int) *AggregatedError {
	if len(errs) == 0 {
		return &AggregatedError{
			FinalCode:    constant.CodeNoResolversAvailable,
			FinalStatus:  http.StatusInternalServerError,
			FinalMessage: "No resolvers available",
		}
	}

	codeCounts := make(map[string]int)

	for _, err := range errs {
		codeCounts[err.Code]++
	}

	agg := &AggregatedError{}

	// A configured NOT_FOUND quorum takes precedence over transient resolver errors.
	// Unanimous NOT_FOUND remains conclusive even if the threshold is higher.
	notFoundCount := codeCounts[constant.CodeNotFound]
	if notFoundCount == len(errs) || (notFoundMinCount > 0 && notFoundCount >= notFoundMinCount) {
		agg.FinalCode = constant.CodeNotFound
		agg.FinalStatus = http.StatusNotFound
		agg.FinalMessage = "Player not found"
		return agg
	}

	// Check for rate limiting
	if codeCounts[constant.CodeRateLimited] > 0 {
		agg.FinalCode = constant.CodeRateLimited
		agg.FinalStatus = http.StatusTooManyRequests
		agg.FinalMessage = resolverFailureSummary("Rate limiting", codeCounts[constant.CodeRateLimited], len(errs))
		return agg
	}

	// Check for timeouts
	if codeCounts[constant.CodeTimeout] > 0 {
		agg.FinalCode = constant.CodeTimeout
		agg.FinalStatus = http.StatusGatewayTimeout
		agg.FinalMessage = resolverFailureSummary("Timeouts", codeCounts[constant.CodeTimeout], len(errs))
		return agg
	}

	// Default to upstream error
	agg.FinalCode = constant.CodeUpstreamError
	agg.FinalStatus = http.StatusBadGateway
	agg.FinalMessage = "All resolver attempts failed"
	return agg
}

func resolverFailureSummary(failure string, count, total int) string {
	if count == total {
		return fmt.Sprintf("%s affected all %d resolvers", failure, total)
	}
	return fmt.Sprintf("%s affected %d of %d resolvers", failure, count, total)
}
