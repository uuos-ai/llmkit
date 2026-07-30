// Package routing defines the non-secret provider/target catalog shared by
// SDK, local-service, and gateway deployments.
package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
)

type ProviderSource string

const (
	SourceBusiness ProviderSource = "business"
	SourceCustom   ProviderSource = "custom"
)

// ProviderOption and TargetOption intentionally contain no credential field.
// Credentials are always supplied request-scoped or resolved from SecretStore.
type ProviderOption struct {
	ID          string         `json:"id"`
	DisplayName string         `json:"display_name,omitempty"`
	Source      ProviderSource `json:"source"`
	Targets     []TargetOption `json:"targets,omitempty"`
}

type TargetOption struct {
	ID           string              `json:"id"`
	DisplayName  string              `json:"display_name,omitempty"`
	Target       llmkit.Target       `json:"target"`
	Capabilities []llmkit.Capability `json:"capabilities,omitempty"`
}

type OptionsRequest struct {
	// LocalCustomProviders implements A2: disabled-sync clients may submit
	// non-secret local definitions for this process session only.
	LocalCustomProviders []ProviderOption `json:"local_custom_providers,omitempty"`
	LocalDefaultTargetID string           `json:"local_default_target_id,omitempty"`
}

type OptionsResponse struct {
	Revision        string           `json:"revision"`
	DefaultTargetID string           `json:"default_target_id,omitempty"`
	GeneratedAt     time.Time        `json:"generated_at"`
	Providers       []ProviderOption `json:"providers"`
}

type Source interface {
	Options(context.Context, identity.Principal) (OptionsResponse, error)
}

type SourceFunc func(context.Context, identity.Principal) (OptionsResponse, error)

func (f SourceFunc) Options(ctx context.Context, principal identity.Principal) (OptionsResponse, error) {
	return f(ctx, principal)
}

// SessionCatalog merges business and A2 client-local definitions, then keeps
// only the latest non-secret target snapshot per authenticated client.
type SessionCatalog struct {
	source     Source
	allowLocal bool
	mu         sync.RWMutex
	views      map[string]view
}

type view struct {
	defaultID string
	targets   map[string]llmkit.Target
}

func NewSessionCatalog(source Source) *SessionCatalog {
	return NewSessionCatalogWithPolicy(source, true)
}

func NewSessionCatalogWithPolicy(source Source, allowLocal bool) *SessionCatalog {
	return &SessionCatalog{source: source, allowLocal: allowLocal, views: make(map[string]view)}
}

func (c *SessionCatalog) Refresh(ctx context.Context, principal identity.Principal, request OptionsRequest) (OptionsResponse, error) {
	base := OptionsResponse{}
	var err error
	if c.source != nil {
		base, err = c.source.Options(ctx, principal)
		if err != nil {
			return OptionsResponse{}, err
		}
	}
	providers := append([]ProviderOption(nil), base.Providers...)
	if !c.allowLocal && len(request.LocalCustomProviders) != 0 {
		return OptionsResponse{}, errors.New("routing: client-local providers are disabled in managed sync mode")
	}
	for _, provider := range request.LocalCustomProviders {
		if provider.Source != SourceCustom {
			return OptionsResponse{}, errors.New("routing: local providers must use source=custom")
		}
		providers = append(providers, provider)
	}
	response, targets, err := normalize(providers)
	if err != nil {
		return OptionsResponse{}, err
	}
	response.DefaultTargetID = base.DefaultTargetID
	if request.LocalDefaultTargetID != "" {
		response.DefaultTargetID = request.LocalDefaultTargetID
	}
	if response.DefaultTargetID != "" {
		if _, ok := targets[response.DefaultTargetID]; !ok {
			return OptionsResponse{}, errors.New("routing: default target is not in the catalog")
		}
	}
	response.GeneratedAt = time.Now().UTC()
	response.Revision = revision(response)
	c.mu.Lock()
	c.views[principal.Key()] = view{defaultID: response.DefaultTargetID, targets: targets}
	c.mu.Unlock()
	return response, nil
}

