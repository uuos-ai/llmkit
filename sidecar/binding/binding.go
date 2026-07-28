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
	"github.com/uuos-ai/llmkit/sidecar/protocol"
	"github.com/uuos-ai/llmkit/sidecar/server"
)

type EndpointPolicy func(llmkit.Target) error

type Config struct {
	Registry       *llmkit.Registry
	EndpointPolicy EndpointPolicy
}

func Register(target *server.Server, config Config) error {
	if target == nil || config.Registry == nil {
		return fmt.Errorf("sidecar binding: server and registry are required")
	}
	binding := &binder{registry: config.Registry, endpointPolicy: config.EndpointPolicy}
	for method, handler := range map[protocol.Method]server.Handler{
		protocol.MethodListCapabilities: binding.capabilities,
		protocol.MethodGenerate:         binding.generate,
		protocol.MethodEmbed:            binding.embed,
	} {
		if err := target.Register(method, handler); err != nil {
			return err
		}
	}
	return nil
}

type binder struct {
	registry       *llmkit.Registry
	endpointPolicy EndpointPolicy
}

func (b *binder) capabilities(ctx context.Context, payload json.RawMessage) (any, error) {
	var request protocol.CapabilitiesRequest
	if err := decode(payload, &request); err != nil {
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

func (b *binder) validateTarget(target llmkit.Target) error {
	if target.Provider == "" || target.Model == "" {
		return &llmkit.ProviderError{
			Provider: target.Provider, Model: target.Model,
			Kind: llmkit.ErrorInvalidRequest, SafeMessage: "provider and model are required",
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
