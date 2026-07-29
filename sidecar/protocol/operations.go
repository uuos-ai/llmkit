package protocol

import "github.com/uuos-ai/llmkit"

type Credential struct {
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
	Value  []byte `json:"value"`
}

type CapabilitiesRequest struct {
	Target llmkit.Target `json:"target"`
}

type ValidateCredentialRequest struct {
	Target     llmkit.Target `json:"target"`
	Credential Credential    `json:"credential"`
}

type ValidateCredentialResponse struct {
	Valid bool `json:"valid"`
}

type GenerateRequest struct {
	OperationID string                 `json:"operation_id,omitempty"`
	Target      llmkit.Target          `json:"target"`
	Credential  Credential             `json:"credential"`
	Request     llmkit.GenerateRequest `json:"request"`
	Stream      bool                   `json:"stream,omitempty"`
}

type EmbedRequest struct {
	OperationID string        `json:"operation_id,omitempty"`
	Target      llmkit.Target `json:"target"`
	Credential  Credential    `json:"credential"`
	Input       []string      `json:"input"`
	Dimensions  *int          `json:"dimensions,omitempty"`
}
