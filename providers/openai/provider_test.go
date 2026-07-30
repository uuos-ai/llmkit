package openai

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
	"github.com/uuos-ai/llmkit/transport"
)

type credentialFunc func(context.Context, llmkit.Target, *http.Request) error

func (f credentialFunc) Apply(ctx context.Context, target llmkit.Target, request *http.Request) error {
	return f(ctx, target, request)
}

func TestValidateCredentialUsesModelsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatal("credential header was not applied")
		}
		_, _ = writer.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	provider := newTestProvider(t)
	err := provider.ValidateCredential(context.Background(), llmkit.CredentialCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Model: "model", Endpoint: server.URL},
		Credential: bearerCredential(t),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateCredentialNormalizesAuthenticationFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"code":"invalid_api_key"}}`))
	}))
	defer server.Close()
	provider := newTestProvider(t)
	conformance.RunError(t, conformance.ErrorCase{
		Name: "credential_authentication",
		Kind: llmkit.ErrorAuthentication, StatusCode: http.StatusUnauthorized,
		Invoke: func(ctx context.Context) error {
			return provider.ValidateCredential(ctx, llmkit.CredentialCall{
				Target:     llmkit.Target{Provider: DefaultProviderID, Model: "model", Endpoint: server.URL},
				Credential: bearerCredential(t),
			})
		},
	})
}

func TestListModelsNormalizesPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" || request.URL.Query().Get("after") != "previous" ||
			request.URL.Query().Get("limit") != "2" {
			t.Fatalf("request URL = %s", request.URL.String())
		}
		_, _ = writer.Write([]byte(`{
			"data":[{"id":"gpt-a","owned_by":"openai"},{"id":"gpt-b"}],
			"has_more":true,"last_id":"gpt-b"
		}`))
	}))
	defer server.Close()
	page, err := newTestProvider(t).ListModels(context.Background(), llmkit.ListModelsCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Endpoint: server.URL},
		Credential: bearerCredential(t), Cursor: "previous", Limit: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Models) != 2 || page.Models[0].ID != "gpt-a" ||
		page.Models[0].Owner != "openai" || page.NextCursor != "gpt-b" {
		t.Fatalf("page = %#v", page)
	}
}

func bearerCredential(t *testing.T) llmkit.CredentialHandle {
	t.Helper()
	return credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer test-secret")
		return nil
	})
}

func baseCall(endpoint string, api API, credential llmkit.CredentialHandle) llmkit.GenerateCall {
	return llmkit.GenerateCall{
		OperationID: "operation-1",
		Target: llmkit.Target{
			Provider: DefaultProviderID,
			Model:    "gpt-test",
			Endpoint: endpoint,
		},
		Credential: credential,
		Request: llmkit.GenerateRequest{
			Messages: []llmkit.Message{{
				Role: llmkit.RoleUser,
				Parts: []llmkit.ContentPart{{
					Type: llmkit.ContentText,
					Text: "hello",
				}},
			}},
			ProviderOptions: map[string][]byte{OptionAPI: []byte(api)},
		},
	}
}

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	provider, err := New(Config{Transport: transport.New(transport.Config{})})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestChatGenerationContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Fatal("missing credential")
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "gpt-test" {
			t.Fatalf("model = %#v", payload["model"])
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"chatcmpl_1",
			"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()

	conformance.RunGeneration(t, conformance.GenerationCase{
		Name:         "chat",
		Generator:    newTestProvider(t),
		Call:         baseCall(server.URL, APIChat, bearerCredential(t)),
		ExpectedText: "hello",
		FinishReason: llmkit.FinishStop,
		UsageSource:  llmkit.UsageReported,
	})
}

func TestResponsesGenerationContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/responses" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"resp_1",
			"status":"completed",
			"output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}],
			"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()

	conformance.RunGeneration(t, conformance.GenerationCase{
		Name:         "responses",
		Generator:    newTestProvider(t),
		Call:         baseCall(server.URL, APIResponses, bearerCredential(t)),
		ExpectedText: "hello",
		FinishReason: llmkit.FinishStop,
		UsageSource:  llmkit.UsageReported,
	})
}

func TestToolsStructuredOutputAndReasoningRoundTrip(t *testing.T) {
	for _, api := range []API{APIChat, APIResponses} {
		t.Run(string(api), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				var payload map[string]any
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				tools, ok := payload["tools"].([]any)
				if !ok || len(tools) != 1 {
					t.Fatalf("tools=%#v", payload["tools"])
				}
				if api == APIChat {
					format, ok := payload["response_format"].(map[string]any)
					if !ok || format["type"] != "json_schema" {
						t.Fatalf("response_format=%#v", payload["response_format"])
					}
					_, _ = writer.Write([]byte(`{"id":"chat","choices":[{"message":{"reasoning_content":"thinking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Tokyo\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3,"completion_tokens_details":{"reasoning_tokens":1}}}`))
					return
				}
				text, ok := payload["text"].(map[string]any)
				if !ok || text["format"] == nil {
					t.Fatalf("text format=%#v", payload["text"])
				}
				_, _ = writer.Write([]byte(`{"id":"response","status":"completed","output":[{"type":"message","content":[{"type":"reasoning_text","text":"thinking"}]},{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Tokyo\"}"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3,"output_tokens_details":{"reasoning_tokens":1}}}`))
			}))
			defer server.Close()
			call := baseCall(server.URL, api, bearerCredential(t))
			call.Request.Tools = []llmkit.Tool{{Name: "weather", InputSchema: []byte(`{"type":"object"}`)}}
			call.Request.ResponseFormat = &llmkit.ResponseFormat{Name: "forecast", Schema: []byte(`{"type":"object"}`), Strict: true}
			response, err := newTestProvider(t).Generate(context.Background(), call)
			if err != nil {
				t.Fatal(err)
			}
			tool, reasoning := richParts(response.Message)
			if tool == nil || tool.Name != "weather" || string(tool.Arguments) != `{"city":"Tokyo"}` || reasoning != "thinking" || response.Usage.ReasoningTokens != 1 {
				t.Fatalf("response=%#v", response)
			}
		})
	}
}

