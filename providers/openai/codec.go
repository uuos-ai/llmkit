package openai

import (
	"encoding/json"
	"fmt"

	"github.com/uuos-ai/llmkit"
)

func encodeChatRequest(call llmkit.GenerateCall, stream bool) (map[string]any, error) {
	messages, err := encodeChatMessages(call.Request.Messages)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"model":    string(call.Target.Model),
		"messages": messages,
	}
	if stream {
		payload["stream"] = true
		payload["stream_options"] = map[string]bool{"include_usage": true}
	}
	applyCommonOptions(payload, call.Request)
	if len(call.Request.Tools) > 0 {
		payload["tools"] = encodeTools(call.Request.Tools)
	}
	if call.Request.ResponseFormat != nil {
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   call.Request.ResponseFormat.Name,
				"schema": json.RawMessage(call.Request.ResponseFormat.Schema),
				"strict": call.Request.ResponseFormat.Strict,
			},
		}
	}
	return payload, nil
}

func encodeResponsesRequest(call llmkit.GenerateCall, stream bool) (map[string]any, error) {
	input, err := encodeResponsesInput(call.Request.Messages)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"model": string(call.Target.Model),
		"input": input,
	}
	if stream {
		payload["stream"] = true
	}
	if call.Request.MaxOutputTokens != nil {
		payload["max_output_tokens"] = *call.Request.MaxOutputTokens
	}
	if call.Request.Temperature != nil {
		payload["temperature"] = *call.Request.Temperature
	}
	if call.Request.TopP != nil {
		payload["top_p"] = *call.Request.TopP
	}
	if len(call.Request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(call.Request.Tools))
		for _, tool := range call.Request.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  json.RawMessage(tool.InputSchema),
			})
		}
		payload["tools"] = tools
	}
	if call.Request.ResponseFormat != nil {
		payload["text"] = map[string]any{"format": map[string]any{
			"type":   "json_schema",
			"name":   call.Request.ResponseFormat.Name,
			"schema": json.RawMessage(call.Request.ResponseFormat.Schema),
			"strict": call.Request.ResponseFormat.Strict,
		}}
	}
	return payload, nil
}

func encodeChatMessages(messages []llmkit.Message) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		wire := map[string]any{"role": string(message.Role)}
		var content []map[string]any
		var toolCalls []map[string]any
		for _, part := range message.Parts {
			switch part.Type {
			case llmkit.ContentText:
				content = append(content, map[string]any{"type": "text", "text": part.Text})
			case llmkit.ContentImage:
				if part.Media == nil {
					return nil, fmt.Errorf("image content is missing media")
				}
				imageURL := part.Media.URL
				if imageURL == "" && len(part.Media.Data) > 0 {
					return nil, fmt.Errorf("inline image data requires an injected media resolver")
				}
				content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageURL}})
			case llmkit.ContentToolCall:
				if part.ToolCall == nil {
					return nil, fmt.Errorf("tool call content is missing tool call")
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   part.ToolCall.ID,
					"type": "function",
					"function": map[string]any{
						"name":      part.ToolCall.Name,
						"arguments": string(part.ToolCall.Arguments),
					},
				})
			case llmkit.ContentToolResult:
				if part.ToolResult == nil {
					return nil, fmt.Errorf("tool result content is missing tool result")
				}
				wire["tool_call_id"] = part.ToolResult.CallID
				wire["content"] = flattenText(part.ToolResult.Content)
			default:
				return nil, fmt.Errorf("unsupported content type %q", part.Type)
			}
		}
		if _, exists := wire["content"]; !exists {
			if len(content) == 1 && content[0]["type"] == "text" {
				wire["content"] = content[0]["text"]
			} else if len(content) > 0 {
				wire["content"] = content
			}
		}
		if len(toolCalls) > 0 {
			wire["tool_calls"] = toolCalls
		}
		result = append(result, wire)
	}
	return result, nil
}

func encodeResponsesInput(messages []llmkit.Message) ([]map[string]any, error) {
	chatMessages, err := encodeChatMessages(messages)
	if err != nil {
		return nil, err
	}
	return chatMessages, nil
}

func encodeTools(tools []llmkit.Tool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  json.RawMessage(tool.InputSchema),
			},
		})
	}
	return result
}

func applyCommonOptions(payload map[string]any, request llmkit.GenerateRequest) {
	if request.MaxOutputTokens != nil {
		payload["max_completion_tokens"] = *request.MaxOutputTokens
	}
	if request.Temperature != nil {
		payload["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		payload["top_p"] = *request.TopP
	}
	if len(request.Stop) > 0 {
		payload["stop"] = request.Stop
	}
}

func flattenText(parts []llmkit.ContentPart) string {
	var result string
	for _, part := range parts {
		if part.Type == llmkit.ContentText {
			result += part.Text
		}
	}
	return result
}

func mapFinishReason(reason string) llmkit.FinishReason {
	switch reason {
	case "stop", "completed":
		return llmkit.FinishStop
	case "length", "max_output_tokens":
		return llmkit.FinishLength
	case "tool_calls":
		return llmkit.FinishToolCalls
	case "content_filter":
		return llmkit.FinishContentFilter
	case "":
		return llmkit.FinishUnknown
	default:
		return llmkit.FinishUnknown
	}
}

type usageDTO struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	PromptDetails    struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
	InputDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

func normalizeUsage(value *usageDTO) llmkit.Usage {
	if value == nil {
		return llmkit.Usage{Source: llmkit.UsageMissing}
	}
	input := value.InputTokens
	if input == 0 {
		input = value.PromptTokens
	}
	output := value.OutputTokens
	if output == 0 {
		output = value.CompletionTokens
	}
	total := value.TotalTokens
	if total == 0 {
		total = input + output
	}
	cached := value.InputDetails.CachedTokens
	if cached == 0 {
		cached = value.PromptDetails.CachedTokens
	}
	reasoning := value.OutputDetails.ReasoningTokens
	if reasoning == 0 {
		reasoning = value.CompletionDetails.ReasoningTokens
	}
	return llmkit.Usage{
		Source:          llmkit.UsageReported,
		InputTokens:     input,
		OutputTokens:    output,
		TotalTokens:     total,
		CachedRead:      cached,
		ReasoningTokens: reasoning,
	}
}
