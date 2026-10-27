package service

import (
	"fmt"
)

// Error is the service-level error consumed by the API layer.
type Error struct {
	Code       string
	Message    string
	StatusCode int
	Cause      error
}

// Error makes *Error an error.
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s (cause: %v)", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the underlying cause for errors.As/Is.
func (e *Error) Unwrap() error {
	return e.Cause
}
