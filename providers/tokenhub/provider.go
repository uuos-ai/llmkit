// Package tokenhub implements Tencent TokenHub's documented OpenAI-compatible
// endpoint as a dedicated profile so protocol differences can be tested
// independently from generic OpenAI-compatible endpoints.
package tokenhub

import (
	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/transport"
)

const (
	DefaultProviderID llmkit.ProviderID = "tokenhub"
	DefaultEndpoint                     = "https://tokenhub.tencentmaas.com/v1"
)

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
}

func New(config Config) (*openaicompat.ChatProvider, error) {
	id := config.ID
	if id == "" {
		id = DefaultProviderID
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return openaicompat.NewChat(openaicompat.Config{ID: id, Endpoint: endpoint, Transport: config.Transport,
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming}})
}
