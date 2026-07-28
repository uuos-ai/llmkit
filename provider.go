package uukit

import "context"

// ProviderID and ModelID are opaque identifiers owned by the host configuration.
type ProviderID string
type ModelID string

type Capability string

const (
	CapabilityChat       Capability = "chat"
	CapabilityResponses  Capability = "responses"
	CapabilityEmbedding  Capability = "embedding"
	CapabilityTools      Capability = "tools"
	CapabilityStructured Capability = "structured_output"
	CapabilityStreaming  Capability = "streaming"
)

// Target is the already-authorized execution target selected by the host.
type Target struct {
	Provider ProviderID
	Model    ModelID
	Region   string
}

// Request is intentionally provider-neutral. Concrete protocol DTOs belong in codec packages.
type Request struct {
	OperationID string
	Capability  Capability
	Target      Target
	Input       any
	Metadata    map[string]string
}

type Usage struct {
	InputTokens     int64
	OutputTokens    int64
	CachedTokens    int64
	ReasoningTokens int64
	ProviderRaw     map[string]any
}

type Response struct {
	ProviderRequestID string
	Output            any
	Usage             Usage
	FinishReason      string
}

type StreamEvent struct {
	Type  string
	Delta any
	Usage *Usage
	Raw   any
}

type Capabilities struct {
	Provider ProviderID
	Models   map[ModelID][]Capability
}

// Adapter performs exactly one attempt against a host-selected Provider and model.
// Routing, cross-provider failover, consent, pricing and ledger settlement remain host concerns.
type Adapter interface {
	ID() ProviderID
	Capabilities(ctx context.Context) (Capabilities, error)
	Invoke(ctx context.Context, request Request) (Response, error)
	Stream(ctx context.Context, request Request, emit func(StreamEvent) error) (Response, error)
}
