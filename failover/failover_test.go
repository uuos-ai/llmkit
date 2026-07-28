package failover

import (
	"context"
	"errors"
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestDoUsesExplicitOrderAndPolicy(t *testing.T) {
	targets := []llmkit.Target{
		{Provider: "a", Model: "one"},
		{Provider: "b", Model: "two"},
	}
	var visited []llmkit.ProviderID
	result, err := Do(context.Background(), targets,
		func(_ llmkit.Target, err error) bool { return errors.Is(err, errRetryable) },
		func(_ context.Context, target llmkit.Target) (string, error) {
			visited = append(visited, target.Provider)
			if target.Provider == "a" {
				return "", errRetryable
			}
			return "ok", nil
		})
	if err != nil || result != "ok" || len(visited) != 2 || visited[0] != "a" || visited[1] != "b" {
		t.Fatalf("result=%q err=%v visited=%v", result, err, visited)
	}
}

func TestDoStopsWhenHostDeniesContinuation(t *testing.T) {
	want := errors.New("stop")
	_, err := Do(context.Background(), []llmkit.Target{{Provider: "a", Model: "one"}},
		func(llmkit.Target, error) bool { return false },
		func(context.Context, llmkit.Target) (int, error) { return 0, want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestDoReportsExhaustionWithoutLeakingErrorsInString(t *testing.T) {
	_, err := Do(context.Background(), []llmkit.Target{{Provider: "a", Model: "one"}},
		func(llmkit.Target, error) bool { return true },
		func(context.Context, llmkit.Target) (int, error) { return 0, errors.New("sensitive") })
	var exhausted *ExhaustedError
	if !errors.As(err, &exhausted) || len(exhausted.Attempts) != 1 ||
		err.Error() != "llmkit failover: 1 explicit targets exhausted" {
		t.Fatalf("error = %#v", err)
	}
}

var errRetryable = errors.New("retryable")
