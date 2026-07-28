package gemini

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

type generateStream struct {
	target    llmkit.Target
	body      io.ReadCloser
	decoder   *sse.Decoder
	queue     []llmkit.StreamEvent
	started   bool
	finished  bool
	terminal  bool
	closeOnce sync.Once
	closeErr  error
}

func newGenerateStream(target llmkit.Target, response *http.Response) *generateStream {
	return &generateStream{
		target: target, body: response.Body,
		decoder: sse.NewDecoder(response.Body, maxSSEEventBytes),
	}
}

func (s *generateStream) Recv() (llmkit.StreamEvent, error) {
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
			if s.finished {
				return llmkit.StreamEvent{}, io.EOF
			}
			return llmkit.StreamEvent{}, s.malformed("Gemini stream ended before a finish reason")
		}
		if err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, s.malformed("Gemini stream contained malformed SSE")
		}
		var wire generateResponse
		if err := json.Unmarshal(event.Data, &wire); err != nil {
			s.terminal = true
			_ = s.Close()
			return llmkit.StreamEvent{}, s.malformed("Gemini stream contained malformed JSON")
		}
		s.enqueue(wire)
		if len(s.queue) > 0 {
			return s.pop(), nil
		}
	}
}

func (s *generateStream) enqueue(wire generateResponse) {
	if !s.started {
		s.started = true
		s.queue = append(s.queue, llmkit.StreamEvent{
			Type: llmkit.EventMessageStart, ProviderRequestID: wire.ResponseID,
		})
	}
	if len(wire.Candidates) > 0 {
		candidate := wire.Candidates[0]
		for index, part := range candidate.Content.Parts {
			switch {
			case part.FunctionCall != nil:
				s.queue = append(s.queue,
					llmkit.StreamEvent{
						Type: llmkit.EventToolCallStart, Index: index,
						ToolCall: &llmkit.ToolCall{
							ID: part.FunctionCall.ID, Name: part.FunctionCall.Name,
						},
					},
					llmkit.StreamEvent{
						Type: llmkit.EventToolArgumentsDelta, Index: index,
						ArgumentsDelta: append([]byte(nil), part.FunctionCall.Args...),
					},
					llmkit.StreamEvent{Type: llmkit.EventToolCallEnd, Index: index},
				)
			case part.Text != "" && part.Thought:
				s.queue = append(s.queue, llmkit.StreamEvent{
					Type: llmkit.EventReasoningDelta, Index: index, Text: part.Text,
				})
			case part.Text != "":
				s.queue = append(s.queue, llmkit.StreamEvent{
					Type: llmkit.EventTextDelta, Index: index, Text: part.Text,
				})
			}
		}
		if candidate.FinishReason != "" {
			s.finished = true
			s.queue = append(s.queue, llmkit.StreamEvent{
				Type: llmkit.EventFinish, FinishReason: mapFinish(candidate.FinishReason),
			})
		}
	}
	if wire.Usage != (usageDTO{}) {
		usage := normalizeUsage(wire.Usage)
		s.queue = append(s.queue, llmkit.StreamEvent{Type: llmkit.EventUsage, Usage: &usage})
	}
}

func (s *generateStream) malformed(message string) error {
	return &llmkit.ProviderError{
		Provider: s.target.Provider, Model: s.target.Model,
		Kind: llmkit.ErrorMalformedResponse, Phase: llmkit.PhaseStream,
		SafeMessage: message,
	}
}

func (s *generateStream) pop() llmkit.StreamEvent {
	event := s.queue[0]
	s.queue = s.queue[1:]
	return event
}

func (s *generateStream) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.body.Close() })
	return s.closeErr
}
