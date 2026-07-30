package llmkit

import (
	"fmt"
	"time"
)

type ErrorKind string

const (
	ErrorAuthenticationFailed   ErrorKind = "authentication_failed"
	ErrorPermissionDenied       ErrorKind = "permission_denied"
	ErrorInvalidRequest         ErrorKind = "invalid_request"
	ErrorCapabilityNotSupported ErrorKind = "capability_not_supported"
	ErrorTargetNotFound         ErrorKind = "target_not_found"
	ErrorTargetUnavailable      ErrorKind = "target_unavailable"
	ErrorCredentialUnavailable  ErrorKind = "credential_unavailable"
	ErrorContextLengthExceeded  ErrorKind = "context_length_exceeded"
	ErrorRateLimited            ErrorKind = "rate_limited"
	ErrorQuotaExceeded          ErrorKind = "quota_exceeded"
	ErrorContentFiltered        ErrorKind = "content_filtered"
	ErrorProviderUnavailable    ErrorKind = "provider_unavailable"
	ErrorProvider               ErrorKind = "provider_error"
	ErrorTimeout                ErrorKind = "timeout"
	ErrorCancelled              ErrorKind = "cancelled"
	ErrorProtocol               ErrorKind = "protocol_error"
	ErrorOutcomeUnknown         ErrorKind = "request_outcome_unknown"
	ErrorInternal               ErrorKind = "internal_error"

	// Compatibility names retain source compatibility while emitting the
	// normalized v1 error vocabulary.
	ErrorAuthentication    = ErrorAuthenticationFailed
	ErrorPermission        = ErrorPermissionDenied
	ErrorUnsupported       = ErrorCapabilityNotSupported
	ErrorModelNotFound     = ErrorTargetNotFound
	ErrorContextLength     = ErrorContextLengthExceeded
	ErrorRateLimit         = ErrorRateLimited
	ErrorQuotaExhausted    = ErrorQuotaExceeded
	ErrorContentBlocked    = ErrorContentFiltered
	ErrorOverloaded        = ErrorProviderUnavailable
	ErrorCanceled          = ErrorCancelled
	ErrorTransport         = ErrorProviderUnavailable
	ErrorMalformedResponse = ErrorProtocol
	ErrorUnknown           = ErrorProvider
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
