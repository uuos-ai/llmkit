package siliconflow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/conformance"
	"github.com/uuos-ai/llmkit/transport"
)

type credentialFunc func(context.Context, llmkit.Target, *http.Request) error

func (f credentialFunc) Apply(ctx context.Context, target llmkit.Target, request *http.Request) error {
	return f(ctx, target, request)
}

func TestRerankNormalizesResultsAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/rerank" || request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("request=%s authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`{"id":"rank_1","results":[{"index":1,"relevance_score":0.9,"document":{"text":"b"}}],"meta":{"tokens":{"input_tokens":7}}}`))
	}))
	defer server.Close()
	provider, err := New(Config{Endpoint: server.URL, Transport: transport.New(transport.Config{})})
	if err != nil {
		t.Fatal(err)
	}
	top := 1
	response, err := provider.Rerank(context.Background(), llmkit.RerankCall{Target: llmkit.Target{Provider: DefaultProviderID, Model: "BAAI/bge-reranker-v2-m3"}, Credential: credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer secret")
		return nil
	}), Query: "q", Documents: []string{"a", "b"}, TopN: &top})
	if err != nil {
		t.Fatal(err)
	}
	if response.ProviderRequestID != "rank_1" || len(response.Results) != 1 || response.Results[0].Index != 1 || response.Results[0].Document != "b" || response.Usage.InputTokens != 7 {
		t.Fatalf("response=%#v", response)
	}
	conformance.RunRerank(t, conformance.RerankCase{Name: "rerank_conformance", Reranker: provider, Call: llmkit.RerankCall{Target: llmkit.Target{Provider: DefaultProviderID, Model: "BAAI/bge-reranker-v2-m3"}, Credential: credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer secret")
		return nil
	}), Query: "q", Documents: []string{"a", "b"}, TopN: &top}, ExpectedCount: 1, UsageSource: llmkit.UsageReported})
}
