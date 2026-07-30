// Package managed defines gateway storage ports. Implementations belong to the
// embedding business platform; llmkit does not prescribe a database or vault.
package managed

import (
	"context"
	"io"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/routing"
)

type Target struct {
	ID             string                 `json:"id"`
	Target         llmkit.Target          `json:"target"`
	OwnerScope     routing.OwnerScope     `json:"owner_scope,omitempty"`
	CredentialMode routing.CredentialMode `json:"credential_mode,omitempty"`
	CredentialRef  string                 `json:"credential_ref"`
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
	Timestamp      time.Time    `json:"timestamp"`
	ClientID       string       `json:"client_id"`
	UserID         string       `json:"user_id,omitempty"`
	BindingVersion uint64       `json:"binding_version,omitempty"`
	Operation      string       `json:"operation"`
	OperationID    string       `json:"operation_id,omitempty"`
	TargetID       string       `json:"target_id,omitempty"`
	Outcome        string       `json:"outcome"`
	ErrorKind      string       `json:"error_kind,omitempty"`
	Usage          llmkit.Usage `json:"usage,omitempty"`
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
	ExpectedRevision string                 `json:"expected_revision,omitempty"`
	BindingVersion   uint64                 `json:"binding_version,omitempty"`
	IdempotencyKey   string                 `json:"idempotency_key,omitempty"`
	Provider         routing.ProviderOption `json:"provider"`
	Credential       CredentialInput        `json:"credential"`
}

type CredentialInput struct {
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
	Value  []byte `json:"value"`
}

// CredentialLifecycleStore supports pending -> active -> retired secret
// versions. Clients never receive CredentialRef values.
type CredentialLifecycleStore interface {
	StageCredential(context.Context, identity.Principal, CredentialInput) (pendingID string, err error)
	ActivateCredential(context.Context, identity.Principal, string, string) (secretVersion string, err error)
	AbortCredential(context.Context, identity.Principal, string) error
	RetireCredential(context.Context, identity.Principal, string, string) error
}

type BlobMetadata struct {
	Ref       string    `json:"blob_ref"`
	MediaType string    `json:"media_type"`
	SizeBytes int64     `json:"size_bytes"`
	Checksum  string    `json:"checksum"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BlobStore interface {
	Put(context.Context, identity.Principal, BlobMetadata, io.Reader) (BlobMetadata, error)
	Open(context.Context, identity.Principal, string) (io.ReadCloser, BlobMetadata, error)
	Delete(context.Context, identity.Principal, string) error
}
