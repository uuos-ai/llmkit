package all

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uuos-ai/llmkit"
)

type credentialFunc func(context.Context, llmkit.Target, *http.Request) error

func (f credentialFunc) Apply(ctx context.Context, target llmkit.Target, request *http.Request) error {
	return f(ctx, target, request)
}

func TestCloudOpenAIProfilesRequireExplicitEndpointAndUseDedicatedPaths(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		provider llmkit.ProviderID
		path     string
		body     string
	}{
		{provider: "azure-openai", path: "/chat/completions", body: `{"id":"chat","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":1}}`},
		{provider: "bedrock", path: "/responses", body: `{"id":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"total_tokens":1}}`},
		{provider: "vertex-ai", path: "/chat/completions", body: `{"id":"chat","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":1}}`},
	} {
		t.Run(string(testCase.provider), func(t *testing.T) {
			provider, _ := registry.Get(testCase.provider)
			if _, err := provider.Capabilities(context.Background(), llmkit.Target{Provider: testCase.provider, Model: "model"}); err == nil {
				t.Fatal("profile accepted an implicit placeholder endpoint")
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != testCase.path || request.Header.Get("Authorization") != "Bearer workload" {
					t.Fatalf("path=%q authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
				}
				_, _ = writer.Write([]byte(testCase.body))
			}))
			defer server.Close()
			generator, ok := provider.(llmkit.Generator)
			if !ok {
				t.Fatal("profile does not implement generation")
			}
			response, err := generator.Generate(context.Background(), llmkit.GenerateCall{Target: llmkit.Target{Provider: testCase.provider, Model: "model", Endpoint: server.URL}, Credential: credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
				request.Header.Set("Authorization", "Bearer workload")
				return nil
			}), Request: llmkit.GenerateRequest{Messages: []llmkit.Message{{Role: llmkit.RoleUser, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hi"}}}}}})
			if err != nil || len(response.Message.Parts) != 1 || response.Message.Parts[0].Text != "ok" {
				t.Fatalf("response=%#v err=%v", response, err)
			}
		})
	}
}
