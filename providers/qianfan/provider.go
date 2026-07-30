// Package qianfan implements Baidu Qianfan's OpenAI-compatible v2 profile.
package qianfan

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "qianfan"
const DefaultEndpoint = "https://qianfan.baidubce.com/v2"

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
}

type Provider struct{ *openaicompat.FullProvider }

func New(config Config) (*Provider, error) {
	id, endpoint := config.ID, config.Endpoint
	if id == "" {
		id = DefaultProviderID
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	provider, err := openaicompat.NewFull(openaicompat.Config{ID: id, Endpoint: endpoint, DefaultAPI: openai.APIChat, Transport: config.Transport,
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityEmbedding, llmkit.CapabilityTools, llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning}})
	if err != nil {
		return nil, err
	}
	return &Provider{FullProvider: provider}, nil
}
