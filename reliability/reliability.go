// Package reliability provides provider-neutral attempt observations and
// advisory health signals. It never blocks calls or changes routing.
package reliability

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/uuos-ai/llmkit"
)

type Operation string

const (
	OperationGenerate Operation = "generate"
	OperationStream   Operation = "stream"
	OperationEmbed    Operation = "embed"
)

type Sample struct {
	Target     llmkit.Target
	Operation  Operation
	StartedAt  time.Time
	Duration   time.Duration
	Succeeded  bool
	ErrorKind  llmkit.ErrorKind
	Retryable  bool
	StatusCode int
	Usage      llmkit.Usage
}

// Observer implementations must return quickly. Run invokes observers
// synchronously so hosts can choose their own buffering and backpressure.
type Observer interface {
	Observe(context.Context, Sample)
}

type ObserverFunc func(context.Context, Sample)

func (f ObserverFunc) Observe(ctx context.Context, sample Sample) { f(ctx, sample) }

// Run records exactly one attempt. It does not retry or alter the Target.
func Run[T any](
	ctx context.Context,
	target llmkit.Target,
	operation Operation,
	observer Observer,
	attempt func(context.Context) (T, llmkit.Usage, error),
) (T, error) {
	started := time.Now()
	result, usage, err := attempt(ctx)
	sample := Sample{
		Target: target, Operation: operation, StartedAt: started,
		Duration: time.Since(started), Succeeded: err == nil, Usage: usage,
	}
	var providerErr *llmkit.ProviderError
	if errors.As(err, &providerErr) {
		sample.ErrorKind = providerErr.Kind
		sample.Retryable = providerErr.Retryable
		sample.StatusCode = providerErr.StatusCode
	}
	if observer != nil {
		observer.Observe(ctx, sample)
	}
	return result, err
}

type Health string

const (
	HealthUnknown     Health = "unknown"
	HealthHealthy     Health = "healthy"
	HealthDegraded    Health = "degraded"
	HealthUnavailable Health = "unavailable"
)

type TrackerConfig struct {
	UnavailableAfter int
}

type Snapshot struct {
	Target                    llmkit.Target
	Health                    Health
	TotalAttempts             uint64
	TotalFailures             uint64
	ConsecutiveRetryableFails int
	LastSuccess               time.Time
	LastFailure               time.Time
	LastErrorKind             llmkit.ErrorKind
}

type Tracker struct {
	mu               sync.RWMutex
	unavailableAfter int
	byTarget         map[llmkit.Target]Snapshot
}

func NewTracker(config TrackerConfig) *Tracker {
	threshold := config.UnavailableAfter
	if threshold < 1 {
		threshold = 3
	}
	return &Tracker{
		unavailableAfter: threshold,
		byTarget:         make(map[llmkit.Target]Snapshot),
	}
}

// Observe updates an advisory signal only. The host remains responsible for
// deciding whether a target may receive another attempt.
func (t *Tracker) Observe(_ context.Context, sample Sample) {
	t.mu.Lock()
	defer t.mu.Unlock()
	snapshot := t.byTarget[sample.Target]
	snapshot.Target = sample.Target
	snapshot.TotalAttempts++
	if sample.Succeeded {
		snapshot.Health = HealthHealthy
		snapshot.ConsecutiveRetryableFails = 0
		snapshot.LastSuccess = sample.StartedAt.Add(sample.Duration)
		snapshot.LastErrorKind = ""
	} else {
		snapshot.TotalFailures++
		snapshot.LastFailure = sample.StartedAt.Add(sample.Duration)
		snapshot.LastErrorKind = sample.ErrorKind
		if sample.Retryable {
			snapshot.ConsecutiveRetryableFails++
		} else {
			snapshot.ConsecutiveRetryableFails = 0
		}
		if snapshot.ConsecutiveRetryableFails >= t.unavailableAfter {
			snapshot.Health = HealthUnavailable
		} else {
			snapshot.Health = HealthDegraded
		}
	}
	t.byTarget[sample.Target] = snapshot
}

func (t *Tracker) Snapshot(target llmkit.Target) Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	snapshot, ok := t.byTarget[target]
	if !ok {
		return Snapshot{Target: target, Health: HealthUnknown}
	}
	return snapshot
}

var _ Observer = (*Tracker)(nil)
