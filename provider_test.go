package llmkit

import (
	"errors"
	"io"
	"testing"
)

type sliceStream struct {
	events []StreamEvent
	index  int
	err    error
	closed bool
}

func (s *sliceStream) Recv() (StreamEvent, error) {
	if s.index < len(s.events) {
		event := s.events[s.index]
		s.index++
		return event, nil
	}
	if s.err != nil {
		return StreamEvent{}, s.err
	}
	return StreamEvent{}, io.EOF
}

func (s *sliceStream) Close() error {
	s.closed = true
	return nil
}

func TestCapabilitiesSupports(t *testing.T) {
	capabilities := Capabilities{Models: map[ModelID]ModelCapabilities{
		"model": {Capabilities: []Capability{CapabilityGenerate, CapabilityTools}},
	}}
	if !capabilities.Supports("model", CapabilityTools) {
		t.Fatal("expected tools support")
	}
	if capabilities.Supports("model", CapabilityAudio) {
		t.Fatal("unexpected audio support")
	}
}

func TestDrainStreamClosesNormalStream(t *testing.T) {
	stream := &sliceStream{events: []StreamEvent{{Type: EventTextDelta, Text: "hello"}}}
	events, err := DrainStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Text != "hello" {
		t.Fatalf("unexpected events: %#v", events)
	}
	if !stream.closed {
		t.Fatal("stream was not closed")
	}
}

func TestDrainStreamDoesNotTreatFailureAsEOF(t *testing.T) {
	want := errors.New("truncated stream")
	stream := &sliceStream{err: want}
	_, err := DrainStream(stream)
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
	if !stream.closed {
		t.Fatal("failed stream was not closed")
	}
}
