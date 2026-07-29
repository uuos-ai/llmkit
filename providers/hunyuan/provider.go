// Package hunyuan implements Tencent Hunyuan's OpenAI-compatible Chat and Embedding APIs.
package hunyuan

import (
	"context"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "hunyuan"
const DefaultEndpoint = "https://api.hunyuan.cloud.tencent.com/v1"

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
}

func (p *Provider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	if len(call.Request.Stop) > 0 {
		return llmkit.Response{}, unsupported(call.Target, "Hunyuan stop semantics differ from OpenAI and require explicit adaptation")
	}
	return p.FullProvider.Generate(ctx, call)
}

func (p *Provider) Stream(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	if len(call.Request.Stop) > 0 {
		return nil, unsupported(call.Target, "Hunyuan stop semantics differ from OpenAI and require explicit adaptation")
	}
	return p.FullProvider.Stream(ctx, call)
}

func (p *Provider) Embed(ctx context.Context, call llmkit.EmbedCall) (llmkit.EmbedResponse, error) {
	if call.Dimensions != nil {
		return llmkit.EmbedResponse{}, unsupported(call.Target, "Hunyuan embeddings use a fixed dimension")
	}
	return p.FullProvider.Embed(ctx, call)
}

func unsupported(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorUnsupported, SafeMessage: message,
	}
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
	p, err := openaicompat.NewFull(openaicompat.Config{
		ID: id, Endpoint: endpoint, DefaultAPI: openai.APIChat, Transport: config.Transport,
		Capabilities: []llmkit.Capability{
			llmkit.CapabilityGenerate, llmkit.CapabilityStreaming,
			llmkit.CapabilityEmbedding, llmkit.CapabilityTools, llmkit.CapabilityVision,
		},
	})
	if err != nil {
		return nil, err
	}
	return &Provider{p}, nil
}
