// Package bedrock implements Amazon Bedrock Mantle's OpenAI-compatible API.
package bedrock

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "bedrock"

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
}

type Provider struct{ *openaicompat.ChatProvider }

func New(config Config) (*Provider, error) {
	id := config.ID
	if id == "" {
		id = DefaultProviderID
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = "https://example.invalid/v1"
	}
	provider, err := openaicompat.NewChat(openaicompat.Config{ID: id, Endpoint: endpoint, DefaultAPI: openai.APIResponses, Transport: config.Transport,
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityTools, llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning},
		Profile:      "bedrock-mantle", APIVersion: "openai-compatible-v1", AuthSchemes: []llmkit.AuthScheme{llmkit.AuthBearer, llmkit.AuthCloudWorkload}, RequireTargetEndpoint: config.Endpoint == ""})
	if err != nil {
		return nil, err
	}
	return &Provider{ChatProvider: provider}, nil
}
