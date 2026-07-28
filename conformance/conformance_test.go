package conformance

import (
	"context"
	"io"
	"testing"

	"github.com/uuos-ai/llmkit"
)

type fakeGenerator struct{}

func (fakeGenerator) ID() llmkit.ProviderID { return "fake" }
func (fakeGenerator) Capabilities(context.Context, llmkit.Target) (llmkit.Capabilities, error) {
	return llmkit.Capabilities{}, nil
}
func (fakeGenerator) Generate(context.Context, llmkit.GenerateCall) (llmkit.Response, error) {
	return llmkit.Response{
		Message: llmkit.Message{Role: llmkit.RoleAssistant, Parts: []llmkit.ContentPart{
			{Type: llmkit.ContentText, Text: "hello"},
		}},
		FinishReason: llmkit.FinishStop,
		Usage:        llmkit.Usage{Source: llmkit.UsageReported},
	}, nil
}
func (fakeGenerator) Stream(context.Context, llmkit.GenerateCall) (llmkit.EventStream, error) {
	usage := llmkit.Usage{Source: llmkit.UsageReported}
	return &fakeStream{events: []llmkit.StreamEvent{
		{Type: llmkit.EventTextDelta, Text: "hel"},
		{Type: llmkit.EventTextDelta, Text: "lo"},
		{Type: llmkit.EventUsage, Usage: &usage},
		{Type: llmkit.EventFinish, FinishReason: llmkit.FinishStop},
	}}, nil
}

type fakeStream struct {
	events []llmkit.StreamEvent
	index  int
}

func (s *fakeStream) Recv() (llmkit.StreamEvent, error) {
	if s.index == len(s.events) {
		return llmkit.StreamEvent{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}

func (*fakeStream) Close() error { return nil }

func TestHarness(t *testing.T) {
	generator := fakeGenerator{}
	RunGeneration(t, GenerationCase{
		Name:         "generation",
		Generator:    generator,
		ExpectedText: "hello",
		FinishReason: llmkit.FinishStop,
		UsageSource:  llmkit.UsageReported,
	})
	RunStreaming(t, StreamingCase{
		Name:          "streaming",
		Generator:     generator,
		ExpectedText:  "hello",
		FinishReason:  llmkit.FinishStop,
		RequireUsage:  true,
		RequireFinish: true,
	})
}
