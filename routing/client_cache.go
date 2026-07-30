package routing

import (
	"context"
	"errors"
	"sync"

	"github.com/uuos-ai/llmkit"
)

// Fetcher is implemented by local IPC and gateway clients. Refresh is
// deliberately caller-triggered (startup or a business scenario event); there
// is no stale background default selection.
type Fetcher interface {
	FetchProviderOptions(context.Context) (OptionsResponse, error)
}

type ClientCache struct {
	mu      sync.RWMutex
	options OptionsResponse
	targets map[string]llmkit.Target
}

func (c *ClientCache) Refresh(ctx context.Context, fetcher Fetcher) (OptionsResponse, error) {
	if fetcher == nil {
		return OptionsResponse{}, errors.New("routing: provider options fetcher is required")
	}
	options, err := fetcher.FetchProviderOptions(ctx)
	if err != nil {
		return OptionsResponse{}, err
	}
	if err := c.Replace(options); err != nil {
		return OptionsResponse{}, err
	}
	return c.Snapshot(), nil
}

// Replace atomically swaps the complete catalog. It never merges stale entries.
func (c *ClientCache) Replace(options OptionsResponse) error {
	normalized, targets, err := normalize(options.Providers)
	if err != nil {
		return err
	}
	if options.DefaultTargetID != "" {
		if _, ok := targets[options.DefaultTargetID]; !ok {
			return errors.New("routing: default target is not in the catalog")
		}
	}
	normalized.Revision = options.Revision
	normalized.DefaultTargetID = options.DefaultTargetID
	normalized.GeneratedAt = options.GeneratedAt
	c.mu.Lock()
	c.options = normalized
	c.targets = targets
	c.mu.Unlock()
	return nil
}

func (c *ClientCache) Snapshot() OptionsResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneOptions(c.options)
}

func (c *ClientCache) Resolve(targetID string) (llmkit.Target, string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	resolved := targetID
	if resolved == "" {
		resolved = c.options.DefaultTargetID
	}
	target, ok := c.targets[resolved]
	if resolved == "" || !ok {
		return llmkit.Target{}, "", errors.New("routing: target is not in the current client cache")
	}
	return target, resolved, nil
}

func cloneOptions(source OptionsResponse) OptionsResponse {
	result := source
	result.Providers = make([]ProviderOption, len(source.Providers))
	for i, provider := range source.Providers {
		result.Providers[i] = provider
		result.Providers[i].Targets = append([]TargetOption(nil), provider.Targets...)
		for j := range result.Providers[i].Targets {
			result.Providers[i].Targets[j].Capabilities = append([]llmkit.Capability(nil), provider.Targets[j].Capabilities...)
		}
	}
	return result
}
