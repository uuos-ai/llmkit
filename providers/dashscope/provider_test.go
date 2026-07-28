package dashscope

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/conformance"
)

type credentialFunc func(context.Context, llmkit.Target, *http.Request) error

func (f credentialFunc) Apply(ctx context.Context, target llmkit.Target, request *http.Request) error {
	return f(ctx, target, request)
}

func credential() llmkit.CredentialHandle {
	return credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer test-secret")
		return nil
	})
}

func call(endpoint string) llmkit.GenerateCall {
	return llmkit.GenerateCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Model: "qwen-plus", Endpoint: endpoint},
		Credential: credential(),
		Request: llmkit.GenerateRequest{Messages: []llmkit.Message{{
			Role:  llmkit.RoleUser,
			Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hello"}},
		}}},
	}
}

func provider(t *testing.T) *Provider {
	t.Helper()
	result, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestGenerationUsesCompatibleBaseURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{
			"id":"chat_1","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()

	conformance.RunGeneration(t, conformance.GenerationCase{
		Name: "dashscope", Generator: provider(t), Call: call(server.URL),
		ExpectedText: "hello", FinishReason: llmkit.FinishStop,
		UsageSource: llmkit.UsageReported,
	})
}

func TestEmbeddingUsesCompatibleEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/embeddings" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{
			"id":"emb_1","data":[{"index":0,"embedding":[1,2]}],
			"usage":{"prompt_tokens":1,"total_tokens":1}
		}`))
	}))
	defer server.Close()

	response, err := provider(t).Embed(context.Background(), llmkit.EmbedCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Model: "text-embedding-v4", Endpoint: server.URL},
		Credential: credential(), Input: []string{"hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Vectors) != 1 || len(response.Vectors[0]) != 2 {
		t.Fatalf("response = %#v", response)
	}
}
