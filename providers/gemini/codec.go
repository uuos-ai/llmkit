package gemini

import (
	"encoding/json"
	"fmt"

	"github.com/uuos-ai/llmkit"
)

type usageDTO struct {
	PromptTokens    int64 `json:"promptTokenCount"`
	CandidateTokens int64 `json:"candidatesTokenCount"`
	TotalTokens     int64 `json:"totalTokenCount"`
	CachedTokens    int64 `json:"cachedContentTokenCount"`
	ThoughtsTokens  int64 `json:"thoughtsTokenCount"`
}

type partDTO struct {
	Text             string           `json:"text,omitempty"`
	Thought          bool             `json:"thought,omitempty"`
	InlineData       *inlineDataDTO   `json:"inlineData,omitempty"`
	FunctionCall     *functionCallDTO `json:"functionCall,omitempty"`
	FunctionResponse map[string]any   `json:"functionResponse,omitempty"`
}

type inlineDataDTO struct {
	MIMEType string `json:"mimeType"`
	Data     []byte `json:"data"`
}

type functionCallDTO struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type contentDTO struct {
	Role  string    `json:"role,omitempty"`
	Parts []partDTO `json:"parts"`
}

type generateResponse struct {
	ResponseID string `json:"responseId"`
	Candidates []struct {
		Content      contentDTO `json:"content"`
		FinishReason string     `json:"finishReason"`
	} `json:"candidates"`
	Usage usageDTO `json:"usageMetadata"`
}

func encodeRequest(call llmkit.GenerateCall) (map[string]any, error) {
	var systemParts []partDTO
	var contents []contentDTO
	for _, message := range call.Request.Messages {
		parts, err := encodeParts(message.Parts)
		if err != nil {
			return nil, err
		}
		if message.Role == llmkit.RoleSystem {
			for _, part := range message.Parts {
				if part.Type != llmkit.ContentText {
					return nil, fmt.Errorf("Gemini system instructions only support text")
				}
			}
			systemParts = append(systemParts, parts...)
			continue
		}
		role := "user"
		if message.Role == llmkit.RoleAssistant {
			role = "model"
		} else if message.Role == llmkit.RoleTool {
			role = "function"
		} else if message.Role != llmkit.RoleUser {
			return nil, fmt.Errorf("unsupported Gemini message role %q", message.Role)
		}
		contents = append(contents, contentDTO{Role: role, Parts: parts})
	}
	payload := map[string]any{"contents": contents}
	if len(systemParts) > 0 {
		payload["systemInstruction"] = contentDTO{Parts: systemParts}
	}
	config := map[string]any{}
	if call.Request.MaxOutputTokens != nil {
		config["maxOutputTokens"] = *call.Request.MaxOutputTokens
	}
	if call.Request.Temperature != nil {
		config["temperature"] = *call.Request.Temperature
	}
	if call.Request.TopP != nil {
		config["topP"] = *call.Request.TopP
	}
	if len(call.Request.Stop) > 0 {
		config["stopSequences"] = call.Request.Stop
	}
	if call.Request.ResponseFormat != nil {
		config["responseMimeType"] = "application/json"
		config["responseJsonSchema"] = json.RawMessage(call.Request.ResponseFormat.Schema)
	}
	if len(config) > 0 {
		payload["generationConfig"] = config
	}
	if len(call.Request.Tools) > 0 {
		declarations := make([]map[string]any, 0, len(call.Request.Tools))
		for _, tool := range call.Request.Tools {
			declarations = append(declarations, map[string]any{
				"name": tool.Name, "description": tool.Description,
				"parametersJsonSchema": json.RawMessage(tool.InputSchema),
			})
		}
		payload["tools"] = []map[string]any{{"functionDeclarations": declarations}}
	}
	return payload, nil
}

