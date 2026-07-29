package catalog

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uuos-ai/llmkit"
)

func TestModelCacheCoalescesWithinExplicitScope(t *testing.T) {
	cache, err := NewModelCache(Config{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	key := ModelKey{Provider: "test", Scope: "account-1", Limit: 100}
	var calls atomic.Int32
	resolver := func(context.Context) (llmkit.ModelPage, error) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		return llmkit.ModelPage{Provider: "test", Models: []llmkit.ModelInfo{{ID: "model"}}}, nil
	}
	var wait sync.WaitGroup
	pages := make([]llmkit.ModelPage, 8)
	for index := range pages {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			pages[index], _ = cache.Resolve(context.Background(), key, resolver)
		}(index)
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("resolver calls = %d", calls.Load())
	}
	pages[0].Models[0].ID = "mutated"
	again, err := cache.Resolve(context.Background(), key, resolver)
	if err != nil || again.Models[0].ID != "model" {
		t.Fatalf("cached page was mutated: %#v, err=%v", again, err)
	}
}

func TestModelCacheSeparatesCredentialScopes(t *testing.T) {
	cache, err := NewModelCache(Config{TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	resolver := func(context.Context) (llmkit.ModelPage, error) {
		calls++
		return llmkit.ModelPage{}, nil
	}
	_, _ = cache.Resolve(context.Background(), ModelKey{Provider: "test", Scope: "account-1"}, resolver)
	_, _ = cache.Resolve(context.Background(), ModelKey{Provider: "test", Scope: "account-2"}, resolver)
	if calls != 2 {
		t.Fatalf("resolver calls = %d", calls)
	}
	if _, err := cache.Resolve(context.Background(), ModelKey{Provider: "test"}, resolver); err == nil {
		t.Fatal("expected empty scope to be rejected")
	}
}
