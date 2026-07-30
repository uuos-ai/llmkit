package llmkit

import (
	"fmt"
	"sort"
	"sync"
)

type Registry struct {
	mu        sync.RWMutex
	providers map[ProviderID]Provider
}

func NewRegistry() *Registry {
	return &Registry{providers: make(map[ProviderID]Provider)}
}

func (r *Registry) Register(provider Provider) error {
	if provider == nil || provider.ID() == "" {
		return fmt.Errorf("llmkit: provider and provider ID are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.providers[provider.ID()]; exists {
		return fmt.Errorf("llmkit: provider %q already registered", provider.ID())
	}
	r.providers[provider.ID()] = provider
	return nil
}

func (r *Registry) Get(id ProviderID) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	provider, ok := r.providers[id]
	return provider, ok
}

// ProviderIDs returns a stable snapshot suitable for discovery APIs. The
// returned slice is sorted and cannot mutate the registry.
func (r *Registry) ProviderIDs() []ProviderID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]ProviderID, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (r *Registry) Generator(id ProviderID) (Generator, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	generator, ok := provider.(Generator)
	return generator, ok
}

func (r *Registry) StreamGenerator(id ProviderID) (StreamGenerator, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	generator, ok := provider.(StreamGenerator)
	return generator, ok
}

func (r *Registry) Embedder(id ProviderID) (Embedder, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	embedder, ok := provider.(Embedder)
	return embedder, ok
}

func (r *Registry) Reranker(id ProviderID) (Reranker, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	reranker, ok := provider.(Reranker)
	return reranker, ok
}

func (r *Registry) Moderator(id ProviderID) (Moderator, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	moderator, ok := provider.(Moderator)
	return moderator, ok
}

func (r *Registry) CredentialValidator(id ProviderID) (CredentialValidator, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	validator, ok := provider.(CredentialValidator)
	return validator, ok
}

func (r *Registry) ModelLister(id ProviderID) (ModelLister, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	lister, ok := provider.(ModelLister)
	return lister, ok
}
