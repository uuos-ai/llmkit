package routing

import (
	"context"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
)

func TestSessionCatalogRefreshDefaultAndIsolation(t *testing.T) {
	source := SourceFunc(func(context.Context, identity.Principal) (OptionsResponse, error) {
		return OptionsResponse{Providers: []ProviderOption{{
			ID: "business", Source: SourceBusiness,
			Targets: []TargetOption{{ID: "default-a", Target: llmkit.Target{Provider: "openai", Model: "gpt"}}},
		}}, DefaultTargetID: "default-a"}, nil
	})
	catalog := NewSessionCatalog(source)
	a := identity.Principal{ClientID: "a", UserID: "u"}
	b := identity.Principal{ClientID: "b", UserID: "u"}
	custom := ProviderOption{ID: "custom-a", Source: SourceCustom, Targets: []TargetOption{{
		ID: "target-a", Target: llmkit.Target{Provider: "openai", Model: "local", Endpoint: "https://example.test/v1"},
	}}}
	response, err := catalog.Refresh(context.Background(), a, OptionsRequest{
		LocalCustomProviders: []ProviderOption{custom}, LocalDefaultTargetID: "target-a",
	})
	if err != nil || response.DefaultTargetID != "target-a" || response.Revision == "" {
		t.Fatalf("response=%#v error=%v", response, err)
	}
	if target, id, err := catalog.Resolve(context.Background(), a, ""); err != nil || id != "target-a" || target.Model != "local" {
		t.Fatalf("target=%#v id=%q error=%v", target, id, err)
	}
	if _, _, err := catalog.Resolve(context.Background(), b, "target-a"); err == nil {
		t.Fatal("expected client-isolated catalog")
	}
}

func TestSessionCatalogRejectsDuplicateTargets(t *testing.T) {
	catalog := NewSessionCatalog(nil)
	principal := identity.Principal{ClientID: "c"}
	provider := ProviderOption{ID: "custom", Source: SourceCustom, Targets: []TargetOption{
		{ID: "same", Target: llmkit.Target{Provider: "openai", Model: "a"}},
		{ID: "same", Target: llmkit.Target{Provider: "openai", Model: "b"}},
	}}
	if _, err := catalog.Refresh(context.Background(), principal, OptionsRequest{LocalCustomProviders: []ProviderOption{provider}}); err == nil {
		t.Fatal("expected duplicate target rejection")
	}
}
