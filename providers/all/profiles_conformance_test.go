package all

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/conformance"
)

func TestEveryOpenAICompatibleProfileProtocolContract(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	profiles := []struct {
		id  llmkit.ProviderID
		api string
	}{
		{id: "deepseek", api: "chat"},
		{id: "dashscope", api: "chat"},
		{id: "minimax", api: "chat"},
		{id: "zhipu", api: "chat"},
		{id: "volcengine", api: "responses"},
		{id: "hunyuan", api: "chat"},
		{id: "moonshot", api: "chat"},
		{id: "tokenhub", api: "chat"},
		{id: "qianfan", api: "chat"},
		{id: "siliconflow", api: "chat"},
		{id: "azure-openai", api: "chat"},
		{id: "bedrock", api: "responses"},
		{id: "vertex-ai", api: "chat"},
	}
	for _, profile := range profiles {
		t.Run(string(profile.id), func(t *testing.T) {
			provider, ok := registry.Get(profile.id)
			if !ok {
				t.Fatal("profile is missing from the built-in registry")
			}
			generator, ok := provider.(llmkit.Generator)
			if !ok {
				t.Fatal("profile does not implement generation")
			}
			streamGenerator, ok := provider.(llmkit.StreamGenerator)
			if !ok {
				t.Fatal("profile does not implement streaming")
			}

			var calls atomic.Int32
			var embeddingCalls atomic.Int32
			var wantTools, wantStructured, wantReasoning bool
			path := "/chat/completions"
			if profile.api == "responses" {
				path = "/responses"
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") != "Bearer profile-secret" {
					http.Error(writer, "unexpected request", http.StatusBadRequest)
					return
				}
				if request.URL.Path == "/embeddings" {
					embeddingCalls.Add(1)
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write([]byte(`{"data":[{"index":1,"embedding":[0.3,0.4]},{"index":0,"embedding":[0.1,0.2]}],"usage":{"prompt_tokens":2,"total_tokens":2}}`))
					return
				}
				if request.URL.Path != path {
					http.Error(writer, "unexpected path", http.StatusBadRequest)
					return
				}
				switch calls.Add(1) {
				case 1:
					var payload map[string]any
					if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
						http.Error(writer, "malformed request", http.StatusBadRequest)
						return
					}
					toolsValid := validToolPayload(payload, profile.api)
					structuredValid := validStructuredPayload(payload, profile.api)
					if toolsValid != wantTools || structuredValid != wantStructured {
						http.Error(writer, "capability encoding mismatch", http.StatusBadRequest)
						return
					}
					writer.Header().Set("Content-Type", "application/json")
					if wantTools && profile.api == "chat" {
						_, _ = writer.Write([]byte(`{"id":"chat","choices":[{"message":{"reasoning_content":"thinking","tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Tokyo\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
					} else if wantTools {
						_, _ = writer.Write([]byte(`{"id":"response","status":"completed","output":[{"type":"message","content":[{"type":"reasoning_text","text":"thinking"}]},{"type":"function_call","call_id":"call_1","name":"weather","arguments":"{\"city\":\"Tokyo\"}"}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`))
					} else if wantReasoning && profile.api == "chat" {
						_, _ = writer.Write([]byte(`{"id":"chat","choices":[{"message":{"reasoning_content":"thinking","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
					} else if wantReasoning {
						_, _ = writer.Write([]byte(`{"id":"response","status":"completed","output":[{"type":"message","content":[{"type":"reasoning_text","text":"thinking"},{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`))
					} else if profile.api == "chat" {
						_, _ = writer.Write([]byte(`{"id":"chat","choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
					} else {
						_, _ = writer.Write([]byte(`{"id":"response","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}`))
					}
				case 2:
					writer.Header().Set("Content-Type", "text/event-stream")
					if profile.api == "chat" {
						_, _ = writer.Write([]byte("data: {\"id\":\"chat\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\n" +
							"data: {\"id\":\"chat\",\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n" +
							"data: [DONE]\n\n"))
					} else {
						_, _ = writer.Write([]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"response\"}}\n\n" +
							"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
							"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response\",\"status\":\"completed\",\"usage\":{\"input_tokens\":2,\"output_tokens\":1,\"total_tokens\":3}}}\n\n"))
					}
				case 3:
					writer.Header().Set("Retry-After", "1")
					writer.WriteHeader(http.StatusTooManyRequests)
					_, _ = writer.Write([]byte(`{"error":{"message":"private-upstream-marker","code":"rate_limit"}}`))
				case 4:
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write([]byte(`{"truncated":`))
				default:
					http.Error(writer, "unexpected extra call", http.StatusInternalServerError)
				}
			}))
			defer server.Close()

			target := llmkit.Target{Provider: profile.id, Model: "model", Endpoint: server.URL}
			capabilities, err := provider.Capabilities(context.Background(), target)
			if err != nil {
				t.Fatal(err)
			}
			modelCapabilities := capabilities.Models[target.Model].Capabilities
			wantTools = hasCapability(modelCapabilities, llmkit.CapabilityTools)
			wantStructured = hasCapability(modelCapabilities, llmkit.CapabilityStructured)
			wantReasoning = hasCapability(modelCapabilities, llmkit.CapabilityReasoning)
			request := llmkit.GenerateRequest{Messages: []llmkit.Message{{Role: llmkit.RoleUser, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hi"}}}}}
			if wantTools {
				request.Tools = []llmkit.Tool{{Name: "weather", InputSchema: []byte(`{"type":"object"}`)}}
			}
			if wantStructured {
				request.ResponseFormat = &llmkit.ResponseFormat{Name: "forecast", Schema: []byte(`{"type":"object"}`), Strict: true}
			}
			credential := credentialFunc(func(_ context.Context, _ llmkit.Target, request *http.Request) error {
				request.Header.Set("Authorization", "Bearer profile-secret")
				return nil
			})
			call := llmkit.GenerateCall{Target: target, Credential: credential, Request: request}
			response, err := generator.Generate(context.Background(), call)
			if err != nil || response.Usage.Source != llmkit.UsageReported || response.Usage.TotalTokens != 3 {
				t.Fatalf("generation response=%#v err=%v", response, err)
			}
			if wantTools {
				if toolCall(response.Message) == nil || toolCall(response.Message).Name != "weather" {
					t.Fatalf("tool response=%#v", response)
				}
			} else if textPart(response.Message, llmkit.ContentText) != "ok" {
				t.Fatalf("text response=%#v", response)
			}
			if wantReasoning && textPart(response.Message, llmkit.ContentReasoning) != "thinking" {
				t.Fatalf("reasoning response=%#v", response)
			}

			streamCall := call
			streamCall.Request.Tools = nil
			streamCall.Request.ResponseFormat = nil
			conformance.RunStreaming(t, conformance.StreamingCase{
				Name: "stream", Generator: streamGenerator, Call: streamCall, ExpectedText: "ok",
				FinishReason: llmkit.FinishStop, RequireUsage: true, RequireFinish: true,
			})
			var mappedErr error
			conformance.RunError(t, conformance.ErrorCase{
				Name: "rate_limit", Kind: llmkit.ErrorRateLimited, Retryable: true, StatusCode: http.StatusTooManyRequests,
				Invoke: func(ctx context.Context) error {
					_, mappedErr = generator.Generate(ctx, streamCall)
					return mappedErr
				},
			})
			if mappedErr == nil || strings.Contains(mappedErr.Error(), "private-upstream-marker") {
				t.Fatalf("unsafe normalized error: %v", mappedErr)
			}
			_, err = generator.Generate(context.Background(), streamCall)
			var providerErr *llmkit.ProviderError
			if !errors.As(err, &providerErr) || providerErr.Kind != llmkit.ErrorProtocol {
				t.Fatalf("malformed response error=%#v", err)
			}

			embedder, supportsEmbedding := provider.(llmkit.Embedder)
			declaresEmbedding := hasCapability(modelCapabilities, llmkit.CapabilityEmbedding)
			if supportsEmbedding != declaresEmbedding {
				t.Fatalf("embedding interface=%v declaration=%v", supportsEmbedding, declaresEmbedding)
			}
			if supportsEmbedding {
				embedding, err := embedder.Embed(context.Background(), llmkit.EmbedCall{Target: target, Credential: credential, Input: []string{"one", "two"}})
				if err != nil || len(embedding.Vectors) != 2 || embedding.Vectors[0][0] != 0.1 || embedding.Vectors[1][0] != 0.3 || embedding.Usage.Source != llmkit.UsageReported || embeddingCalls.Load() != 1 {
					t.Fatalf("embedding=%#v calls=%d err=%v", embedding, embeddingCalls.Load(), err)
				}
			}
		})
	}
}

func toolCall(message llmkit.Message) *llmkit.ToolCall {
	for _, part := range message.Parts {
		if part.Type == llmkit.ContentToolCall {
			return part.ToolCall
		}
	}
	return nil
}

func textPart(message llmkit.Message, contentType llmkit.ContentType) string {
	for _, part := range message.Parts {
		if part.Type == contentType {
			return part.Text
		}
	}
	return ""
}

func hasCapability(capabilities []llmkit.Capability, expected llmkit.Capability) bool {
	for _, capability := range capabilities {
		if capability == expected {
			return true
		}
	}
	return false
}

func validToolPayload(payload map[string]any, api string) bool {
	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) != 1 {
		return false
	}
	tool, ok := tools[0].(map[string]any)
	if !ok || tool["type"] != "function" {
		return false
	}
	if api == "responses" {
		return tool["name"] == "weather"
	}
	function, ok := tool["function"].(map[string]any)
	return ok && function["name"] == "weather"
}

func validStructuredPayload(payload map[string]any, api string) bool {
	var format map[string]any
	if api == "responses" {
		text, ok := payload["text"].(map[string]any)
		if !ok {
			return false
		}
		format, _ = text["format"].(map[string]any)
	} else {
		responseFormat, ok := payload["response_format"].(map[string]any)
		if !ok || responseFormat["type"] != "json_schema" {
			return false
		}
		format, _ = responseFormat["json_schema"].(map[string]any)
	}
	if format == nil || format["type"] != "json_schema" && api == "responses" || format["name"] != "forecast" || format["strict"] != true {
		return false
	}
	schema, ok := format["schema"].(map[string]any)
	return ok && schema["type"] == "object"
}
