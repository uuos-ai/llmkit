// Package dashscope implements Alibaba Cloud Model Studio's documented
// OpenAI-compatible Chat, Responses, and text Embedding APIs.
package dashscope

import (
	"context"
	"fmt"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const (
	DefaultProviderID llmkit.ProviderID = "dashscope"
	DefaultEndpoint                     = "https://dashscope.aliyuncs.com/compatible-mode/v1"
)

type Config struct {
	ID         llmkit.ProviderID
	Endpoint   string
	DefaultAPI openai.API
	Transport  *transport.Client
}

type Provider struct {
	id       llmkit.ProviderID
	delegate *openai.Provider
}

func New(config Config) (*Provider, error) {
	id := config.ID
	if id == "" {
		id = DefaultProviderID
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	api := config.DefaultAPI
	if api == "" {
		api = openai.APIChat
	}
	delegate, err := openai.New(openai.Config{
		ID: id, Endpoint: endpoint, APIPathPrefix: "/",
		DefaultAPI: api, Transport: config.Transport,
	})
	if err != nil {
		return nil, fmt.Errorf("dashscope: %w", err)
	}
	return &Provider{id: id, delegate: delegate}, nil
}

func (p *Provider) ID() llmkit.ProviderID { return p.id }

func (p *Provider) Capabilities(_ context.Context, target llmkit.Target) (llmkit.Capabilities, error) {
	if target.Provider != p.id || target.Model == "" {
		return llmkit.Capabilities{}, invalidTarget(target)
	}
	return llmkit.Capabilities{Provider: p.id, Models: map[llmkit.ModelID]llmkit.ModelCapabilities{
		target.Model: {Capabilities: []llmkit.Capability{
			llmkit.CapabilityGenerate, llmkit.CapabilityStreaming,
			llmkit.CapabilityEmbedding, llmkit.CapabilityTools,
			llmkit.CapabilityStructured, llmkit.CapabilityVision,
			llmkit.CapabilityReasoning,
		}},
	}}, nil
}

func (p *Provider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	if err := p.validateGenerate(call); err != nil {
		return llmkit.Response{}, err
	}
	return p.delegate.Generate(ctx, call)
}

func (p *Provider) Stream(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	if err := p.validateGenerate(call); err != nil {
		return nil, err
	}
	return p.delegate.Stream(ctx, call)
}

func (p *Provider) Embed(ctx context.Context, call llmkit.EmbedCall) (llmkit.EmbedResponse, error) {
	if call.Target.Provider != p.id || call.Target.Model == "" {
		return llmkit.EmbedResponse{}, invalidTarget(call.Target)
	}
	return p.delegate.Embed(ctx, call)
}

func (p *Provider) ValidateCredential(ctx context.Context, call llmkit.CredentialCall) error {
	if call.Target.Provider != p.id || call.Target.Model == "" {
		return invalidTarget(call.Target)
	}
	return p.delegate.ValidateCredential(ctx, call)
}

func (p *Provider) validateGenerate(call llmkit.GenerateCall) error {
	if call.Target.Provider != p.id || call.Target.Model == "" {
		return invalidTarget(call.Target)
	}
	if len(call.Request.ProviderOptions) != 0 {
		return &llmkit.ProviderError{
			Provider: p.id, Model: call.Target.Model,
			Kind:        llmkit.ErrorInvalidRequest,
			SafeMessage: "DashScope provider options are not supported; select the API in Config",
		}
	}
	return nil
}

func invalidTarget(target llmkit.Target) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorInvalidRequest, SafeMessage: "invalid DashScope target",
	}
}

var _ llmkit.Generator = (*Provider)(nil)
var _ llmkit.StreamGenerator = (*Provider)(nil)
var _ llmkit.Embedder = (*Provider)(nil)
var _ llmkit.CredentialValidator = (*Provider)(nil)
