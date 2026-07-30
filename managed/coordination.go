package managed

import (
	"context"
	"time"

	"github.com/uuos-ai/llmkit/identity"
)

type ServiceTokenSource interface {
	ServiceToken(context.Context) ([]byte, error)
}

type SessionStatus string

const (
	SessionActive  SessionStatus = "active"
	SessionRevoked SessionStatus = "revoked"
)

type Session struct {
	SessionID    string             `json:"session_id"`
	Principal    identity.Principal `json:"principal"`
	FamilyID     string             `json:"family_id"`
	Generation   uint64             `json:"generation"`
	FencingToken uint64             `json:"fencing_token"`
	Status       SessionStatus      `json:"status"`
	ExpiresAt    time.Time          `json:"expires_at"`
}

type SessionStore interface {
	GetSession(context.Context, string) (Session, bool, error)
	ReplaceSession(context.Context, Session, uint64) (Session, error)
	RevokeSession(context.Context, string, uint64, string) error
}

type RateLimitRequest struct {
	ClientID        string `json:"client_id"`
	UserID          string `json:"user_id,omitempty"`
	TargetID        string `json:"target_id,omitempty"`
	ProviderAccount string `json:"provider_account,omitempty"`
	Requests        int64  `json:"requests,omitempty"`
	InputTokens     int64  `json:"input_tokens,omitempty"`
	OutputTokens    int64  `json:"output_tokens,omitempty"`
	Concurrency     int64  `json:"concurrency,omitempty"`
}
type RateLimitLease struct {
	LeaseID    string        `json:"lease_id"`
	Allowed    bool          `json:"allowed"`
	RetryAfter time.Duration `json:"retry_after,omitempty"`
	ExpiresAt  time.Time     `json:"expires_at,omitempty"`
}
type RateLimitUsage struct {
	InputTokens  int64 `json:"input_tokens,omitempty"`
	OutputTokens int64 `json:"output_tokens,omitempty"`
	Requests     int64 `json:"requests,omitempty"`
}
type RateLimitStore interface {
	Reserve(context.Context, RateLimitRequest) (RateLimitLease, error)
	Commit(context.Context, string, RateLimitUsage) error
	Release(context.Context, string) error
}

type OIDCExchangeRequest struct {
	Assertion        string `json:"assertion"`
	ClientInstanceID string `json:"client_instance_id"`
}

type RefreshTokenRequest struct {
	AccessToken    string `json:"access_token"`
	RefreshToken   string `json:"refresh_token"`
	IdempotencyKey string `json:"idempotency_key"`
}

type BindUserRequest struct {
	AccessToken            string `json:"access_token"`
	UserID                 string `json:"user_id"`
	ExpectedBindingVersion uint64 `json:"expected_binding_version"`
	IdempotencyKey         string `json:"idempotency_key"`
	Proof                  []byte `json:"proof,omitempty"`
}

// IdentityService is the strongly consistent gateway authority for token
// families and user switches. OIDC is accepted only by ExchangeOIDC.
type IdentityService interface {
	identity.Authenticator
	ExchangeOIDC(context.Context, OIDCExchangeRequest) (identity.TokenPair, error)
	RefreshToken(context.Context, RefreshTokenRequest) (identity.TokenPair, error)
	BindUser(context.Context, BindUserRequest) (identity.TokenPair, identity.UserBinding, error)
}
