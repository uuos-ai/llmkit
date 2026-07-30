package stream

import (
	"errors"
	"fmt"

	"github.com/uuos-ai/llmkit"
)

// Normalizer converts compact adapter events into the public ordered stream
// state machine. One instance is used for exactly one response.
type Normalizer struct {
	requestID, responseID string
	sequence              uint64
	started, terminal     bool
}

func NewNormalizer(requestID, responseID string) *Normalizer {
	return &Normalizer{requestID: requestID, responseID: responseID}
}

func (n *Normalizer) Start() (llmkit.StreamEvent, error) {
	if n.started {
		return llmkit.StreamEvent{}, errors.New("stream: response already started")
	}
	n.started = true
	return n.event(llmkit.StreamEvent{Type: llmkit.EventResponseCreated}), nil
}

func (n *Normalizer) Accept(input llmkit.StreamEvent) (llmkit.StreamEvent, error) {
	if !n.started || n.terminal {
		return llmkit.StreamEvent{}, errors.New("stream: event outside active response")
	}
	switch input.Type {
	case llmkit.EventMessageStart, llmkit.EventToolCallStart:
		input.Type = llmkit.EventOutputItemAdded
	case llmkit.EventContentStart:
		input.Type = llmkit.EventContentPartAdded
	case llmkit.EventTextDelta, llmkit.EventReasoningDelta:
		input.Type = llmkit.EventContentDelta
	case llmkit.EventToolArgumentsDelta:
		input.Type = llmkit.EventToolArguments
	case llmkit.EventToolCallEnd:
		input.Type = llmkit.EventOutputItemDone
	case llmkit.EventUsage:
		input.Type = llmkit.EventUsageUpdated
	case llmkit.EventFinish:
		input.Type = llmkit.EventResponseCompleted
		n.terminal = true
	case llmkit.EventResponseCompleted, llmkit.EventResponseFailed, llmkit.EventResponseCancelled:
		n.terminal = true
	case llmkit.EventOutputItemAdded, llmkit.EventContentPartAdded, llmkit.EventContentDelta,
		llmkit.EventToolArguments, llmkit.EventUsageUpdated, llmkit.EventContentPartDone,
		llmkit.EventOutputItemDone:
	default:
		return llmkit.StreamEvent{}, fmt.Errorf("stream: unsupported event type %q", input.Type)
	}
	return n.event(input), nil
}

func (n *Normalizer) Fail(err error) llmkit.StreamEvent {
	n.terminal = true
	streamErr := &llmkit.StreamError{Code: llmkit.ErrorInternal, Message: "request failed"}
	var providerErr *llmkit.ProviderError
	if errors.As(err, &providerErr) {
		streamErr.Code, streamErr.Message, streamErr.Retryable = providerErr.Kind, providerErr.SafeMessage, providerErr.Retryable
		streamErr.RetryAfterMS = providerErr.RetryAfter.Milliseconds()
	}
	return n.event(llmkit.StreamEvent{Type: llmkit.EventResponseFailed, Error: streamErr})
}

func (n *Normalizer) Cancel() llmkit.StreamEvent {
	n.terminal = true
	return n.event(llmkit.StreamEvent{Type: llmkit.EventResponseCancelled})
}

func (n *Normalizer) Terminal() bool { return n.terminal }

func (n *Normalizer) event(event llmkit.StreamEvent) llmkit.StreamEvent {
	n.sequence++
	event.RequestID, event.ResponseID, event.Sequence = n.requestID, n.responseID, n.sequence
	return event
}