func richParts(message llmkit.Message) (*llmkit.ToolCall, string) {
	var tool *llmkit.ToolCall
	var reasoning string
	for _, part := range message.Parts {
		switch part.Type {
		case llmkit.ContentToolCall:
			tool = part.ToolCall
		case llmkit.ContentReasoning:
			reasoning += part.Text
		}
	}
	return tool, reasoning
}

func TestChatStreamingContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept") != "text/event-stream" {
			t.Fatal("missing SSE accept header")
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n" +
				"data: {\"id\":\"chatcmpl_1\",\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"id\":\"chatcmpl_1\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer server.Close()

	conformance.RunStreaming(t, conformance.StreamingCase{
		Name:          "chat",
		Generator:     newTestProvider(t),
		Call:          baseCall(server.URL, APIChat, bearerCredential(t)),
		ExpectedText:  "hello",
		FinishReason:  llmkit.FinishStop,
		RequireUsage:  true,
		RequireFinish: true,
	})
}

func TestResponsesStreamingContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\n" +
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n",
		))
	}))
	defer server.Close()

	conformance.RunStreaming(t, conformance.StreamingCase{
		Name:          "responses",
		Generator:     newTestProvider(t),
		Call:          baseCall(server.URL, APIResponses, bearerCredential(t)),
		ExpectedText:  "hello",
		FinishReason:  llmkit.FinishStop,
		RequireUsage:  true,
		RequireFinish: true,
	})
}

func TestErrorContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "2")
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = writer.Write([]byte(`{"error":{"message":"do not expose this","type":"rate_limit","code":"rate_limit"}}`))
	}))
	defer server.Close()

	provider := newTestProvider(t)
	call := baseCall(server.URL, APIChat, bearerCredential(t))
	conformance.RunError(t, conformance.ErrorCase{
		Name: "rate limit",
		Invoke: func(ctx context.Context) error {
			_, err := provider.Generate(ctx, call)
			return err
		},
		Kind:       llmkit.ErrorRateLimit,
		Retryable:  true,
		StatusCode: http.StatusTooManyRequests,
	})
	_, err := provider.Generate(context.Background(), call)
	if strings.Contains(err.Error(), "do not expose this") {
		t.Fatalf("error leaked upstream body: %q", err)
	}
}

