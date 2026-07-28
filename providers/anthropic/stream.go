package anthropic

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/stream/sse"
)

const maxSSEEventBytes = 4 << 20

type messageStream struct {
	target     llmkit.Target
	body       io.ReadCloser
	decoder    *sse.Decoder
	queue      []llmkit.StreamEvent
	requestID  string
	usage      usageDTO
	blockTypes map[int]string
	finish     llmkit.FinishReason
	sawFinish  bool
	sawStop    bool
	terminal   bool
	closeOnce  sync.Once
	closeErr   error
}

func newMessageStream(target llmkit.Target, response *http.Response) *messageStream {
	return &messageStream{
		target:     target,
		body:       response.Body,
		decoder:    sse.NewDecoder(response.Body, maxSSEEventBytes),
		requestID:  response.Header.Get("Request-ID"),
		blockTypes: make(map[int]string),
	}
}

type streamEvent struct {
	Type    string `json:"type"`
	Message struct {
		ID    string   `json:"id"`
		Usage usageDTO `json:"usage"`
	} `json:"message"`
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage usageDTO `json:"usage"`
	Error struct {
		Type string `json:"type"`
	} `json:"error"`
}

func (s *messageStream) Recv() (llmkit.StreamEvent, error) {
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
			if s.sawStop && s.sawFinish {
				return llmkit.StreamEvent{}, io.EOF
			}
			return llmkit.StreamEvent{}, s.malformed("Anthropic stream ended before message_stop")
		}
		if err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, s.malformed("Anthropic stream contained malformed SSE")
		}
		var wire streamEvent
		if err := json.Unmarshal(event.Data, &wire); err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, s.malformed("Anthropic stream contained malformed JSON")
		}
		if err := s.enqueue(wire); err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, err
		}
		if len(s.queue) > 0 {
			return s.pop(), nil
		}
	}
}

func (s *messageStream) enqueue(event streamEvent) error {
	switch event.Type {
	case "ping":
		return nil
	case "message_start":
		s.requestID = event.Message.ID
		s.usage = event.Message.Usage
		s.queue = append(s.queue, llmkit.StreamEvent{
			Type: llmkit.EventMessageStart, ProviderRequestID: s.requestID,
		})
	case "content_block_start":
		s.blockTypes[event.Index] = event.ContentBlock.Type
		switch event.ContentBlock.Type {
		case "tool_use":
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type:  llmkit.EventToolCallStart,
				Index: event.Index,
				ToolCall: &llmkit.ToolCall{
					ID: event.ContentBlock.ID, Name: event.ContentBlock.Name,
				},
			})
		default:
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventContentStart, Index: event.Index,
			})
		}
	case "content_block_delta":
		switch event.Delta.Type {
		case "text_delta":
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventTextDelta, Index: event.Index, Text: event.Delta.Text,
			})
		case "thinking_delta":
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventReasoningDelta, Index: event.Index, Text: event.Delta.Thinking,
			})
		case "input_json_delta":
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventToolArgumentsDelta, Index: event.Index,
				ArgumentsDelta: []byte(event.Delta.PartialJSON),
			})
		}
	case "content_block_stop":
		if s.blockTypes[event.Index] == "tool_use" {
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventToolCallEnd, Index: event.Index,
			})
		}
		delete(s.blockTypes, event.Index)
	case "message_delta":
		s.usage.OutputTokens = event.Usage.OutputTokens
		s.finish = mapStopReason(event.Delta.StopReason)
		s.sawFinish = true
		usage := normalizeUsage(s.usage)
		s.queue = append(s.queue,
			llmkit.StreamEvent{Type: llmkit.EventUsage, Usage: &usage},
			llmkit.StreamEvent{Type: llmkit.EventFinish, FinishReason: s.finish},
		)
	case "message_stop":
		s.sawStop = true
	case "error":
		return &llmkit.ProviderError{
			Provider: s.target.Provider, Model: s.target.Model,
			Kind: llmkit.ErrorUnknown, Phase: llmkit.PhaseStream,
			SafeMessage:  "provider reported a streaming error",
			ProviderCode: event.Error.Type, RequestID: s.requestID,
		}
	}
	return nil
}

func (s *messageStream) malformed(message string) error {
	return &llmkit.ProviderError{
		Provider: s.target.Provider, Model: s.target.Model,
		Kind: llmkit.ErrorMalformedResponse, Phase: llmkit.PhaseStream,
		SafeMessage: message, RequestID: s.requestID,
	}
}

func (s *messageStream) pop() llmkit.StreamEvent {
	event := s.queue[0]
	s.queue = s.queue[1:]
	return event
}

func (s *messageStream) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.body.Close() })
	return s.closeErr
}
