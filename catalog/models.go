package catalog

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/uuos-ai/llmkit"
)

// ModelKey is host-defined and intentionally excludes credentials. Scope must
// identify the host authorization/credential visibility boundary without
// containing a secret (for example, an internal account UUID).
type ModelKey struct {
	Provider llmkit.ProviderID
	Region   string
	Endpoint string
	Scope    string
	Cursor   string
	Limit    int
}

type ModelResolver func(context.Context) (llmkit.ModelPage, error)

type modelEntry struct {
	value     llmkit.ModelPage
	expiresAt time.Time
}

type modelFlight struct {
	done  chan struct{}
	value llmkit.ModelPage
	err   error
}

// ModelCache coalesces only explicit host-triggered catalog refreshes. A
// caller must supply a non-secret Scope so results are never shared across
// credential or authorization boundaries by accident.
type ModelCache struct {
	mu       sync.Mutex
	ttl      time.Duration
	now      func() time.Time
	entries  map[ModelKey]modelEntry
	inflight map[ModelKey]*modelFlight
}

func NewModelCache(config Config) (*ModelCache, error) {
	if config.TTL <= 0 {
		return nil, fmt.Errorf("catalog: TTL must be positive")
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	return &ModelCache{
		ttl: config.TTL, now: now,
		entries: make(map[ModelKey]modelEntry), inflight: make(map[ModelKey]*modelFlight),
	}, nil
}

func (c *ModelCache) Resolve(ctx context.Context, key ModelKey, resolver ModelResolver) (llmkit.ModelPage, error) {
	if key.Provider == "" || key.Scope == "" {
		return llmkit.ModelPage{}, fmt.Errorf("catalog: provider and non-secret scope are required")
	}
	if resolver == nil {
		return llmkit.ModelPage{}, fmt.Errorf("catalog: model resolver is required")
	}
	c.mu.Lock()
	if cached, ok := c.entries[key]; ok && c.now().Before(cached.expiresAt) {
		value := cloneModelPage(cached.value)
		c.mu.Unlock()
		return value, nil
	}
	if pending, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return llmkit.ModelPage{}, ctx.Err()
		case <-pending.done:
			return cloneModelPage(pending.value), pending.err
		}
	}
	pending := &modelFlight{done: make(chan struct{})}
	c.inflight[key] = pending
	c.mu.Unlock()

	value, err := resolver(ctx)
	value = cloneModelPage(value)
	c.mu.Lock()
	pending.value, pending.err = value, err
	if err == nil {
		c.entries[key] = modelEntry{value: value, expiresAt: c.now().Add(c.ttl)}
	}
	delete(c.inflight, key)
	close(pending.done)
	c.mu.Unlock()
	return cloneModelPage(value), err
}

func (c *ModelCache) Invalidate(key ModelKey) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *ModelCache) Clear() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

func cloneModelPage(source llmkit.ModelPage) llmkit.ModelPage {
	source.Models = append([]llmkit.ModelInfo(nil), source.Models...)
	return source
}
