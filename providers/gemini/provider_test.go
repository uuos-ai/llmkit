package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/conformance"
)

type credentialFunc func(context.Context, llmkit.Target, *http.Request) error

func (f credentialFunc) Apply(ctx context.Context, target llmkit.Target, request *http.Request) error {
	return f(ctx, target, request)
}

func testCredential() llmkit.CredentialHandle {
	return credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
		request.Header.Set("x-goog-api-key", "test-secret")
		return nil
	})
}

func testCall(endpoint string) llmkit.GenerateCall {
	return llmkit.GenerateCall{
		Target: llmkit.Target{
			Provider: DefaultProviderID, Model: "gemini-test", Endpoint: endpoint,
		},
		Credential: testCredential(),
		Request: llmkit.GenerateRequest{Messages: []llmkit.Message{
			{Role: llmkit.RoleSystem, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "system"}}},
			{Role: llmkit.RoleUser, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hello"}}},
		}},
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

func TestGenerationContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/gemini-test:generateContent" ||
			request.Header.Get("x-goog-api-key") != "test-secret" {
			t.Fatalf("unexpected request: path=%q headers=%v", request.URL.Path, request.Header)
		}
		_, _ = writer.Write([]byte(`{
			"responseId":"resp_1",
			"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],
			"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}
		}`))
	}))
	defer server.Close()

	conformance.RunGeneration(t, conformance.GenerationCase{
		Name: "gemini", Generator: testProvider(t), Call: testCall(server.URL),
		ExpectedText: "hello", FinishReason: llmkit.FinishStop,
		UsageSource: llmkit.UsageReported,
	})
}

func TestStreamingContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("alt") != "sse" {
			t.Fatalf("alt = %q", request.URL.Query().Get("alt"))
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"responseId\":\"resp_1\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hel\"}]}}]}\n\n" +
				"data: {\"responseId\":\"resp_1\",\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"lo\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2,\"candidatesTokenCount\":1,\"totalTokenCount\":3}}\n\n",
		))
	}))
	defer server.Close()

	conformance.RunStreaming(t, conformance.StreamingCase{
		Name: "gemini", Generator: testProvider(t), Call: testCall(server.URL),
		ExpectedText: "hello", FinishReason: llmkit.FinishStop,
		RequireUsage: true, RequireFinish: true,
	})
}

func TestEmbeddingContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1beta/models/gemini-embedding-001:batchEmbedContents" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		var body struct {
			Requests []struct {
				Model string `json:"model"`
			} `json:"requests"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Requests) != 2 || body.Requests[0].Model != "models/gemini-embedding-001" {
			t.Fatalf("body = %#v", body)
		}
		_, _ = writer.Write([]byte(`{
			"embeddings":[{"values":[1,2]},{"values":[3,4]}],
			"usageMetadata":{"promptTokenCount":2,"totalTokenCount":2}
		}`))
	}))
	defer server.Close()

	response, err := testProvider(t).Embed(context.Background(), llmkit.EmbedCall{
		Target: llmkit.Target{
			Provider: DefaultProviderID, Model: "gemini-embedding-001", Endpoint: server.URL,
		},
		Credential: testCredential(), Input: []string{"a", "b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Vectors) != 2 || len(response.Vectors[0]) != 2 ||
		response.Usage.Source != llmkit.UsageReported {
		t.Fatalf("response = %#v", response)
	}
}

func TestErrorContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "1")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"status":"RESOURCE_EXHAUSTED","message":"sensitive"}}`))
	}))
	defer server.Close()

	provider := testProvider(t)
	call := testCall(server.URL)
	conformance.RunError(t, conformance.ErrorCase{
		Name: "rate limit",
		Invoke: func(ctx context.Context) error {
			_, err := provider.Generate(ctx, call)
			return err
		},
		Kind: llmkit.ErrorRateLimit, Retryable: true,
		StatusCode: http.StatusTooManyRequests,
	})
	_, err := provider.Generate(context.Background(), call)
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("error leaked upstream body: %q", err)
	}
}

func TestStreamRequiresFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(`data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}` + "\n\n"))
	}))
	defer server.Close()

	stream, err := testProvider(t).Stream(context.Background(), testCall(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	for {
		_, err = stream.Recv()
		if err != nil {
			break
		}
	}
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != llmkit.ErrorMalformedResponse {
		t.Fatalf("error = %#v", err)
	}
}

func TestEncodeToolsAndStructuredOutput(t *testing.T) {
	call := testCall("https://example.invalid")
	call.Request.Messages = append(call.Request.Messages, llmkit.Message{
		Role: llmkit.RoleTool,
		Parts: []llmkit.ContentPart{{
			Type: llmkit.ContentToolResult,
			ToolResult: &llmkit.ToolResult{
				CallID: "call_1", Name: "weather",
				Content: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "sunny"}},
			},
		}},
	})
	call.Request.Tools = []llmkit.Tool{{
		Name: "weather", InputSchema: []byte(`{"type":"object"}`),
	}}
	call.Request.ResponseFormat = &llmkit.ResponseFormat{
		Name: "forecast", Schema: []byte(`{"type":"object"}`), Strict: true,
	}
	payload, err := encodeRequest(call)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"parametersJsonSchema":{"type":"object"}`) ||
		!strings.Contains(string(encoded), `"responseJsonSchema":{"type":"object"}`) ||
		!strings.Contains(string(encoded), `"role":"function"`) ||
		!strings.Contains(string(encoded), `"name":"weather"`) {
		t.Fatalf("payload = %s", encoded)
	}
}
