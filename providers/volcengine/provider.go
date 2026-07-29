// Package volcengine implements Volcengine Ark's OpenAI-compatible Responses protocol.
package volcengine

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "volcengine"
const DefaultEndpoint = "https://ark.cn-beijing.volces.com/api/v3"

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
	p, err := openaicompat.NewChat(openaicompat.Config{
		ID: id, Endpoint: endpoint, DefaultAPI: openai.APIResponses, Transport: config.Transport,
		Capabilities: []llmkit.Capability{
			llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityTools,
			llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning,
		},
	})
	if err != nil {
		return nil, err
	}
	return &Provider{p}, nil
}
