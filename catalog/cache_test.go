package catalog

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uuos-ai/llmkit"
)

func TestCacheCoalescesAndClones(t *testing.T) {
	cache, err := NewCache(Config{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	target := llmkit.Target{Provider: "test", Model: "model"}
	var calls atomic.Int32
	resolver := func(context.Context, llmkit.Target) (llmkit.Capabilities, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return llmkit.Capabilities{Provider: "test", Models: map[llmkit.ModelID]llmkit.ModelCapabilities{
			"model": {Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate}},
		}}, nil
	}
	var wait sync.WaitGroup
	values := make([]llmkit.Capabilities, 8)
	errorsByIndex := make([]error, len(values))
	for index := range values {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			values[index], errorsByIndex[index] = cache.Resolve(context.Background(), target, resolver)
		}(index)
	}
	wait.Wait()
	for _, resolveErr := range errorsByIndex {
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	model := values[0].Models["model"]
	model.Capabilities[0] = llmkit.CapabilityAudio
	values[0].Models["model"] = model
	again, err := cache.Resolve(context.Background(), target, resolver)
	if err != nil || !again.Supports("model", llmkit.CapabilityGenerate) {
		t.Fatalf("cached value was mutated: %#v, err=%v", again, err)
	}
}

func TestCacheExpiryAndInvalidate(t *testing.T) {
	now := time.Unix(100, 0)
	cache, err := NewCache(Config{TTL: time.Minute, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	target := llmkit.Target{Provider: "test", Model: "model"}
	var calls int
	resolver := func(context.Context, llmkit.Target) (llmkit.Capabilities, error) {
		calls++
		return llmkit.Capabilities{}, nil
	}
	_, _ = cache.Resolve(context.Background(), target, resolver)
	_, _ = cache.Resolve(context.Background(), target, resolver)
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
	now = now.Add(time.Minute)
	_, _ = cache.Resolve(context.Background(), target, resolver)
	cache.Invalidate(target)
	_, _ = cache.Resolve(context.Background(), target, resolver)
	if calls != 3 {
		t.Fatalf("calls = %d", calls)
	}
}
