package qianfan

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

func TestChatProfile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Fatalf("path=%q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"id":"chat","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"total_tokens":1}}`))
	}))
	defer server.Close()
	provider, err := New(Config{Endpoint: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	response, err := provider.Generate(context.Background(), llmkit.GenerateCall{Target: llmkit.Target{Provider: DefaultProviderID, Model: "ernie", Endpoint: server.URL}, Credential: credentialFunc(func(context.Context, llmkit.Target, *http.Request) error { return nil }), Request: llmkit.GenerateRequest{Messages: []llmkit.Message{{Role: llmkit.RoleUser, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hi"}}}}}})
	if err != nil || len(response.Message.Parts) != 1 || response.Message.Parts[0].Text != "ok" {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}
