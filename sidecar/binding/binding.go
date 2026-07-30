// Package binding maps authenticated sidecar operations to an llmkit Registry.
package binding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/routing"
	"github.com/uuos-ai/llmkit/sidecar/protocol"
	"github.com/uuos-ai/llmkit/sidecar/server"
)

type EndpointPolicy func(llmkit.Target) error

type Config struct {
	Registry       *llmkit.Registry
	EndpointPolicy EndpointPolicy
	Routes         *routing.SessionCatalog
}

func Register(target *server.Server, config Config) error {
	if target == nil || config.Registry == nil {
		return fmt.Errorf("sidecar binding: server and registry are required")
	}
	binding := &binder{registry: config.Registry, endpointPolicy: config.EndpointPolicy, routes: config.Routes}
	for method, handler := range map[protocol.Method]server.Handler{
		protocol.MethodListCapabilities:   binding.capabilities,
		protocol.MethodListModels:         binding.listModels,
		protocol.MethodValidateCredential: binding.validateCredential,
		protocol.MethodGenerate:           binding.generate,
		protocol.MethodEmbed:              binding.embed,
	} {
		if err := target.Register(method, handler); err != nil {
			return err
		}
	}
	if config.Routes != nil {
		if err := target.Register(protocol.MethodResolveOptions, binding.resolveOptions); err != nil {
			return err
		}
	}
	return nil
}

func (b *binder) resolveOptions(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.ResolveProviderOptionsRequest
	if len(payload) != 0 {
		if err := decode(payload, &request); err != nil {
			return nil, err
		}
	}
	principal, ok := identity.FromContext(ctx)
	if !ok {
		return nil, &llmkit.ProviderError{Kind: llmkit.ErrorAuthentication, SafeMessage: "authenticated client identity is required"}
	}
	return b.routes.Refresh(ctx, principal, request)
}

func (b *binder) listModels(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.ListModelsRequest
	if err := decode(payload, &request); err != nil {
		return nil, err
	}
	defer clear(request.Credential.Value)
	var err error
	request.Target, err = b.resolveTarget(ctx, request.TargetID, request.Target)
	if err != nil {
		return nil, err
	}
	if err := b.validateProviderTarget(request.Target); err != nil {
		return nil, err
	}
	lister, ok := b.registry.ModelLister(request.Target.Provider)
	if !ok {
		return nil, unsupported(request.Target, "provider does not support model listing")
	}
	credential, err := credentialHandle(request.Target, request.Credential)
	if err != nil {
		return nil, err
	}
	defer credential.clear()
	return lister.ListModels(ctx, llmkit.ListModelsCall{
		Target: request.Target, Credential: credential,
		Cursor: request.Cursor, Limit: request.Limit,
	})
}

func (b *binder) validateCredential(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.ValidateCredentialRequest
	if err := decode(payload, &request); err != nil {
		return nil, err
	}
	defer clear(request.Credential.Value)
	var err error
	request.Target, err = b.resolveTarget(ctx, request.TargetID, request.Target)
	if err != nil {
		return nil, err
	}
	if err := b.validateTarget(request.Target); err != nil {
		return nil, err
	}
	validator, ok := b.registry.CredentialValidator(request.Target.Provider)
	if !ok {
		return nil, unsupported(request.Target, "provider does not support credential validation")
	}
	credential, err := credentialHandle(request.Target, request.Credential)
	if err != nil {
		return nil, err
	}
	defer credential.clear()
	if err := validator.ValidateCredential(ctx, llmkit.CredentialCall{
		Target: request.Target, Credential: credential,
	}); err != nil {
		return nil, err
	}
	return protocol.ValidateCredentialResponse{Valid: true}, nil
}

type binder struct {
	registry       *llmkit.Registry
	endpointPolicy EndpointPolicy
	routes         *routing.SessionCatalog
}

func (b *binder) capabilities(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.CapabilitiesRequest
	if err := decode(payload, &request); err != nil {
		return nil, err
	}
	var err error
	request.Target, err = b.resolveTarget(ctx, request.TargetID, request.Target)
	if err != nil {
		return nil, err
	}
	if err := b.validateTarget(request.Target); err != nil {
		return nil, err
	}
	provider, ok := b.registry.Get(request.Target.Provider)
	if !ok {
		return nil, unsupported(request.Target, "provider is not registered")
	}
	return provider.Capabilities(ctx, request.Target)
}

func (b *binder) generate(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.GenerateRequest
	if err := decode(payload, &request); err != nil {
		return nil, err
	}
	defer clear(request.Credential.Value)
	var err error
	request.Target, err = b.resolveTarget(ctx, request.TargetID, request.Target)
	if err != nil {
		return nil, err
	}
	if err := b.validateTarget(request.Target); err != nil {
		return nil, err
	}
	credential, err := credentialHandle(request.Target, request.Credential)
	if err != nil {
		return nil, err
	}
	call := llmkit.GenerateCall{
		OperationID: request.OperationID, Target: request.Target,
		Credential: credential, Request: request.Request,
	}
	if request.Stream {
		generator, ok := b.registry.StreamGenerator(request.Target.Provider)
		if !ok {
			credential.clear()
			return nil, unsupported(request.Target, "provider does not support streaming")
		}
		stream, streamErr := generator.Stream(ctx, call)
		if streamErr != nil {
			credential.clear()
			return nil, streamErr
		}
		return server.EventSequence{Run: func(_ context.Context, emitter server.Emitter) error {
			defer credential.clear()
			defer stream.Close()
			for {
				event, receiveErr := stream.Recv()
				if receiveErr == io.EOF {
					return nil
				}
				if receiveErr != nil {
					return receiveErr
				}
				if emitErr := emitter.Emit(event); emitErr != nil {
					return emitErr
				}
			}
		}}, nil
	}
	generator, ok := b.registry.Generator(request.Target.Provider)
	if !ok {
		credential.clear()
		return nil, unsupported(request.Target, "provider does not support generation")
	}
	defer credential.clear()
	return generator.Generate(ctx, call)
}

