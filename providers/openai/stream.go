package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/stream/sse"
	"github.com/uuos-ai/llmkit/transport"
)

const maxSSEEventBytes = 4 << 20

func (p *Provider) openStream(
	ctx context.Context,
	call llmkit.GenerateCall,
	path string,
	payload any,
) (*http.Response, error) {
	request, err := transport.NewJSONRequest(
		ctx,
		call.Target,
		call.Credential,
		http.MethodPost,
		p.endpointFor(call.Target)+path,
		payload,
		nil,
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	if call.OperationID != "" {
		request.Header.Set("X-Client-Request-ID", call.OperationID)
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return nil, p.classifyError(call.Target, err)
	}
	return response, nil
}

func (p *Provider) streamChat(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	payload, err := encodeChatRequest(call, true)
	if err != nil {
		return nil, invalidRequest(call.Target, err.Error())
	}
	response, err := p.openStream(ctx, call, "/v1/chat/completions", payload)
	if err != nil {
		return nil, err
	}
	return &chatStream{
		target:    call.Target,
		body:      response.Body,
		decoder:   sse.NewDecoder(response.Body, maxSSEEventBytes),
		requestID: response.Header.Get("X-Request-ID"),
		toolSeen:  make(map[int]bool),
	}, nil
}

type chatStream struct {
	target    llmkit.Target
	body      io.ReadCloser
	decoder   *sse.Decoder
	requestID string
	queue     []llmkit.StreamEvent
	toolSeen  map[int]bool
	started   bool
	sawFinish bool
	sawDone   bool
	terminal  bool
	closeOnce sync.Once
	closeErr  error
}

type chatStreamChunk struct {
	ID      string `json:"id"`
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *usageDTO `json:"usage"`
}

func (s *chatStream) Recv() (llmkit.StreamEvent, error) {
	if len(s.queue) > 0 {
		return s.pop(), nil
	}
	if s.terminal {
		return llmkit.StreamEvent{}, io.EOF
	}

	for {
		event, err := s.decoder.Next()
		if errors.Is(err, io.EOF) {
			s.terminal = true
			_ = s.Close()
			if s.sawDone && s.sawFinish {
				return llmkit.StreamEvent{}, io.EOF
			}
			return llmkit.StreamEvent{}, malformed(s.target, s.requestID, "OpenAI chat stream ended before finalization")
		}
		if err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, malformed(s.target, s.requestID, "OpenAI chat stream contained malformed SSE")
		}
		if string(event.Data) == "[DONE]" {
			s.sawDone = true
			s.terminal = true
			_ = s.Close()
			if !s.sawFinish {
				return llmkit.StreamEvent{}, malformed(s.target, s.requestID, "OpenAI chat stream omitted finish reason")
			}
			return llmkit.StreamEvent{}, io.EOF
		}

		var chunk chatStreamChunk
		if err := json.Unmarshal(event.Data, &chunk); err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, malformed(s.target, s.requestID, "OpenAI chat stream contained malformed JSON")
		}
		if chunk.ID != "" {
			s.requestID = chunk.ID
		}
		s.enqueueChatChunk(chunk)
		if len(s.queue) > 0 {
			return s.pop(), nil
		}
	}
}

func (s *chatStream) enqueueChatChunk(chunk chatStreamChunk) {
	if !s.started {
		s.started = true
		s.queue = append(s.queue, llmkit.StreamEvent{
			Type:              llmkit.EventMessageStart,
			ProviderRequestID: s.requestID,
		})
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.ReasoningContent != "" {
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventReasoningDelta,
				Text: choice.Delta.ReasoningContent,
			})
		}
		if choice.Delta.Content != "" {
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventTextDelta,
				Text: choice.Delta.Content,
			})
		}
		for _, tool := range choice.Delta.ToolCalls {
			if !s.toolSeen[tool.Index] {
				s.toolSeen[tool.Index] = true
				s.queue = append(s.queue, llmkit.StreamEvent{
					Type:  llmkit.EventToolCallStart,
					Index: tool.Index,
					ToolCall: &llmkit.ToolCall{
						ID:   tool.ID,
						Name: tool.Function.Name,
					},
				})
			}
			if tool.Function.Arguments != "" {
				s.queue = append(s.queue, llmkit.StreamEvent{
					Type:           llmkit.EventToolArgumentsDelta,
					Index:          tool.Index,
					ArgumentsDelta: []byte(tool.Function.Arguments),
				})
			}
		}
		if choice.FinishReason != "" {
			for index := range s.toolSeen {
				s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventToolCallEnd, Index: index})
			}
			s.toolSeen = make(map[int]bool)
			s.sawFinish = true
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type:         llmkit.EventFinish,
				FinishReason: mapFinishReason(choice.FinishReason),
			})
		}
	}
	if chunk.Usage != nil {
		usage := normalizeUsage(chunk.Usage)
		s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventUsage, Usage: &usage})
	}
}

