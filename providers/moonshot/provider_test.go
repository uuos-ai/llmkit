package moonshot

import (
	"context"
	"github.com/uuos-ai/llmkit"
	"net/http"
	"net/http/httptest"
	"testing"
)

type credentialFunc func(context.Context, llmkit.Target, *http.Request) error

func (f credentialFunc) Apply(c context.Context, t llmkit.Target, r *http.Request) error {
	return f(c, t, r)
}

func TestChatContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"1","choices":[{"message":{"content":"ok","reasoning_content":"think"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2,"cached_tokens":1}}`))
	}))
	defer server.Close()
	p, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.Generate(context.Background(), llmkit.GenerateCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Model: "kimi-k3", Endpoint: server.URL},
		Credential: credentialFunc(func(context.Context, llmkit.Target, *http.Request) error { return nil }),
		Request:    llmkit.GenerateRequest{Messages: []llmkit.Message{{Role: llmkit.RoleUser, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hi"}}}}},
	})
	if err != nil || response.Usage.CachedRead != 1 || len(response.Message.Parts) != 2 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}
