// Package deepseek implements DeepSeek's documented OpenAI-compatible Chat
// Completions protocol without exposing unsupported OpenAI capabilities.
package deepseek

import (
	"context"
	"fmt"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const (
	DefaultProviderID llmkit.ProviderID = "deepseek"
	DefaultEndpoint                     = "https://api.deepseek.com"
)

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
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
	delegate, err := openai.New(openai.Config{
		ID: id, Endpoint: endpoint, APIPathPrefix: "/",
		DefaultAPI: openai.APIChat, Transport: config.Transport,
	})
	if err != nil {
		return nil, fmt.Errorf("deepseek: %w", err)
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
			llmkit.CapabilityTools, llmkit.CapabilityStructured,
			llmkit.CapabilityReasoning,
		}},
	}}, nil
}

func (p *Provider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	prepared, err := p.prepare(call)
	if err != nil {
		return llmkit.Response{}, err
	}
	return p.delegate.Generate(ctx, prepared)
}

func (p *Provider) Stream(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	prepared, err := p.prepare(call)
	if err != nil {
		return nil, err
	}
	return p.delegate.Stream(ctx, prepared)
}

func (p *Provider) ValidateCredential(ctx context.Context, call llmkit.CredentialCall) error {
	if call.Target.Provider != p.id || call.Target.Model == "" {
		return invalidTarget(call.Target)
	}
	return p.delegate.ValidateCredential(ctx, call)
}

func (p *Provider) prepare(call llmkit.GenerateCall) (llmkit.GenerateCall, error) {
	if call.Target.Provider != p.id || call.Target.Model == "" {
		return llmkit.GenerateCall{}, invalidTarget(call.Target)
	}
	if len(call.Request.ProviderOptions) != 0 {
		return llmkit.GenerateCall{}, &llmkit.ProviderError{
			Provider: p.id, Model: call.Target.Model,
			Kind:        llmkit.ErrorInvalidRequest,
			SafeMessage: "DeepSeek provider options are not yet supported",
		}
	}
	call.Request.ProviderOptions = map[string][]byte{
		openai.OptionAPI: []byte(openai.APIChat),
	}
	return call, nil
}

func invalidTarget(target llmkit.Target) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorInvalidRequest, SafeMessage: "invalid DeepSeek target",
	}
}

var _ llmkit.Generator = (*Provider)(nil)
var _ llmkit.StreamGenerator = (*Provider)(nil)
var _ llmkit.CredentialValidator = (*Provider)(nil)
