package anthropic

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

func TestValidateCredentialUsesModelsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/models" || request.URL.Query().Get("limit") != "1" {
			t.Fatalf("request = %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("x-api-key") != "test-secret" || request.Header.Get("anthropic-version") == "" {
			t.Fatal("required Anthropic headers were not applied")
		}
		_, _ = writer.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	provider, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = provider.ValidateCredential(context.Background(), llmkit.CredentialCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Model: "claude-test", Endpoint: server.URL},
		Credential: testCredential(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestListModelsNormalizesPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/models" || request.URL.Query().Get("after_id") != "previous" {
			t.Fatalf("request URL = %s", request.URL.String())
		}
		_, _ = writer.Write([]byte(`{
			"data":[{"id":"claude-a","display_name":"Claude A"}],
			"has_more":true,"last_id":"claude-a"
		}`))
	}))
	defer server.Close()
	page, err := testProvider(t).ListModels(context.Background(), llmkit.ListModelsCall{
		Target:     llmkit.Target{Provider: DefaultProviderID, Endpoint: server.URL},
		Credential: testCredential(), Cursor: "previous", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Models) != 1 || page.Models[0].DisplayName != "Claude A" || page.NextCursor != "claude-a" {
		t.Fatalf("page = %#v", page)
	}
}

func testCredential() llmkit.CredentialHandle {
	return credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
		request.Header.Set("x-api-key", "test-secret")
		return nil
	})
}

func testCall(endpoint string) llmkit.GenerateCall {
	return llmkit.GenerateCall{
		Target: llmkit.Target{
			Provider: DefaultProviderID, Model: "claude-test", Endpoint: endpoint,
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
		if request.URL.Path != "/v1/messages" ||
			request.Header.Get("anthropic-version") != DefaultVersion ||
			request.Header.Get("x-api-key") != "test-secret" {
			t.Fatalf("unexpected request: path=%q headers=%v", request.URL.Path, request.Header)
		}
		_, _ = writer.Write([]byte(`{
			"id":"msg_1",
			"content":[{"type":"text","text":"hello"}],
			"stop_reason":"end_turn",
			"usage":{"input_tokens":2,"output_tokens":1,"cache_read_input_tokens":1}
		}`))
	}))
	defer server.Close()

	conformance.RunGeneration(t, conformance.GenerationCase{
		Name: "anthropic", Generator: testProvider(t), Call: testCall(server.URL),
		ExpectedText: "hello", FinishReason: llmkit.FinishStop,
		UsageSource: llmkit.UsageReported,
	})
}

func TestToolsStructuredOutputAndReasoningRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		tools, toolsOK := payload["tools"].([]any)
		outputConfig, formatOK := payload["output_config"].(map[string]any)
		if !toolsOK || len(tools) != 1 || !formatOK || outputConfig["format"] == nil {
			t.Fatalf("payload=%#v", payload)
		}
		_, _ = writer.Write([]byte(`{
			"id":"msg_rich",
			"content":[
				{"type":"thinking","thinking":"thinking"},
				{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Tokyo"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":2,"output_tokens":1}
		}`))
	}))
	defer server.Close()
	call := testCall(server.URL)
	call.Request.Tools = []llmkit.Tool{{Name: "weather", InputSchema: []byte(`{"type":"object"}`)}}
	call.Request.ResponseFormat = &llmkit.ResponseFormat{Name: "forecast", Schema: []byte(`{"type":"object"}`), Strict: true}
	response, err := testProvider(t).Generate(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	tool, reasoning := richParts(response.Message)
	if tool == nil || tool.ID != "toolu_1" || tool.Name != "weather" || string(tool.Arguments) != `{"city":"Tokyo"}` || reasoning != "thinking" || response.FinishReason != llmkit.FinishToolCalls {
		t.Fatalf("response=%#v", response)
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

func TestStreamingContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":2}}}\n\n" +
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hel\"}}\n\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
		))
	}))
	defer server.Close()

	conformance.RunStreaming(t, conformance.StreamingCase{
		Name: "anthropic", Generator: testProvider(t), Call: testCall(server.URL),
		ExpectedText: "hello", FinishReason: llmkit.FinishStop,
		RequireUsage: true, RequireFinish: true,
	})
}

func TestErrorContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Retry-After", "1")
		writer.WriteHeader(529)
		_, _ = writer.Write([]byte(`{"error":{"type":"overloaded_error","message":"sensitive"}}`))
	}))
	defer server.Close()

	provider := testProvider(t)
	call := testCall(server.URL)
	conformance.RunError(t, conformance.ErrorCase{
		Name: "overloaded",
		Invoke: func(ctx context.Context) error {
			_, err := provider.Generate(ctx, call)
			return err
		},
		Kind: llmkit.ErrorOverloaded, Retryable: true, StatusCode: 529,
	})
	_, err := provider.Generate(context.Background(), call)
	if strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("error leaked upstream body: %q", err)
	}
}

func TestStreamRequiresMessageStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = writer.Write([]byte(
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"usage\":{\"input_tokens\":2}}}\n\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n",
		))
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

func TestEncodeRequestMapsToolRoleAndStructuredOutput(t *testing.T) {
	call := testCall("https://example.invalid")
	call.Request.Messages = append(call.Request.Messages,
		llmkit.Message{
			Role: llmkit.RoleAssistant,
			Parts: []llmkit.ContentPart{{
				Type: llmkit.ContentToolCall,
				ToolCall: &llmkit.ToolCall{
					ID: "toolu_1", Name: "weather", Arguments: []byte(`{"city":"Tokyo"}`),
				},
			}},
		},
		llmkit.Message{
			Role: llmkit.RoleTool,
			Parts: []llmkit.ContentPart{{
				Type: llmkit.ContentToolResult,
				ToolResult: &llmkit.ToolResult{
					CallID:  "toolu_1",
					Content: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "sunny"}},
				},
			}},
		},
	)
	call.Request.Tools = []llmkit.Tool{{
		Name: "weather", InputSchema: []byte(`{"type":"object"}`),
	}}
	call.Request.ResponseFormat = &llmkit.ResponseFormat{
		Name: "forecast", Schema: []byte(`{"type":"object"}`), Strict: true,
	}

	payload, err := encodeRequest(call, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type      string `json:"type"`
				ToolUseID string `json:"tool_use_id"`
			} `json:"content"`
		} `json:"messages"`
		OutputConfig struct {
			Format struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			} `json:"format"`
		} `json:"output_config"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if got := wire.Messages[len(wire.Messages)-1].Role; got != "user" {
		t.Fatalf("tool result role = %q, want user", got)
	}
	result := wire.Messages[len(wire.Messages)-1].Content[0]
	if result.Type != "tool_result" || result.ToolUseID != "toolu_1" {
		t.Fatalf("tool result = %#v", result)
	}
	if wire.OutputConfig.Format.Type != "json_schema" ||
		string(wire.OutputConfig.Format.Schema) != `{"type":"object"}` {
		t.Fatalf("output format = %#v", wire.OutputConfig.Format)
	}
}
