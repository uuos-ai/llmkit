package llmkit

import (
	"context"
	"io"
)

// ProviderID and ModelID are opaque identifiers supplied by host configuration.
type ProviderID string
type ModelID string

// Target is the single, already-authorized execution target selected by the host.
// A provider implementation must not change Provider, Model, Region, or Endpoint.
type Target struct {
	Provider ProviderID
	Model    ModelID
	Region   string
	Endpoint string
}

type Capability string

const (
	CapabilityGenerate   Capability = "generate"
	CapabilityStreaming  Capability = "streaming"
	CapabilityEmbedding  Capability = "embedding"
	CapabilityTools      Capability = "tools"
	CapabilityStructured Capability = "structured_output"
	CapabilityVision     Capability = "vision"
	CapabilityAudio      Capability = "audio"
	CapabilityReasoning  Capability = "reasoning"
)

type ModelCapabilities struct {
	Capabilities   []Capability
	MaxInputTokens int64
}

type Capabilities struct {
	Provider ProviderID
	Models   map[ModelID]ModelCapabilities
}

func (c Capabilities) Supports(model ModelID, capability Capability) bool {
	modelCapabilities, ok := c.Models[model]
	if !ok {
		return false
	}
	for _, candidate := range modelCapabilities.Capabilities {
		if candidate == capability {
			return true
		}
	}
	return false
}

// Provider is deliberately small. Generate, streaming, and embeddings are
// optional capabilities expressed by separate interfaces below.
type Provider interface {
	ID() ProviderID
	Capabilities(ctx context.Context, target Target) (Capabilities, error)
}

type Generator interface {
	Provider
	Generate(ctx context.Context, call GenerateCall) (Response, error)
}

type StreamGenerator interface {
	Provider
	Stream(ctx context.Context, call GenerateCall) (EventStream, error)
}

type Embedder interface {
	Provider
	Embed(ctx context.Context, call EmbedCall) (EmbedResponse, error)
}

// EventStream owns one upstream response body. Recv returns io.EOF only after
// a normal, finalized stream. Truncation and provider-side failures are errors.
type EventStream interface {
	Recv() (StreamEvent, error)
	Close() error
}

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ContentType string

const (
	ContentText       ContentType = "text"
	ContentImage      ContentType = "image"
	ContentAudio      ContentType = "audio"
	ContentReasoning  ContentType = "reasoning"
	ContentToolCall   ContentType = "tool_call"
	ContentToolResult ContentType = "tool_result"
)

// ContentPart is a tagged union. Only the field matching Type may be set.
type ContentPart struct {
	Type       ContentType
	Text       string
	Media      *MediaContent
	ToolCall   *ToolCall
	ToolResult *ToolResult
}

type MediaContent struct {
	MediaType string
	URL       string
	Data      []byte
}

type Message struct {
	Role  Role
	Parts []ContentPart
}

type Tool struct {
	Name        string
	Description string
	InputSchema []byte
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments []byte
}

type ToolResult struct {
	CallID string
	// Name is required by protocols such as Gemini that correlate tool
	// results by function name rather than only by an opaque call ID.
	Name    string
	Content []ContentPart
	IsError bool
}

type ResponseFormat struct {
	Name   string
	Schema []byte
	Strict bool
}

type GenerateRequest struct {
	Messages        []Message
	Tools           []Tool
	ResponseFormat  *ResponseFormat
	MaxOutputTokens *int64
	Temperature     *float64
	TopP            *float64
	Stop            []string
	ProviderOptions map[string][]byte
}

// GenerateCall contains all request-scoped inputs for exactly one target.
// Metadata is host-only and must not be forwarded unless a provider contract
// explicitly defines the corresponding field.
type GenerateCall struct {
	OperationID string
	Target      Target
	Credential  CredentialHandle
	Request     GenerateRequest
	Metadata    map[string]string
}

type EmbedCall struct {
	OperationID string
	Target      Target
	Credential  CredentialHandle
	Input       []string
	Dimensions  *int
	Metadata    map[string]string
}

type EmbedResponse struct {
	ProviderRequestID string
	Vectors           [][]float32
	Usage             Usage
}

type UsageSource string

const (
	UsageMissing   UsageSource = "missing"
	UsageReported  UsageSource = "provider_reported"
	UsageEstimated UsageSource = "estimated"
)

type Usage struct {
	Source          UsageSource
	InputTokens     int64
	OutputTokens    int64
	TotalTokens     int64
	CachedRead      int64
	CachedWrite     int64
	ReasoningTokens int64
	TextTokens      int64
	ImageTokens     int64
	AudioTokens     int64
	// Extensions may contain documented, non-sensitive numeric counters using
	// provider-qualified keys. It must never contain arbitrary provider DTOs.
	Extensions map[string]int64
}

type FinishReason string

const (
	FinishStop          FinishReason = "stop"
	FinishLength        FinishReason = "length"
	FinishToolCalls     FinishReason = "tool_calls"
	FinishContentFilter FinishReason = "content_filter"
	FinishError         FinishReason = "error"
	FinishUnknown       FinishReason = "unknown"
)

type Response struct {
	ProviderRequestID string
	Message           Message
	Usage             Usage
	FinishReason      FinishReason
	Adaptations       []Adaptation
}

type AdaptationQuality string

const (
	AdaptationLossless AdaptationQuality = "lossless"
	AdaptationApprox   AdaptationQuality = "approximate"
	AdaptationLossy    AdaptationQuality = "lossy"
)

// Adaptation makes protocol compromises observable to the host.
type Adaptation struct {
	Field   string
	Action  string
	Quality AdaptationQuality
	Detail  string
}

type StreamEventType string

const (
	EventMessageStart       StreamEventType = "message_start"
	EventContentStart       StreamEventType = "content_start"
	EventTextDelta          StreamEventType = "text_delta"
	EventReasoningDelta     StreamEventType = "reasoning_delta"
	EventToolCallStart      StreamEventType = "tool_call_start"
	EventToolArgumentsDelta StreamEventType = "tool_arguments_delta"
	EventToolCallEnd        StreamEventType = "tool_call_end"
	EventUsage              StreamEventType = "usage"
	EventFinish             StreamEventType = "finish"
)

type StreamEvent struct {
	Type              StreamEventType
	Index             int
	Text              string
	ToolCall          *ToolCall
	ArgumentsDelta    []byte
	Usage             *Usage
	FinishReason      FinishReason
	ProviderRequestID string
	Adaptations       []Adaptation
}

// DrainStream is a convenience for consumers that need all events. It keeps
// io.EOF semantics centralized and always closes the stream.
func DrainStream(stream EventStream) ([]StreamEvent, error) {
	defer stream.Close()
	var events []StreamEvent
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			return events, nil
		}
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
}
