package retry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uuos-ai/llmkit"
)

func TestDoRetriesSameTarget(t *testing.T) {
	target := llmkit.Target{
		Provider: "provider",
		Model:    "model",
		Region:   "region",
		Endpoint: "https://example.test",
	}
	attempts := 0
	result, err := Do(context.Background(), target, Policy{MaxAttempts: 3}, func(_ context.Context, got llmkit.Target) (string, error) {
		attempts++
		if got != target {
			t.Fatalf("target changed: %#v", got)
		}
		if attempts < 3 {
			return "", &llmkit.ProviderError{Kind: llmkit.ErrorOverloaded, Retryable: true, Phase: llmkit.PhaseConnect}
		}
		return "ok", nil
	})
	if err != nil || result != "ok" || attempts != 3 {
		t.Fatalf("result = %q, attempts = %d, error = %v", result, attempts, err)
	}
}

func TestDoDoesNotRetryCancellation(t *testing.T) {
	attempts := 0
	_, err := Do(context.Background(), llmkit.Target{}, Policy{MaxAttempts: 3}, func(context.Context, llmkit.Target) (struct{}, error) {
		attempts++
		return struct{}{}, &llmkit.ProviderError{
			Kind:      llmkit.ErrorCanceled,
			Retryable: true,
		}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("attempts = %d, error = %v", attempts, err)
	}
}

func TestDoStopsWhileWaiting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Do(ctx, llmkit.Target{}, Policy{
		MaxAttempts: 2,
		Backoff:     func(int) time.Duration { return time.Hour },
	}, func(context.Context, llmkit.Target) (struct{}, error) {
		return struct{}{}, &llmkit.ProviderError{
			Kind:      llmkit.ErrorOverloaded,
			Retryable: true,
			Phase:     llmkit.PhaseConnect,
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
}

func TestSafeRejectsUnknownOutcomeAndOutputStarted(t *testing.T) {
	unknown := &llmkit.ProviderError{Kind: llmkit.ErrorOutcomeUnknown, Retryable: true, Phase: llmkit.PhaseReadBody}
	if Safe(unknown, true, false) {
		t.Fatal("unknown outcome was retryable")
	}
	connect := &llmkit.ProviderError{Kind: llmkit.ErrorProviderUnavailable, Retryable: true, Phase: llmkit.PhaseConnect}
	if !Safe(connect, false, false) {
		t.Fatal("pre-dispatch connect failure was not retryable")
	}
	if Safe(connect, true, true) {
		t.Fatal("request with output was retryable")
	}
}

func TestDoRejectsUnboundedPolicy(t *testing.T) {
	_, err := Do(context.Background(), llmkit.Target{}, Policy{}, func(context.Context, llmkit.Target) (struct{}, error) {
		return struct{}{}, nil
	})
	if err == nil {
		t.Fatal("expected invalid policy error")
	}
}