func TestChatStreamRejectsTruncation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"partial"}}]}` + "\n\n"))
	}))
	defer server.Close()

	provider := newTestProvider(t)
	stream, err := provider.Stream(context.Background(), baseCall(server.URL, APIChat, bearerCredential(t)))
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
		t.Fatalf("error = %#v, want malformed response", err)
	}
}

func TestCapabilitiesRejectMismatchedTarget(t *testing.T) {
	_, err := newTestProvider(t).Capabilities(context.Background(), llmkit.Target{
		Provider: "other",
		Model:    "model",
	})
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != llmkit.ErrorInvalidRequest {
		t.Fatalf("error = %#v", err)
	}
}

func TestRejectsUnknownProviderOption(t *testing.T) {
	call := baseCall("https://example.test", APIChat, bearerCredential(t))
	call.Request.ProviderOptions["openai.unknown"] = []byte("true")
	_, err := newTestProvider(t).Generate(context.Background(), call)
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != llmkit.ErrorInvalidRequest {
		t.Fatalf("error = %#v", err)
	}
}

func TestResponsesStreamReturnsStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
				"data: {\"type\":\"error\",\"error\":{\"code\":\"server_error\",\"message\":\"sensitive\"}}\n\n",
		))
	}))
	defer server.Close()

	stream, err := newTestProvider(t).Stream(
		context.Background(),
		baseCall(server.URL, APIResponses, bearerCredential(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	_, _ = stream.Recv()
	_, err = stream.Recv()
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Phase != llmkit.PhaseStream {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("stream error leaked body: %q", err)
	}
}

func TestEmbeddings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/embeddings" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{
			"id":"emb_1",
			"data":[
				{"index":1,"embedding":[0.3,0.4]},
				{"index":0,"embedding":[0.1,0.2]}
			],
			"usage":{"prompt_tokens":2,"total_tokens":2}
		}`))
	}))
	defer server.Close()

	provider := newTestProvider(t)
	response, err := provider.Embed(context.Background(), llmkit.EmbedCall{
		Target: llmkit.Target{
			Provider: DefaultProviderID,
			Model:    "text-embedding-test",
			Endpoint: server.URL,
		},
		Credential: bearerCredential(t),
		Input:      []string{"one", "two"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Vectors) != 2 || response.Vectors[0][0] != 0.1 || response.Vectors[1][0] != 0.3 {
		t.Fatalf("unexpected vectors: %#v", response.Vectors)
	}
}

func TestModerationNormalizesCategories(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/moderations" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"id":"mod_1","results":[{"flagged":true,"categories":{"violence":true,"hate":false},"category_scores":{"violence":0.9,"hate":0.1}}]}`))
	}))
	defer server.Close()
	response, err := newTestProvider(t).Moderate(context.Background(), llmkit.ModerateCall{Target: llmkit.Target{Provider: DefaultProviderID, Model: "omni-moderation-latest", Endpoint: server.URL}, Credential: bearerCredential(t), Content: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "content"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Flagged || response.ProviderRequestID != "mod_1" || len(response.Categories) != 2 || response.Categories[0].Name != "hate" || response.Usage.Source != llmkit.UsageUnavailable {
		t.Fatalf("response=%#v", response)
	}
	conformance.RunModeration(t, conformance.ModerationCase{Name: "moderation_conformance", Moderator: newTestProvider(t), Call: llmkit.ModerateCall{Target: llmkit.Target{Provider: DefaultProviderID, Model: "omni-moderation-latest", Endpoint: server.URL}, Credential: bearerCredential(t), Content: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "content"}}}, Flagged: true, MinCategory: 2, UsageSource: llmkit.UsageUnavailable})
}