// Resolve applies strict explicit selection. An empty ID resolves to the
// current default captured by the client's most recent refresh.
func (c *SessionCatalog) Resolve(_ context.Context, principal identity.Principal, targetID string) (llmkit.Target, string, error) {
	c.mu.RLock()
	current, ok := c.views[principal.Key()]
	c.mu.RUnlock()
	if !ok {
		return llmkit.Target{}, "", errors.New("routing: provider options must be refreshed before use")
	}
	resolvedID := targetID
	if resolvedID == "" {
		resolvedID = current.defaultID
	}
	if resolvedID == "" {
		return llmkit.Target{}, "", errors.New("routing: no default target is available")
	}
	target, ok := current.targets[resolvedID]
	if !ok {
		return llmkit.Target{}, "", errors.New("routing: target is not in the current catalog")
	}
	return target, resolvedID, nil
}

func normalize(providers []ProviderOption) (OptionsResponse, map[string]llmkit.Target, error) {
	result := append([]ProviderOption(nil), providers...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	targets := make(map[string]llmkit.Target)
	providerIDs := make(map[string]struct{})
	for i := range result {
		provider := &result[i]
		if provider.ID == "" || (provider.Source != SourceBusiness && provider.Source != SourceCustom) {
			return OptionsResponse{}, nil, errors.New("routing: provider id and valid source are required")
		}
		if _, duplicate := providerIDs[provider.ID]; duplicate {
			return OptionsResponse{}, nil, errors.New("routing: duplicate provider id")
		}
		providerIDs[provider.ID] = struct{}{}
		sort.Slice(provider.Targets, func(a, b int) bool { return provider.Targets[a].ID < provider.Targets[b].ID })
		for _, option := range provider.Targets {
			if option.ID == "" || option.Target.Provider == "" || option.Target.Model == "" {
				return OptionsResponse{}, nil, errors.New("routing: target id, provider, and model are required")
			}
			if _, duplicate := targets[option.ID]; duplicate {
				return OptionsResponse{}, nil, errors.New("routing: duplicate target id")
			}
			targets[option.ID] = option.Target
		}
	}
	return OptionsResponse{Providers: result}, targets, nil
}

// ValidateCustomProvider validates one client-supplied, non-secret catalog
// definition before a managed platform write.
func ValidateCustomProvider(provider ProviderOption) error {
	if provider.Source != SourceCustom {
		return errors.New("routing: managed custom provider must use source=custom")
	}
	_, _, err := normalize([]ProviderOption{provider})
	return err
}

func revision(response OptionsResponse) string {
	response.Revision = ""
	response.GeneratedAt = time.Time{}
	encoded, _ := json.Marshal(response)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

// RegistrySource exposes built-in adapters. Business configuration can add
// authorized model targets around these provider entries.
func RegistrySource(registry *llmkit.Registry) Source {
	return SourceFunc(func(context.Context, identity.Principal) (OptionsResponse, error) {
		response := OptionsResponse{GeneratedAt: time.Now().UTC()}
		for _, id := range registry.ProviderIDs() {
			response.Providers = append(response.Providers, ProviderOption{ID: string(id), DisplayName: string(id), Source: SourceBusiness})
		}
		response.Revision = revision(response)
		return response, nil
	})
}

// CombineSources creates one catalog view while preserving source order.
// The first non-empty default wins; SessionCatalog validates global IDs.
func CombineSources(sources ...Source) Source {
	return SourceFunc(func(ctx context.Context, principal identity.Principal) (OptionsResponse, error) {
		result := OptionsResponse{GeneratedAt: time.Now().UTC()}
		for _, source := range sources {
			if source == nil {
				continue
			}
			options, err := source.Options(ctx, principal)
			if err != nil {
				return OptionsResponse{}, err
			}
			result.Providers = append(result.Providers, options.Providers...)
			if result.DefaultTargetID == "" {
				result.DefaultTargetID = options.DefaultTargetID
			}
		}
		result.Revision = revision(result)
		return result, nil
	})
}
