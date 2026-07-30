// Package azure implements Azure OpenAI's v1 OpenAI-compatible endpoint.
package azure

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "azure-openai"

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
}

type Provider struct{ *openaicompat.FullProvider }

func New(config Config) (*Provider, error) {
	id := config.ID
	if id == "" {
		id = DefaultProviderID
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = "https://example.invalid/openai/v1"
	}
	provider, err := openaicompat.NewFull(openaicompat.Config{ID: id, Endpoint: endpoint, DefaultAPI: openai.APIChat, Transport: config.Transport,
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityEmbedding, llmkit.CapabilityTools, llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning},
		Profile:      "azure-openai-v1", APIVersion: "v1", AuthSchemes: []llmkit.AuthScheme{llmkit.AuthAPIKeyHeader, llmkit.AuthCloudWorkload}, RequireTargetEndpoint: config.Endpoint == ""})
	if err != nil {
		return nil, err
	}
	return &Provider{FullProvider: provider}, nil
}
