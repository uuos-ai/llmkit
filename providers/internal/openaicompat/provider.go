package openaicompat

import (
	"context"
	"fmt"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

type Config struct {
	ID                    llmkit.ProviderID
	Endpoint              string
	DefaultAPI            openai.API
	Capabilities          []llmkit.Capability
	Transport             *transport.Client
	Profile               string
	APIVersion            string
	AuthSchemes           []llmkit.AuthScheme
	RequireTargetEndpoint bool
}

type ChatProvider struct {
	id                    llmkit.ProviderID
	capabilities          []llmkit.Capability
	delegate              *openai.Provider
	profile, apiVersion   string
	authSchemes           []llmkit.AuthScheme
	requireTargetEndpoint bool
}

func NewChat(config Config) (*ChatProvider, error) {
	if config.ID == "" || config.Endpoint == "" {
		return nil, fmt.Errorf("OpenAI-compatible profile requires ID and endpoint")
	}
	api := config.DefaultAPI
	if api == "" {
		api = openai.APIChat
	}
	delegate, err := openai.New(openai.Config{
		ID: config.ID, Endpoint: config.Endpoint, APIPathPrefix: "/",
		DefaultAPI: api, Transport: config.Transport,
	})
	if err != nil {
		return nil, err
	}
	profile, apiVersion := config.Profile, config.APIVersion
	if profile == "" {
		profile = string(config.ID)
	}
	if apiVersion == "" {
		apiVersion = "openai-compatible"
	}
	authSchemes := append([]llmkit.AuthScheme(nil), config.AuthSchemes...)
	if len(authSchemes) == 0 {
		authSchemes = []llmkit.AuthScheme{llmkit.AuthBearer}
	}
	return &ChatProvider{
		id: config.ID, capabilities: append([]llmkit.Capability(nil), config.Capabilities...),
		delegate: delegate, profile: profile, apiVersion: apiVersion, authSchemes: authSchemes, requireTargetEndpoint: config.RequireTargetEndpoint,
	}, nil
}

func (p *ChatProvider) ID() llmkit.ProviderID { return p.id }

func (p *ChatProvider) Manifest() llmkit.AdapterManifest {
	return llmkit.AdapterManifest{
		ProviderID: p.id, AdapterVersion: "1.0.0", ProviderAPIVersion: p.apiVersion,
		Maturity: llmkit.AdapterConformant, Operations: []llmkit.Operation{llmkit.OperationGenerate},
		Capabilities: append([]llmkit.Capability(nil), p.capabilities...), AuthSchemes: append([]llmkit.AuthScheme(nil), p.authSchemes...), Profile: p.profile,
	}
}

func (p *ChatProvider) Capabilities(_ context.Context, target llmkit.Target) (llmkit.Capabilities, error) {
	if err := p.validateTarget(target); err != nil {
		return llmkit.Capabilities{}, err
	}
	return llmkit.Capabilities{Provider: p.id, Models: map[llmkit.ModelID]llmkit.ModelCapabilities{
		target.Model: {Capabilities: append([]llmkit.Capability(nil), p.capabilities...)},
	}}, nil
}

func (p *ChatProvider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	if err := p.validateCall(call); err != nil {
		return llmkit.Response{}, err
	}
	return p.delegate.Generate(ctx, call)
}

func (p *ChatProvider) Stream(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	if err := p.validateCall(call); err != nil {
		return nil, err
	}
	return p.delegate.Stream(ctx, call)
}

func (p *ChatProvider) ValidateCredential(ctx context.Context, call llmkit.CredentialCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	return p.delegate.ValidateCredential(ctx, call)
}

func (p *ChatProvider) ListModels(ctx context.Context, call llmkit.ListModelsCall) (llmkit.ModelPage, error) {
	if call.Target.Provider != p.id {
		return llmkit.ModelPage{}, invalid(call.Target, "target does not match provider profile")
	}
	return p.delegate.ListModels(ctx, call)
}

func (p *ChatProvider) validateCall(call llmkit.GenerateCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	if len(call.Request.ProviderOptions) != 0 {
		return invalid(call.Target, "provider options must be defined by the dedicated profile")
	}
	return nil
}

func (p *ChatProvider) validateTarget(target llmkit.Target) error {
	if target.Provider != p.id || target.Model == "" {
		return invalid(target, "target does not match provider profile")
	}
	if p.requireTargetEndpoint && target.Endpoint == "" {
		return invalid(target, "target endpoint is required for this provider profile")
	}
	return nil
}

type FullProvider struct{ *ChatProvider }

func (p *FullProvider) Manifest() llmkit.AdapterManifest {
	manifest := p.ChatProvider.Manifest()
	manifest.Operations = append(manifest.Operations, llmkit.OperationEmbed)
	return manifest
}

func NewFull(config Config) (*FullProvider, error) {
	provider, err := NewChat(config)
	if err != nil {
		return nil, err
	}
	return &FullProvider{ChatProvider: provider}, nil
}

func (p *FullProvider) Embed(ctx context.Context, call llmkit.EmbedCall) (llmkit.EmbedResponse, error) {
	if err := p.validateTarget(call.Target); err != nil {
		return llmkit.EmbedResponse{}, err
	}
	return p.delegate.Embed(ctx, call)
}

func invalid(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorInvalidRequest, SafeMessage: message,
	}
}

var _ llmkit.Generator = (*ChatProvider)(nil)
var _ llmkit.StreamGenerator = (*ChatProvider)(nil)
var _ llmkit.CredentialValidator = (*ChatProvider)(nil)
var _ llmkit.ModelLister = (*ChatProvider)(nil)
var _ llmkit.ManifestProvider = (*ChatProvider)(nil)
var _ llmkit.Embedder = (*FullProvider)(nil)
