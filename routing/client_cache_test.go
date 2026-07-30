package routing

import (
	"context"
	"testing"

	"github.com/uuos-ai/llmkit"
)

type fetcherFunc func(context.Context) (OptionsResponse, error)

func (f fetcherFunc) FetchProviderOptions(ctx context.Context) (OptionsResponse, error) {
	return f(ctx)
}

func TestClientCacheRefreshReplacesDynamicDefault(t *testing.T) {
	cache := &ClientCache{}
	model := "a"
	fetcher := fetcherFunc(func(context.Context) (OptionsResponse, error) {
		return OptionsResponse{Revision: model, DefaultTargetID: model, Providers: []ProviderOption{{
			ID: "p", Source: SourceBusiness, Targets: []TargetOption{{ID: model, Target: llmkit.Target{Provider: "openai", Model: llmkit.ModelID(model)}}},
		}}}, nil
	})
	if _, err := cache.Refresh(context.Background(), fetcher); err != nil {
		t.Fatal(err)
	}
	model = "b"
	if _, err := cache.Refresh(context.Background(), fetcher); err != nil {
		t.Fatal(err)
	}
	if _, id, err := cache.Resolve(""); err != nil || id != "b" {
		t.Fatalf("id=%q error=%v", id, err)
	}
	if _, _, err := cache.Resolve("a"); err == nil {
		t.Fatal("stale target must be replaced")
	}
}
