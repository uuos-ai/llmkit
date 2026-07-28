package uukit

import (
	"fmt"
	"sync"
)

type Registry struct {
	mu       sync.RWMutex
	adapters map[ProviderID]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: make(map[ProviderID]Adapter)}
}

func (r *Registry) Register(adapter Adapter) error {
	if adapter == nil || adapter.ID() == "" {
		return fmt.Errorf("uukit: adapter and provider ID are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.adapters[adapter.ID()]; exists {
		return fmt.Errorf("uukit: provider %q already registered", adapter.ID())
	}
	r.adapters[adapter.ID()] = adapter
	return nil
}

func (r *Registry) Get(id ProviderID) (Adapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	adapter, ok := r.adapters[id]
	return adapter, ok
}
