package uukit

import "fmt"

type ErrorKind string

const (
	ErrorAuthentication  ErrorKind = "authentication"
	ErrorPermission      ErrorKind = "permission"
	ErrorRateLimited     ErrorKind = "rate_limited"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorContextOverflow ErrorKind = "context_overflow"
	ErrorInvalidRequest  ErrorKind = "invalid_request"
	ErrorInvalidResponse ErrorKind = "invalid_response"
	ErrorUnavailable     ErrorKind = "unavailable"
	ErrorCancelled       ErrorKind = "cancelled"
	ErrorUnknown         ErrorKind = "unknown"
)

type ProviderError struct {
	Provider     ProviderID
	Model        ModelID
	Kind         ErrorKind
	StatusCode   int
	Retryable    bool
	RetryAfterMS int64
	SafeMessage  string
	ProviderCode string
	Cause        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("provider %s model %s: %s", e.Provider, e.Model, e.SafeMessage)
}

func (e *ProviderError) Unwrap() error { return e.Cause }
