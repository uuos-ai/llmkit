package binding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/sidecar/protocol"
	"github.com/uuos-ai/llmkit/sidecar/server"
)

type fakeProvider struct {
	header string
}

func (p *fakeProvider) ID() llmkit.ProviderID { return "fake" }

func (p *fakeProvider) Capabilities(context.Context, llmkit.Target) (llmkit.Capabilities, error) {
	return llmkit.Capabilities{Provider: "fake"}, nil
}

func (p *fakeProvider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://example.invalid", nil)
	if err := call.Credential.Apply(ctx, call.Target, request); err != nil {
		return llmkit.Response{}, err
	}
	p.header = request.Header.Get("Authorization")
	return llmkit.Response{
		Message: llmkit.Message{Role: llmkit.RoleAssistant, Parts: []llmkit.ContentPart{{
			Type: llmkit.ContentText, Text: "hello",
		}}},
		FinishReason: llmkit.FinishStop,
	}, nil
}

func (p *fakeProvider) Embed(context.Context, llmkit.EmbedCall) (llmkit.EmbedResponse, error) {
	return llmkit.EmbedResponse{Vectors: [][]float32{{1, 2}}}, nil
}

func (p *fakeProvider) ValidateCredential(ctx context.Context, call llmkit.CredentialCall) error {
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid", nil)
	if err := call.Credential.Apply(ctx, call.Target, request); err != nil {
		return err
	}
	p.header = request.Header.Get("Authorization")
	return nil
}

func (p *fakeProvider) Stream(context.Context, llmkit.GenerateCall) (llmkit.EventStream, error) {
	return &fakeStream{events: []llmkit.StreamEvent{
		{Type: llmkit.EventTextDelta, Text: "hello"},
		{Type: llmkit.EventFinish, FinishReason: llmkit.FinishStop},
	}}, nil
}

type fakeStream struct {
	events []llmkit.StreamEvent
}

func (s *fakeStream) Recv() (llmkit.StreamEvent, error) {
	if len(s.events) == 0 {
		return llmkit.StreamEvent{}, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}

func (*fakeStream) Close() error { return nil }

type collectingEmitter struct {
	events []llmkit.StreamEvent
}

func (e *collectingEmitter) Emit(value any) error {
	e.events = append(e.events, value.(llmkit.StreamEvent))
	return nil
}

func testBinder(t *testing.T) (*binder, *fakeProvider) {
	t.Helper()
	registry := llmkit.NewRegistry()
	provider := &fakeProvider{}
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	return &binder{registry: registry}, provider
}

func TestGenerateMapsCredentialAndTypedPayload(t *testing.T) {
	binding, provider := testBinder(t)
	payload, err := json.Marshal(protocol.GenerateRequest{
		Target:     llmkit.Target{Provider: "fake", Model: "model"},
		Credential: protocol.Credential{Type: "bearer", Value: []byte("test-secret")},
		Request: llmkit.GenerateRequest{Messages: []llmkit.Message{{
			Role:  llmkit.RoleUser,
			Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hello"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := binding.generate(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	response := result.(llmkit.Response)
	if provider.header != "Bearer test-secret" || response.FinishReason != llmkit.FinishStop {
		t.Fatalf("header=%q response=%#v", provider.header, response)
	}
}

func TestCustomEndpointDeniedByDefault(t *testing.T) {
	binding, _ := testBinder(t)
	payload, _ := json.Marshal(protocol.CapabilitiesRequest{Target: llmkit.Target{
		Provider: "fake", Model: "model", Endpoint: "http://127.0.0.1:8080",
	}})
	_, err := binding.capabilities(context.Background(), payload)
	providerErr, ok := err.(*llmkit.ProviderError)
	if !ok || providerErr.Kind != llmkit.ErrorPermission {
		t.Fatalf("error = %#v", err)
	}
}

func TestValidateCredentialUsesRequestScopedSecret(t *testing.T) {
	binding, provider := testBinder(t)
	payload, err := json.Marshal(protocol.ValidateCredentialRequest{
		Target:     llmkit.Target{Provider: "fake", Model: "model"},
		Credential: protocol.Credential{Type: "bearer", Value: []byte("validation-secret")},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := binding.validateCredential(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if provider.header != "Bearer validation-secret" ||
		!result.(protocol.ValidateCredentialResponse).Valid {
		t.Fatalf("header=%q result=%#v", provider.header, result)
	}
}

func TestStreamingUsesBackpressuredEventSequence(t *testing.T) {
	binding, _ := testBinder(t)
	payload, _ := json.Marshal(protocol.GenerateRequest{
		Target: llmkit.Target{Provider: "fake", Model: "model"}, Stream: true,
		Credential: protocol.Credential{Type: "bearer", Value: []byte("test-secret")},
	})
	result, err := binding.generate(context.Background(), payload)
	if err != nil {
		t.Fatal(err)
	}
	sequence, ok := result.(server.EventSequence)
	if !ok {
		t.Fatalf("result = %T", result)
	}
	emitter := &collectingEmitter{}
	if err := sequence.Run(context.Background(), emitter); err != nil {
		t.Fatal(err)
	}
	if len(emitter.events) != 2 || emitter.events[0].Text != "hello" ||
		emitter.events[1].FinishReason != llmkit.FinishStop {
		t.Fatalf("events = %#v", emitter.events)
	}
}
