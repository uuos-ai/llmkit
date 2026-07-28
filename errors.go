package llmkit

import (
	"fmt"
	"time"
)

type ErrorKind string

const (
	ErrorAuthentication    ErrorKind = "authentication"
	ErrorPermission        ErrorKind = "permission"
	ErrorInvalidRequest    ErrorKind = "invalid_request"
	ErrorUnsupported       ErrorKind = "unsupported_feature"
	ErrorModelNotFound     ErrorKind = "model_not_found"
	ErrorContextLength     ErrorKind = "context_length"
	ErrorRateLimit         ErrorKind = "rate_limit"
	ErrorQuotaExhausted    ErrorKind = "quota_exhausted"
	ErrorContentBlocked    ErrorKind = "content_blocked"
	ErrorOverloaded        ErrorKind = "overloaded"
	ErrorTimeout           ErrorKind = "timeout"
	ErrorCanceled          ErrorKind = "canceled"
	ErrorTransport         ErrorKind = "transport"
	ErrorMalformedResponse ErrorKind = "malformed_response"
	ErrorUnknown           ErrorKind = "unknown"
)

type TransportPhase string

const (
	PhaseResolveDNS   TransportPhase = "resolve_dns"
	PhaseConnect      TransportPhase = "connect"
	PhaseTLS          TransportPhase = "tls"
	PhaseWriteRequest TransportPhase = "write_request"
	PhaseReadHeaders  TransportPhase = "read_headers"
	PhaseReadBody     TransportPhase = "read_body"
	PhaseDecode       TransportPhase = "decode"
	PhaseStream       TransportPhase = "stream"
)

// ProviderError contains only host-safe diagnostics. Cause must also be safe:
// adapters must not wrap errors containing credentials, prompts, or raw bodies.
type ProviderError struct {
	Provider     ProviderID
	Model        ModelID
	Kind         ErrorKind
	Phase        TransportPhase
	StatusCode   int
	Retryable    bool
	RetryAfter   time.Duration
	SafeMessage  string
	ProviderCode string
	RequestID    string
	Cause        error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := e.SafeMessage
	if message == "" {
		message = string(e.Kind)
	}
	return fmt.Sprintf("llmkit: provider %s model %s: %s", e.Provider, e.Model, message)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}
