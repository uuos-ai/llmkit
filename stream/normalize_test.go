package stream

import (
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestNormalizerProducesOrderedUniqueTerminal(t *testing.T) {
	n := NewNormalizer("req", "resp")
	start, err := n.Start()
	if err != nil || start.Type != llmkit.EventResponseCreated || start.Sequence != 1 {
		t.Fatalf("start = %#v, %v", start, err)
	}
	delta, err := n.Accept(llmkit.StreamEvent{Type: llmkit.EventTextDelta, Text: "x"})
	if err != nil || delta.Type != llmkit.EventContentDelta || delta.Sequence != 2 {
		t.Fatalf("delta = %#v, %v", delta, err)
	}
	done, err := n.Accept(llmkit.StreamEvent{Type: llmkit.EventFinish})
	if err != nil || done.Type != llmkit.EventResponseCompleted || done.Sequence != 3 {
		t.Fatalf("done = %#v, %v", done, err)
	}
	if _, err := n.Accept(llmkit.StreamEvent{Type: llmkit.EventFinish}); err == nil {
		t.Fatal("second terminal accepted")
	}
}
