package llmkit

import (
	"fmt"
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

func (r *Registry) CredentialValidator(id ProviderID) (CredentialValidator, bool) {
	provider, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	validator, ok := provider.(CredentialValidator)
	return validator, ok
}
