// Package retry provides explicit, bounded retries against one immutable
// llmkit Target. It never selects another Provider, model, region, or endpoint.
package retry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/uuos-ai/llmkit"
)

type Policy struct {
	MaxAttempts        int
	MaxTotalAttempts   int
	ProviderIdempotent bool
	Backoff            func(attempt int) time.Duration
	MaxDelay           time.Duration
}

// Safe reports whether a failed generation may be retried without silently
// risking duplicate billing. Once output started, retry is never safe.
func Safe(err error, providerIdempotent, outputStarted bool) bool {
	if outputStarted {
		return false
	}
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) || !providerErr.Retryable {
		return false
	}
	switch providerErr.Kind {
	case llmkit.ErrorCancelled, llmkit.ErrorInvalidRequest, llmkit.ErrorAuthenticationFailed,
		llmkit.ErrorPermissionDenied, llmkit.ErrorContentFiltered, llmkit.ErrorOutcomeUnknown:
		return false
	}
	if providerIdempotent {
		return true
	}
	return providerErr.Phase == llmkit.PhaseResolveDNS || providerErr.Phase == llmkit.PhaseConnect || providerErr.Phase == llmkit.PhaseTLS
}

func Do[T any](
	ctx context.Context,
	target llmkit.Target,
	policy Policy,
	operation func(context.Context, llmkit.Target) (T, error),
) (T, error) {
	var zero T
	if policy.MaxAttempts < 1 {
		return zero, fmt.Errorf("retry: MaxAttempts must be at least 1")
	}
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		result, err := operation(ctx, target)
		if err == nil {
			return result, nil
		}
		if attempt == policy.MaxAttempts || !Safe(err, policy.ProviderIdempotent, false) {
			return zero, err
		}
		delay := retryDelay(err, policy, attempt)
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return zero, ctx.Err()
		case <-timer.C:
		}
	}
	return zero, errors.New("retry: unreachable")
}

func retryDelay(err error, policy Policy, attempt int) time.Duration {
	var providerErr *llmkit.ProviderError
	if errors.As(err, &providerErr) && providerErr.RetryAfter > 0 {
		return capDelay(providerErr.RetryAfter, policy.MaxDelay)
	}
	if policy.Backoff == nil {
		return 0
	}
	return capDelay(policy.Backoff(attempt), policy.MaxDelay)
}

func capDelay(delay, maximum time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	if maximum > 0 && delay > maximum {
		return maximum
	}
	return delay
}