func (b *binder) embed(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.EmbedRequest
	if err := decode(payload, &request); err != nil {
		return nil, err
	}
	defer clear(request.Credential.Value)
	var err error
	request.Target, err = b.resolveTarget(ctx, request.TargetID, request.Target)
	if err != nil {
		return nil, err
	}
	if err := b.validateTarget(request.Target); err != nil {
		return nil, err
	}
	embedder, ok := b.registry.Embedder(request.Target.Provider)
	if !ok {
		return nil, unsupported(request.Target, "provider does not support embeddings")
	}
	credential, err := credentialHandle(request.Target, request.Credential)
	if err != nil {
		return nil, err
	}
	defer credential.clear()
	return embedder.Embed(ctx, llmkit.EmbedCall{
		OperationID: request.OperationID, Target: request.Target,
		Credential: credential, Input: request.Input, Dimensions: request.Dimensions,
	})
}

func (b *binder) resolveTarget(ctx context.Context, targetID string, raw llmkit.Target) (llmkit.Target, error) {
	if targetID == "" && raw.Provider != "" {
		return raw, nil
	}
	if b.routes == nil {
		return llmkit.Target{}, &llmkit.ProviderError{Kind: llmkit.ErrorInvalidRequest, SafeMessage: "target is required"}
	}
	principal, ok := identity.FromContext(ctx)
	if !ok {
		return llmkit.Target{}, &llmkit.ProviderError{Kind: llmkit.ErrorAuthentication, SafeMessage: "authenticated client identity is required"}
	}
	target, _, err := b.routes.Resolve(ctx, principal, targetID)
	if err != nil {
		return llmkit.Target{}, &llmkit.ProviderError{Kind: llmkit.ErrorInvalidRequest, SafeMessage: err.Error()}
	}
	return target, nil
}

func (b *binder) validateTarget(target llmkit.Target) error {
	if err := b.validateProviderTarget(target); err != nil {
		return err
	}
	if target.Model == "" {
		return &llmkit.ProviderError{
			Provider: target.Provider, Model: target.Model,
			Kind: llmkit.ErrorInvalidRequest, SafeMessage: "model is required",
		}
	}
	return nil
}

func (b *binder) validateProviderTarget(target llmkit.Target) error {
	if target.Provider == "" {
		return &llmkit.ProviderError{
			Provider: target.Provider, Model: target.Model,
			Kind: llmkit.ErrorInvalidRequest, SafeMessage: "provider is required",
		}
	}
	if target.Endpoint == "" {
		return nil
	}
	if b.endpointPolicy == nil {
		return &llmkit.ProviderError{
			Provider: target.Provider, Model: target.Model,
			Kind: llmkit.ErrorPermission, SafeMessage: "custom endpoint is not allowed",
		}
	}
	return b.endpointPolicy(target)
}

type requestCredential struct {
	header string
	value  []byte
}

func (c *requestCredential) Apply(_ context.Context, _ llmkit.Target, request *http.Request) error {
	request.Header.Set(c.header, string(c.value))
	return nil
}

func (c *requestCredential) clear() { clear(c.value) }

func credentialHandle(target llmkit.Target, credential protocol.Credential) (*requestCredential, error) {
	if len(credential.Value) == 0 {
		return nil, authentication(target, "credential value is required")
	}
	header := credential.Header
	switch credential.Type {
	case "bearer":
		header = "Authorization"
		credential.Value = append([]byte("Bearer "), credential.Value...)
	case "header":
		if !allowedCredentialHeader(header) {
			return nil, authentication(target, "credential header is not allowed")
		}
	default:
		return nil, authentication(target, "credential type is not supported")
	}
	value := append([]byte(nil), credential.Value...)
	clear(credential.Value)
	return &requestCredential{header: header, value: value}, nil
}

func allowedCredentialHeader(header string) bool {
	switch strings.ToLower(header) {
	case "authorization", "x-api-key", "x-goog-api-key":
		return true
	default:
		return false
	}
}

func decode(payload json.RawMessage, destination any) error {
	if len(payload) == 0 || json.Unmarshal(payload, destination) != nil {
		return &llmkit.ProviderError{
			Kind: llmkit.ErrorInvalidRequest, SafeMessage: "sidecar payload is malformed",
		}
	}
	return nil
}

func authentication(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorAuthentication, SafeMessage: message,
	}
}

func unsupported(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorUnsupported, SafeMessage: message,
	}
}
