// Package catalog caches host-requested Provider capability discovery. It does
// not fetch or select models on its own.
package catalog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/uuos-ai/llmkit"
)

type Resolver func(context.Context, llmkit.Target) (llmkit.Capabilities, error)

type Config struct {
	TTL time.Duration
	Now func() time.Time
}

type entry struct {
	value     llmkit.Capabilities
	expiresAt time.Time
}

type flight struct {
	done  chan struct{}
	value llmkit.Capabilities
	err   error
}

type Cache struct {
	mu       sync.Mutex
	ttl      time.Duration
	now      func() time.Time
	entries  map[llmkit.Target]entry
	inflight map[llmkit.Target]*flight
}

func NewCache(config Config) (*Cache, error) {
	if config.TTL <= 0 {
		return nil, fmt.Errorf("catalog: TTL must be positive")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &Cache{
		ttl: config.TTL, now: now,
		entries: make(map[llmkit.Target]entry), inflight: make(map[llmkit.Target]*flight),
	}, nil
}

// Resolve caches only successful results and coalesces concurrent resolutions
// for the same immutable Target.
func (c *Cache) Resolve(ctx context.Context, target llmkit.Target, resolver Resolver) (llmkit.Capabilities, error) {
	if resolver == nil {
		return llmkit.Capabilities{}, fmt.Errorf("catalog: resolver is required")
	}
	c.mu.Lock()
	if cached, ok := c.entries[target]; ok && c.now().Before(cached.expiresAt) {
		value := cloneCapabilities(cached.value)
		c.mu.Unlock()
		return value, nil
	}
	if pending, ok := c.inflight[target]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return llmkit.Capabilities{}, ctx.Err()
		case <-pending.done:
			return cloneCapabilities(pending.value), pending.err
		}
	}
	pending := &flight{done: make(chan struct{})}
	c.inflight[target] = pending
	c.mu.Unlock()

	value, err := resolver(ctx, target)
	value = cloneCapabilities(value)
	c.mu.Lock()
	pending.value, pending.err = value, err
	if err == nil {
		c.entries[target] = entry{value: value, expiresAt: c.now().Add(c.ttl)}
	}
	delete(c.inflight, target)
	close(pending.done)
	c.mu.Unlock()
	return cloneCapabilities(value), err
}

func (c *Cache) Invalidate(target llmkit.Target) {
	c.mu.Lock()
	delete(c.entries, target)
	c.mu.Unlock()
}

func (c *Cache) Clear() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

func cloneCapabilities(source llmkit.Capabilities) llmkit.Capabilities {
	result := llmkit.Capabilities{Provider: source.Provider}
	if source.Models == nil {
		return result
	}
	result.Models = make(map[llmkit.ModelID]llmkit.ModelCapabilities, len(source.Models))
	for model, capabilities := range source.Models {
		capabilities.Capabilities = append([]llmkit.Capability(nil), capabilities.Capabilities...)
		result.Models[model] = capabilities
	}
	return result
}
