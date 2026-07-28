// Package failover provides host-controlled traversal of an explicit target
// list. It contains no default routing or error policy.
package failover

import (
	"context"
	"fmt"

	"github.com/uuos-ai/llmkit"
)

type DecisionFunc func(target llmkit.Target, err error) bool

type Attempt struct {
	Target llmkit.Target
	Err    error
}

type ExhaustedError struct {
	Attempts []Attempt
}

func (e *ExhaustedError) Error() string {
	return fmt.Sprintf("llmkit failover: %d explicit targets exhausted", len(e.Attempts))
}

// Do tries targets in the exact order supplied by the host. Continue must
// explicitly authorize moving past every failed target.
func Do[T any](
	ctx context.Context,
	targets []llmkit.Target,
	continueAfter DecisionFunc,
	operation func(context.Context, llmkit.Target) (T, error),
) (T, error) {
	var zero T
	if len(targets) == 0 {
		return zero, fmt.Errorf("llmkit failover: at least one target is required")
	}
	if continueAfter == nil {
		return zero, fmt.Errorf("llmkit failover: an explicit continuation policy is required")
	}
	attempts := make([]Attempt, 0, len(targets))
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		result, err := operation(ctx, target)
		if err == nil {
			return result, nil
		}
		attempts = append(attempts, Attempt{Target: target, Err: err})
		if !continueAfter(target, err) {
			return zero, err
		}
	}
	return zero, &ExhaustedError{Attempts: attempts}
}
