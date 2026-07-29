package hunyuan

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

func TestEmbeddingPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embeddings" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,2]}],"usage":{"prompt_tokens":1,"total_tokens":1}}`))
	}))
	defer server.Close()
	p, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.Embed(context.Background(), llmkit.EmbedCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Model: "hunyuan-embedding", Endpoint: server.URL},
		Credential: credentialFunc(func(context.Context, llmkit.Target, *http.Request) error { return nil }), Input: []string{"hi"},
	})
	if err != nil || len(response.Vectors) != 1 {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}
