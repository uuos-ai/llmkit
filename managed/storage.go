// Package managed defines gateway storage ports. Implementations belong to the
// embedding business platform; llmkit does not prescribe a database or vault.
package managed

import (
	"context"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/routing"
)

type Target struct {
	ID            string        `json:"id"`
	Target        llmkit.Target `json:"target"`
	CredentialRef string        `json:"credential_ref"`
}

type ConfigStore interface {
	ProviderOptions(context.Context, identity.Principal) (routing.OptionsResponse, error)
	ResolveTarget(context.Context, identity.Principal, string) (Target, error)
}

// SecretStore returns a request-scoped handle. release must erase/release any
// transient material and is always called by the gateway.
type SecretStore interface {
	OpenCredential(context.Context, identity.Principal, string) (handle llmkit.CredentialHandle, release func(), err error)
}

type AuditEvent struct {
	Timestamp   time.Time    `json:"timestamp"`
	TenantID    string       `json:"tenant_id,omitempty"`
	ClientID    string       `json:"client_id"`
	Operation   string       `json:"operation"`
	OperationID string       `json:"operation_id,omitempty"`
	TargetID    string       `json:"target_id,omitempty"`
	Outcome     string       `json:"outcome"`
	ErrorKind   string       `json:"error_kind,omitempty"`
	Usage       llmkit.Usage `json:"usage,omitempty"`
}

type AuditStore interface {
	Append(context.Context, AuditEvent) error
}

// CustomProviderStore owns the platform-specific atomic write to ConfigStore
// and Vault/KMS. Input credential bytes must never be returned by an API.
type CustomProviderStore interface {
	UpsertCustomProvider(context.Context, identity.Principal, CustomProviderInput) (routing.OptionsResponse, error)
	DeleteCustomProvider(context.Context, identity.Principal, string) (routing.OptionsResponse, error)
}

type CustomProviderInput struct {
	Provider   routing.ProviderOption `json:"provider"`
	Credential []byte                 `json:"credential"`
}
