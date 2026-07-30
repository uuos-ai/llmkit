package protocol

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
	"github.com/uuos-ai/llmkit/routing"
)

type Credential struct {
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
	Value  []byte `json:"value"`
}

type EnrollClientResponse = identity.TokenPair

type CapabilitiesRequest struct {
	TargetID string        `json:"target_id,omitempty"`
	Target   llmkit.Target `json:"target"`
}

type ValidateCredentialRequest struct {
	TargetID   string        `json:"target_id,omitempty"`
	Target     llmkit.Target `json:"target"`
	Credential Credential    `json:"credential"`
}

type ValidateCredentialResponse struct {
	Valid bool `json:"valid"`
}

type ListModelsRequest struct {
	TargetID   string        `json:"target_id,omitempty"`
	Target     llmkit.Target `json:"target"`
	Credential Credential    `json:"credential"`
	Cursor     string        `json:"cursor,omitempty"`
	Limit      int           `json:"limit,omitempty"`
}

type GenerateRequest struct {
	OperationID string                 `json:"operation_id,omitempty"`
	TargetID    string                 `json:"target_id,omitempty"`
	Target      llmkit.Target          `json:"target"`
	Credential  Credential             `json:"credential"`
	Request     llmkit.GenerateRequest `json:"request"`
	Stream      bool                   `json:"stream,omitempty"`
}

type EmbedRequest struct {
	OperationID string        `json:"operation_id,omitempty"`
	TargetID    string        `json:"target_id,omitempty"`
	Target      llmkit.Target `json:"target"`
	Credential  Credential    `json:"credential"`
	Input       []string      `json:"input"`
	Dimensions  *int          `json:"dimensions,omitempty"`
}

type RerankRequest struct {
	OperationID string        `json:"operation_id,omitempty"`
	TargetID    string        `json:"target_id,omitempty"`
	Target      llmkit.Target `json:"target"`
	Credential  Credential    `json:"credential"`
	Query       string        `json:"query"`
	Documents   []string      `json:"documents"`
	TopN        *int          `json:"top_n,omitempty"`
}

type ModerateRequest struct {
	OperationID string               `json:"operation_id,omitempty"`
	TargetID    string               `json:"target_id,omitempty"`
	Target      llmkit.Target        `json:"target"`
	Credential  Credential           `json:"credential"`
	Content     []llmkit.ContentPart `json:"content"`
}

type ResolveProviderOptionsRequest = routing.OptionsRequest
type ResolveProviderOptionsResponse = routing.OptionsResponse

type UpsertCustomProviderRequest = managed.CustomProviderInput

type DeleteCustomProviderRequest struct {
	ProviderID string `json:"provider_id"`
}