func encodeParts(parts []llmkit.ContentPart) ([]partDTO, error) {
	result := make([]partDTO, 0, len(parts))
	for _, part := range parts {
		switch part.Type {
		case llmkit.ContentText:
			result = append(result, partDTO{Text: part.Text})
		case llmkit.ContentReasoning:
			result = append(result, partDTO{Text: part.Text, Thought: true})
		case llmkit.ContentImage, llmkit.ContentAudio:
			if part.Media == nil || len(part.Media.Data) == 0 {
				return nil, fmt.Errorf("Gemini media input requires inline data")
			}
			result = append(result, partDTO{InlineData: &inlineDataDTO{
				MIMEType: part.Media.MediaType, Data: part.Media.Data,
			}})
		case llmkit.ContentToolCall:
			if part.ToolCall == nil {
				return nil, fmt.Errorf("tool call content is missing tool call")
			}
			result = append(result, partDTO{FunctionCall: &functionCallDTO{
				ID: part.ToolCall.ID, Name: part.ToolCall.Name,
				Args: json.RawMessage(part.ToolCall.Arguments),
			}})
		case llmkit.ContentToolResult:
			if part.ToolResult == nil {
				return nil, fmt.Errorf("tool result content is missing tool result")
			}
			if part.ToolResult.Name == "" {
				return nil, fmt.Errorf("Gemini tool result requires a function name")
			}
			result = append(result, partDTO{FunctionResponse: map[string]any{
				"id":       part.ToolResult.CallID,
				"name":     part.ToolResult.Name,
				"response": map[string]any{"output": flattenText(part.ToolResult.Content)},
			}})
		default:
			return nil, fmt.Errorf("unsupported Gemini content type %q", part.Type)
		}
	}
	return result, nil
}

func decodeResponse(target llmkit.Target, wire generateResponse) (llmkit.Response, error) {
	if len(wire.Candidates) == 0 {
		return llmkit.Response{}, malformed(target, "provider response contained no candidates")
	}
	candidate := wire.Candidates[0]
	parts := make([]llmkit.ContentPart, 0, len(candidate.Content.Parts))
	for _, part := range candidate.Content.Parts {
		switch {
		case part.FunctionCall != nil:
			parts = append(parts, llmkit.ContentPart{
				Type: llmkit.ContentToolCall,
				ToolCall: &llmkit.ToolCall{
					ID: part.FunctionCall.ID, Name: part.FunctionCall.Name,
					Arguments: append([]byte(nil), part.FunctionCall.Args...),
				},
			})
		case part.Text != "" && part.Thought:
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentReasoning, Text: part.Text})
		case part.Text != "":
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentText, Text: part.Text})
		}
	}
	if len(parts) == 0 {
		return llmkit.Response{}, malformed(target, "provider response contained no content")
	}
	return llmkit.Response{
		ProviderRequestID: wire.ResponseID,
		Message:           llmkit.Message{Role: llmkit.RoleAssistant, Parts: parts},
		Usage:             normalizeUsage(wire.Usage), FinishReason: mapFinish(candidate.FinishReason),
	}, nil
}

func normalizeUsage(usage usageDTO) llmkit.Usage {
	total := usage.TotalTokens
	if total == 0 {
		total = usage.PromptTokens + usage.CandidateTokens
	}
	return llmkit.Usage{
		Source: llmkit.UsageReported, InputTokens: usage.PromptTokens,
		OutputTokens: usage.CandidateTokens, TotalTokens: total,
		CachedRead: usage.CachedTokens, ReasoningTokens: usage.ThoughtsTokens,
	}
}

func mapFinish(reason string) llmkit.FinishReason {
	switch reason {
	case "STOP", "FINISH_REASON_UNSPECIFIED":
		return llmkit.FinishStop
	case "MAX_TOKENS":
		return llmkit.FinishLength
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII":
		return llmkit.FinishContentFilter
	case "MALFORMED_FUNCTION_CALL", "UNEXPECTED_TOOL_CALL":
		return llmkit.FinishError
	default:
		return llmkit.FinishUnknown
	}
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
