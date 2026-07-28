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
	Provider ProviderID `json:"provider"`
	Model    ModelID    `json:"model"`
	Region   string     `json:"region,omitempty"`
	Endpoint string     `json:"endpoint,omitempty"`
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
	Capabilities   []Capability `json:"capabilities"`
	MaxInputTokens int64        `json:"max_input_tokens,omitempty"`
}

type Capabilities struct {
	Provider ProviderID                    `json:"provider"`
	Models   map[ModelID]ModelCapabilities `json:"models"`
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
	Type       ContentType   `json:"type"`
	Text       string        `json:"text,omitempty"`
	Media      *MediaContent `json:"media,omitempty"`
	ToolCall   *ToolCall     `json:"tool_call,omitempty"`
	ToolResult *ToolResult   `json:"tool_result,omitempty"`
}

type MediaContent struct {
	MediaType string `json:"media_type"`
	URL       string `json:"url,omitempty"`
	Data      []byte `json:"data,omitempty"`
}

type Message struct {
	Role  Role          `json:"role"`
	Parts []ContentPart `json:"parts"`
}

type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema []byte `json:"input_schema"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments []byte `json:"arguments"`
}

type ToolResult struct {
	CallID string `json:"call_id"`
	// Name is required by protocols such as Gemini that correlate tool
	// results by function name rather than only by an opaque call ID.
	Name    string        `json:"name"`
	Content []ContentPart `json:"content"`
	IsError bool          `json:"is_error,omitempty"`
}

type ResponseFormat struct {
	Name   string `json:"name"`
	Schema []byte `json:"schema"`
	Strict bool   `json:"strict"`
}

type GenerateRequest struct {
	Messages        []Message         `json:"messages"`
	Tools           []Tool            `json:"tools,omitempty"`
	ResponseFormat  *ResponseFormat   `json:"response_format,omitempty"`
	MaxOutputTokens *int64            `json:"max_output_tokens,omitempty"`
	Temperature     *float64          `json:"temperature,omitempty"`
	TopP            *float64          `json:"top_p,omitempty"`
	Stop            []string          `json:"stop,omitempty"`
	ProviderOptions map[string][]byte `json:"provider_options,omitempty"`
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
	ProviderRequestID string      `json:"provider_request_id,omitempty"`
	Vectors           [][]float32 `json:"vectors"`
	Usage             Usage       `json:"usage"`
}

type UsageSource string

const (
	UsageMissing   UsageSource = "missing"
	UsageReported  UsageSource = "provider_reported"
	UsageEstimated UsageSource = "estimated"
)

type Usage struct {
	Source          UsageSource `json:"source"`
	InputTokens     int64       `json:"input_tokens,omitempty"`
	OutputTokens    int64       `json:"output_tokens,omitempty"`
	TotalTokens     int64       `json:"total_tokens,omitempty"`
	CachedRead      int64       `json:"cached_read,omitempty"`
	CachedWrite     int64       `json:"cached_write,omitempty"`
	ReasoningTokens int64       `json:"reasoning_tokens,omitempty"`
	TextTokens      int64       `json:"text_tokens,omitempty"`
	ImageTokens     int64       `json:"image_tokens,omitempty"`
	AudioTokens     int64       `json:"audio_tokens,omitempty"`
	// Extensions may contain documented, non-sensitive numeric counters using
	// provider-qualified keys. It must never contain arbitrary provider DTOs.
	Extensions map[string]int64 `json:"extensions,omitempty"`
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
	ProviderRequestID string       `json:"provider_request_id,omitempty"`
	Message           Message      `json:"message"`
	Usage             Usage        `json:"usage"`
	FinishReason      FinishReason `json:"finish_reason"`
	Adaptations       []Adaptation `json:"adaptations,omitempty"`
}

type AdaptationQuality string

const (
	AdaptationLossless AdaptationQuality = "lossless"
	AdaptationApprox   AdaptationQuality = "approximate"
	AdaptationLossy    AdaptationQuality = "lossy"
)

// Adaptation makes protocol compromises observable to the host.
type Adaptation struct {
	Field   string            `json:"field"`
	Action  string            `json:"action"`
	Quality AdaptationQuality `json:"quality"`
	Detail  string            `json:"detail,omitempty"`
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
	Type              StreamEventType `json:"type"`
	Index             int             `json:"index,omitempty"`
	Text              string          `json:"text,omitempty"`
	ToolCall          *ToolCall       `json:"tool_call,omitempty"`
	ArgumentsDelta    []byte          `json:"arguments_delta,omitempty"`
	Usage             *Usage          `json:"usage,omitempty"`
	FinishReason      FinishReason    `json:"finish_reason,omitempty"`
	ProviderRequestID string          `json:"provider_request_id,omitempty"`
	Adaptations       []Adaptation    `json:"adaptations,omitempty"`
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
