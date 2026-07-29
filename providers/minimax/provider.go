// Package minimax implements MiniMax's OpenAI-compatible text protocol.
package minimax

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "minimax"
const DefaultEndpoint = "https://api.minimaxi.com/v1"

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
}

type Provider struct{ *openaicompat.ChatProvider }

func New(config Config) (*Provider, error) {
	id, endpoint := config.ID, config.Endpoint
	if id == "" {
		id = DefaultProviderID
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	provider, err := openaicompat.NewChat(openaicompat.Config{
		ID: id, Endpoint: endpoint, DefaultAPI: openai.APIChat, Transport: config.Transport,
		Capabilities: []llmkit.Capability{
			llmkit.CapabilityGenerate, llmkit.CapabilityStreaming,
			llmkit.CapabilityTools, llmkit.CapabilityReasoning,
		},
	})
	if err != nil {
		return nil, err
	}
	return &Provider{provider}, nil
}
