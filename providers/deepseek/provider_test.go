package deepseek

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

func testCall(endpoint string) llmkit.GenerateCall {
	return llmkit.GenerateCall{
		Target: llmkit.Target{Provider: DefaultProviderID, Model: "deepseek-v4-flash", Endpoint: endpoint},
		Credential: credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
			request.Header.Set("Authorization", "Bearer test-secret")
			return nil
		}),
		Request: llmkit.GenerateRequest{Messages: []llmkit.Message{{
			Role:  llmkit.RoleUser,
			Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hello"}},
		}}},
	}
}

func testProvider(t *testing.T) *Provider {
	t.Helper()
	provider, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestGenerationContractAndNativePath(t *testing.T) {
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
		Name: "deepseek", Generator: testProvider(t), Call: testCall(server.URL),
		ExpectedText: "hello", FinishReason: llmkit.FinishStop,
		UsageSource: llmkit.UsageReported,
	})
}

func TestStreamingContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"id\":\"chat_1\",\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n" +
				"data: {\"id\":\"chat_1\",\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	conformance.RunStreaming(t, conformance.StreamingCase{
		Name: "deepseek", Generator: testProvider(t), Call: testCall(server.URL),
		ExpectedText: "hello", FinishReason: llmkit.FinishStop,
		RequireUsage: true, RequireFinish: true,
	})
}

func TestDoesNotClaimEmbedding(t *testing.T) {
	provider := testProvider(t)
	capabilities, err := provider.Capabilities(context.Background(), llmkit.Target{
		Provider: DefaultProviderID, Model: "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.Supports("deepseek-v4-flash", llmkit.CapabilityEmbedding) {
		t.Fatal("DeepSeek adapter unexpectedly claims embedding")
	}
	if _, ok := any(provider).(llmkit.Embedder); ok {
		t.Fatal("DeepSeek adapter unexpectedly implements Embedder")
	}
}
