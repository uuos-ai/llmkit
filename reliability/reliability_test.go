package reliability

import (
	"context"
	"errors"
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestRunEmitsSafeSample(t *testing.T) {
	target := llmkit.Target{Provider: "test", Model: "model"}
	var got Sample
	_, err := Run(context.Background(), target, OperationGenerate,
		ObserverFunc(func(_ context.Context, sample Sample) { got = sample }),
		func(context.Context) (string, llmkit.Usage, error) {
			return "", llmkit.Usage{}, &llmkit.ProviderError{
				Provider: target.Provider, Model: target.Model,
				Kind: llmkit.ErrorRateLimit, Retryable: true, StatusCode: 429,
				SafeMessage: "safe",
			}
		},
	)
	if err == nil || got.Succeeded || got.ErrorKind != llmkit.ErrorRateLimit ||
		!got.Retryable || got.StatusCode != 429 || got.Duration < 0 {
		t.Fatalf("sample = %#v, err = %v", got, err)
	}
}

func TestTrackerProducesAdvisorySignalAndRecovers(t *testing.T) {
	tracker := NewTracker(TrackerConfig{UnavailableAfter: 2})
	target := llmkit.Target{Provider: "test", Model: "model"}
	failure := Sample{Target: target, ErrorKind: llmkit.ErrorOverloaded, Retryable: true}
	tracker.Observe(context.Background(), failure)
	if got := tracker.Snapshot(target).Health; got != HealthDegraded {
		t.Fatalf("health = %q", got)
	}
	tracker.Observe(context.Background(), failure)
	if got := tracker.Snapshot(target).Health; got != HealthUnavailable {
		t.Fatalf("health = %q", got)
	}
	tracker.Observe(context.Background(), Sample{Target: target, Succeeded: true})
	snapshot := tracker.Snapshot(target)
	if snapshot.Health != HealthHealthy || snapshot.ConsecutiveRetryableFails != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestRunPreservesOriginalError(t *testing.T) {
	want := errors.New("failure")
	_, got := Run(context.Background(), llmkit.Target{}, OperationEmbed, nil,
		func(context.Context) (int, llmkit.Usage, error) { return 0, llmkit.Usage{}, want })
	if !errors.Is(got, want) {
		t.Fatalf("error = %v", got)
	}
}
