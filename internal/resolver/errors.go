package resolver

import (
	"errors"
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

// Is checks if target error matches this error's code
func (e *ResolverError) Is(target error) bool {
	var t *ResolverError
	if errors.As(target, &t) {
		return e.Code == t.Code
	}
	return false
}

// Sentinel errors for comparison
var (
	ErrNotFound = &ResolverError{
		Code:       constant.CodeNotFound,
		StatusCode: http.StatusNotFound,
	}

	ErrRateLimited = &ResolverError{
		Code:       constant.CodeRateLimited,
		StatusCode: http.StatusTooManyRequests,
	}

	ErrUpstreamUnavailable = &ResolverError{
		Code:       constant.CodeUpstreamError,
		StatusCode: http.StatusBadGateway,
	}

	ErrTimeout = &ResolverError{
		Code:       constant.CodeTimeout,
		StatusCode: http.StatusGatewayTimeout,
	}

	ErrInvalidInput = &ResolverError{
		Code:       constant.CodeInvalidInput,
		StatusCode: http.StatusBadRequest,
	}
)

// NewNotFoundError creates a not found error
func NewNotFoundError(resolver, identifier string) *ResolverError {
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

// NewUpstreamError creates an upstream error
func NewUpstreamError(resolver string, statusCode int, cause error) *ResolverError {
	return &ResolverError{
		Code:       constant.CodeUpstreamError,
		Message:    fmt.Sprintf("Upstream API returned status %d", statusCode),
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
func NewInvalidInputError(identifier string) *ResolverError {
	return &ResolverError{
		Code:       constant.CodeInvalidInput,
		Message:    fmt.Sprintf("Invalid identifier '%s': must be a valid Minecraft username (3-16 chars) or UUID", identifier),
		StatusCode: http.StatusBadRequest,
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
		return NewInvalidInputError(identifier)
	default:
		if statusCode >= 500 {
			return NewUpstreamError(resolver, statusCode, nil)
		}
		return NewUpstreamError(resolver, statusCode, fmt.Errorf("unexpected status: %d", statusCode))
	}
}

type AggregatedError struct {
	Errors         []*ResolverError
	FinalCode      string
	FinalStatus    int
	FinalMessage   string
}

func (e *AggregatedError) Error() string {
	return e.FinalMessage
}

// AggregateErrors analyzes errors from all resolvers and returns a unified error
// Priority: NOT_FOUND (if all agree) > RATE_LIMITED > TIMEOUT > UPSTREAM_ERROR
func AggregateErrors(errs []*ResolverError) *AggregatedError {
	if len(errs) == 0 {
		return &AggregatedError{
			FinalCode:    constant.CodeNoResolversAvailable,
			FinalStatus:  http.StatusInternalServerError,
			FinalMessage: "No resolvers available",
		}
	}

	resolversTried := make([]string, 0, len(errs))
	codeCounts := make(map[string]int)

	for _, err := range errs {
		resolversTried = append(resolversTried, err.Resolver)
		codeCounts[err.Code]++
	}

	agg := &AggregatedError{
		Errors:         errs,
	}

	// If ALL resolvers returned NOT_FOUND, the player doesn't exist
	if codeCounts[constant.CodeNotFound] == len(errs) {
		agg.FinalCode = constant.CodeNotFound
		agg.FinalStatus = http.StatusNotFound
		agg.FinalMessage = "Player does not exist"
		return agg
	}

	// Check for rate limiting
	if codeCounts[constant.CodeRateLimited] > 0 {
		agg.FinalCode = constant.CodeRateLimited
		agg.FinalStatus = http.StatusTooManyRequests
		agg.FinalMessage = "All resolvers are rate limited"
		return agg
	}

	// Check for timeouts
	if codeCounts[constant.CodeTimeout] > 0 {
		agg.FinalCode = constant.CodeTimeout
		agg.FinalStatus = http.StatusGatewayTimeout
		agg.FinalMessage = "All resolvers timed out"
		return agg
	}

	// Default to upstream error
	agg.FinalCode = constant.CodeUpstreamError
	agg.FinalStatus = http.StatusBadGateway
	agg.FinalMessage = "All upstream resolvers failed"
	return agg
}