func (s *chatStream) pop() llmkit.StreamEvent {
	event := s.queue[0]
	s.queue = s.queue[1:]
	return event
}

func (s *chatStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.body.Close()
	})
	return s.closeErr
}

func (p *Provider) streamResponses(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	payload, err := encodeResponsesRequest(call, true)
	if err != nil {
		return nil, invalidRequest(call.Target, err.Error())
	}
	response, err := p.openStream(ctx, call, "/v1/responses", payload)
	if err != nil {
		return nil, err
	}
	return &responsesStream{
		target:  call.Target,
		body:    response.Body,
		decoder: sse.NewDecoder(response.Body, maxSSEEventBytes),
	}, nil
}

type responsesStream struct {
	target     llmkit.Target
	body       io.ReadCloser
	decoder    *sse.Decoder
	queue      []llmkit.StreamEvent
	requestID  string
	completed  bool
	terminal   bool
	pendingErr error
	closeOnce  sync.Once
	closeErr   error
}

type responsesStreamEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	ItemID   string `json:"item_id"`
	OutputID string `json:"output_index"`
	Item     struct {
		Type   string `json:"type"`
		ID     string `json:"id"`
		CallID string `json:"call_id"`
		Name   string `json:"name"`
	} `json:"item"`
	Response struct {
		ID         string    `json:"id"`
		Status     string    `json:"status"`
		Usage      *usageDTO `json:"usage"`
		Incomplete *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func (s *responsesStream) Recv() (llmkit.StreamEvent, error) {
	if len(s.queue) > 0 {
		return s.pop(), nil
	}
	if s.terminal {
		return llmkit.StreamEvent{}, io.EOF
	}
	for {
		event, err := s.decoder.Next()
		if errors.Is(err, io.EOF) {
			s.terminal = true
			_ = s.Close()
			if s.completed {
				return llmkit.StreamEvent{}, io.EOF
			}
			return llmkit.StreamEvent{}, malformed(s.target, s.requestID, "OpenAI Responses stream ended before completion")
		}
		if err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, malformed(s.target, s.requestID, "OpenAI Responses stream contained malformed SSE")
		}
		var wire responsesStreamEvent
		if err := json.Unmarshal(event.Data, &wire); err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, malformed(s.target, s.requestID, "OpenAI Responses stream contained malformed JSON")
		}
		s.enqueueResponsesEvent(wire)
		if s.pendingErr != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, s.pendingErr
		}
		if len(s.queue) > 0 {
			return s.pop(), nil
		}
	}
}

func (s *responsesStream) enqueueResponsesEvent(event responsesStreamEvent) {
	switch event.Type {
	case "response.created":
		s.requestID = event.Response.ID
		s.queue = append(s.queue, llmkit.StreamEvent{
			Type:              llmkit.EventMessageStart,
			ProviderRequestID: s.requestID,
		})
	case "response.output_text.delta":
		s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventTextDelta, Text: event.Delta})
	case "response.reasoning_text.delta":
		s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventReasoningDelta, Text: event.Delta})
	case "response.output_item.added":
		if event.Item.Type == "function_call" {
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventToolCallStart,
				ToolCall: &llmkit.ToolCall{
					ID:   event.Item.CallID,
					Name: event.Item.Name,
				},
			})
		}
	case "response.function_call_arguments.delta":
		s.queue = append(s.queue, llmkit.StreamEvent{
			Type:           llmkit.EventToolArgumentsDelta,
			ArgumentsDelta: []byte(event.Delta),
		})
	case "response.function_call_arguments.done":
		s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventToolCallEnd})
	case "response.completed", "response.incomplete":
		s.requestID = event.Response.ID
		if event.Response.Usage != nil {
			usage := normalizeUsage(event.Response.Usage)
			s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventUsage, Usage: &usage})
		}
		finish := llmkit.FinishStop
		if event.Type == "response.incomplete" && event.Response.Incomplete != nil {
			finish = mapFinishReason(event.Response.Incomplete.Reason)
		}
		s.completed = true
		s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventFinish, FinishReason: finish})
	case "error", "response.failed":
		s.pendingErr = &llmkit.ProviderError{
			Provider:     s.target.Provider,
			Model:        s.target.Model,
			Kind:         llmkit.ErrorUnknown,
			Phase:        llmkit.PhaseStream,
			SafeMessage:  "provider reported a streaming error",
			ProviderCode: event.Error.Code,
			RequestID:    s.requestID,
		}
	}
}

func (s *responsesStream) pop() llmkit.StreamEvent {
	event := s.queue[0]
	s.queue = s.queue[1:]
	return event
}

func (s *responsesStream) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.body.Close()
	})
	return s.closeErr
}
