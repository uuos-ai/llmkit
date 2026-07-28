package openai

import (
	"context"
	"encoding/json"

	"github.com/uuos-ai/llmkit"
)

type chatResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Message struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageDTO `json:"usage"`
}

func (p *Provider) generateChat(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	payload, err := encodeChatRequest(call, false)
	if err != nil {
		return llmkit.Response{}, invalidRequest(call.Target, err.Error())
	}
	var wire chatResponse
	if _, err := p.doJSON(ctx, call, "/v1/chat/completions", payload, &wire); err != nil {
		return llmkit.Response{}, err
	}
	if len(wire.Choices) == 0 {
		return llmkit.Response{}, malformed(call.Target, wire.ID, "provider response contained no choices")
	}
	choice := wire.Choices[0]
	parts := make([]llmkit.ContentPart, 0, 2+len(choice.Message.ToolCalls))
	if choice.Message.ReasoningContent != "" {
		parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentReasoning, Text: choice.Message.ReasoningContent})
	}
	if choice.Message.Content != "" {
		parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentText, Text: choice.Message.Content})
	}
	for _, tool := range choice.Message.ToolCalls {
		parts = append(parts, llmkit.ContentPart{
			Type: llmkit.ContentToolCall,
			ToolCall: &llmkit.ToolCall{
				ID:        tool.ID,
				Name:      tool.Function.Name,
				Arguments: append([]byte(nil), tool.Function.Arguments...),
			},
		})
	}
	return llmkit.Response{
		ProviderRequestID: wire.ID,
		Message:           llmkit.Message{Role: llmkit.RoleAssistant, Parts: parts},
		Usage:             normalizeUsage(wire.Usage),
		FinishReason:      mapFinishReason(choice.FinishReason),
	}, nil
}

type responsesResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Incomplete *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Type      string          `json:"type"`
		ID        string          `json:"id"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"output"`
	Usage *usageDTO `json:"usage"`
}

func (p *Provider) generateResponses(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	payload, err := encodeResponsesRequest(call, false)
	if err != nil {
		return llmkit.Response{}, invalidRequest(call.Target, err.Error())
	}
	var wire responsesResponse
	if _, err := p.doJSON(ctx, call, "/v1/responses", payload, &wire); err != nil {
		return llmkit.Response{}, err
	}
	var parts []llmkit.ContentPart
	for _, item := range wire.Output {
		switch item.Type {
		case "message":
			for _, content := range item.Content {
				switch content.Type {
				case "output_text":
					parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentText, Text: content.Text})
				case "reasoning_text":
					parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentReasoning, Text: content.Text})
				}
			}
		case "function_call":
			parts = append(parts, llmkit.ContentPart{Type: llmkit.ContentToolCall, ToolCall: &llmkit.ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: append([]byte(nil), item.Arguments...),
			}})
		}
	}
	if len(parts) == 0 && wire.Status == "completed" {
		return llmkit.Response{}, malformed(call.Target, wire.ID, "provider response contained no output")
	}
	finish := llmkit.FinishStop
	if wire.Status == "incomplete" && wire.Incomplete != nil {
		finish = mapFinishReason(wire.Incomplete.Reason)
	}
	return llmkit.Response{
		ProviderRequestID: wire.ID,
		Message:           llmkit.Message{Role: llmkit.RoleAssistant, Parts: parts},
		Usage:             normalizeUsage(wire.Usage),
		FinishReason:      finish,
	}, nil
}

func malformed(target llmkit.Target, requestID, message string) error {
	return &llmkit.ProviderError{
		Provider:    target.Provider,
		Model:       target.Model,
		Kind:        llmkit.ErrorMalformedResponse,
		Phase:       llmkit.PhaseDecode,
		SafeMessage: message,
		RequestID:   requestID,
	}
}
