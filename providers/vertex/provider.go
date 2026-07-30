// Package vertex implements Vertex AI's OpenAI-compatible endpoint profile.
package vertex

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "vertex-ai"

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
		endpoint = "https://example.invalid/v1beta1/openapi"
	}
	provider, err := openaicompat.NewChat(openaicompat.Config{ID: id, Endpoint: endpoint, DefaultAPI: openai.APIChat, Transport: config.Transport,
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityTools, llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning},
		Profile:      "vertex-openai-compatible", APIVersion: "v1beta1", AuthSchemes: []llmkit.AuthScheme{llmkit.AuthCloudWorkload}, RequireTargetEndpoint: config.Endpoint == ""})
	if err != nil {
		return nil, err
	}
	return &Provider{ChatProvider: provider}, nil
}
