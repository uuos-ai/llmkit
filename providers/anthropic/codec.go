package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/uuos-ai/llmkit"
)

func encodeRequest(call llmkit.GenerateCall, stream bool) (map[string]any, error) {
	var system []map[string]any
	var messages []map[string]any
	for _, message := range call.Request.Messages {
		blocks, err := encodeBlocks(message.Parts)
		if err != nil {
			return nil, err
		}
		if message.Role == llmkit.RoleSystem {
			system = append(system, blocks...)
			continue
		}
		role := message.Role
		// Anthropic Messages only accepts user and assistant turns. Tool
		// results are content blocks inside a user turn.
		if role == llmkit.RoleTool {
			role = llmkit.RoleUser
		}
		if role != llmkit.RoleUser && role != llmkit.RoleAssistant {
			return nil, fmt.Errorf("unsupported Anthropic message role %q", message.Role)
		}
		messages = append(messages, map[string]any{
			"role":    string(role),
			"content": blocks,
		})
	}
	maxTokens := int64(4096)
	if call.Request.MaxOutputTokens != nil {
		maxTokens = *call.Request.MaxOutputTokens
	}
	payload := map[string]any{
		"model":      string(call.Target.Model),
		"messages":   messages,
		"max_tokens": maxTokens,
	}
	if len(system) > 0 {
		payload["system"] = system
	}
	if stream {
		payload["stream"] = true
	}
	if call.Request.Temperature != nil {
		payload["temperature"] = *call.Request.Temperature
	}
	if call.Request.TopP != nil {
		payload["top_p"] = *call.Request.TopP
	}
	if len(call.Request.Stop) > 0 {
		payload["stop_sequences"] = call.Request.Stop
	}
	if len(call.Request.Tools) > 0 {
		tools := make([]map[string]any, 0, len(call.Request.Tools))
		for _, tool := range call.Request.Tools {
			tools = append(tools, map[string]any{
				"name":         tool.Name,
				"description":  tool.Description,
				"input_schema": json.RawMessage(tool.InputSchema),
			})
		}
		payload["tools"] = tools
	}
	if call.Request.ResponseFormat != nil {
		payload["output_config"] = map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"schema": json.RawMessage(call.Request.ResponseFormat.Schema),
			},
		}
	}
	return payload, nil
}

func encodeBlocks(parts []llmkit.ContentPart) ([]map[string]any, error) {
	blocks := make([]map[string]any, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case llmkit.ContentText:
			blocks = append(blocks, map[string]any{"type": "text", "text": part.Text})
		case llmkit.ContentImage:
			if part.Media == nil || len(part.Media.Data) == 0 {
				return nil, fmt.Errorf("Anthropic image input requires inline media data")
			}
			blocks = append(blocks, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": part.Media.MediaType,
					"data":       part.Media.Data,
				},
			})
		case llmkit.ContentToolCall:
			if part.ToolCall == nil {
				return nil, fmt.Errorf("tool call content is missing tool call")
			}
			blocks = append(blocks, map[string]any{
				"type":  "tool_use",
				"id":    part.ToolCall.ID,
				"name":  part.ToolCall.Name,
				"input": json.RawMessage(part.ToolCall.Arguments),
			})
		case llmkit.ContentToolResult:
			if part.ToolResult == nil {
				return nil, fmt.Errorf("tool result content is missing tool result")
			}
			blocks = append(blocks, map[string]any{
				"type":        "tool_result",
				"tool_use_id": part.ToolResult.CallID,
				"content":     flattenText(part.ToolResult.Content),
				"is_error":    part.ToolResult.IsError,
			})
		default:
			return nil, fmt.Errorf("unsupported Anthropic content type %q", part.Type)
		}
	}
	return blocks, nil
}

func flattenText(parts []llmkit.ContentPart) string {
	var text string
	for _, part := range parts {
		if part.Type == llmkit.ContentText {
			text += part.Text
		}
	}
	return text
}

type usageDTO struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

func normalizeUsage(usage usageDTO) llmkit.Usage {
	return llmkit.Usage{
		Source:       llmkit.UsageReported,
		InputTokens:  usage.InputTokens,
		OutputTokens: usage.OutputTokens,
		TotalTokens:  usage.InputTokens + usage.OutputTokens,
		CachedRead:   usage.CacheReadInputTokens,
		CachedWrite:  usage.CacheCreationInputTokens,
	}
}

func mapStopReason(reason string) llmkit.FinishReason {
	switch reason {
	case "end_turn", "stop_sequence":
		return llmkit.FinishStop
	case "max_tokens", "model_context_window_exceeded":
		return llmkit.FinishLength
	case "tool_use":
		return llmkit.FinishToolCalls
	case "refusal":
		return llmkit.FinishContentFilter
	default:
		return llmkit.FinishUnknown
	}
}

type messageResponse struct {
	ID         string `json:"id"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		ID       string          `json:"id"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
	} `json:"content"`
	Usage usageDTO `json:"usage"`
}

func decodeResponse(target llmkit.Target, wire messageResponse) (llmkit.Response, error) {
	var parts []llmkit.ContentPart
	for _, block := range wire.Content {
		switch block.Type {
		case "text":
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentText, Text: block.Text})
		case "thinking":
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentReasoning, Text: block.Thinking})
		case "tool_use":
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentToolCall, ToolCall: &llmkit.ToolCall{
				ID: block.ID, Name: block.Name, Arguments: append([]byte(nil), block.Input...),
			}})
		}
	}
	if len(parts) == 0 {
		return llmkit.Response{}, &llmkit.ProviderError{
			Provider: target.Provider, Model: target.Model,
			Kind: llmkit.ErrorMalformedResponse, Phase: llmkit.PhaseDecode,
			SafeMessage: "provider response contained no content", RequestID: wire.ID,
		}
	}
	return llmkit.Response{
		ProviderRequestID: wire.ID,
		Message:           llmkit.Message{Role: llmkit.RoleAssistant, Parts: parts},
		Usage:             normalizeUsage(wire.Usage),
		FinishReason:      mapStopReason(wire.StopReason),
	}, nil
}
