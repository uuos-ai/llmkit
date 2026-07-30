package managed

import (
	"context"
	"time"

	"github.com/uuos-ai/llmkit/identity"
)

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
