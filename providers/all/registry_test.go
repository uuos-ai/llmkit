package all

import (
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestEveryBuiltInProviderHasValidMaturityManifest(t *testing.T) {
	registry, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range registry.ProviderIDs() {
		provider, _ := registry.Get(id)
		manifestProvider, ok := provider.(llmkit.ManifestProvider)
		if !ok {
			t.Errorf("provider %q has no manifest", id)
			continue
		}
		manifest := manifestProvider.Manifest()
		if err := manifest.Validate(); err != nil {
			t.Errorf("provider %q manifest: %v", id, err)
		}
		if manifest.ProviderID != id || manifest.Maturity == llmkit.AdapterExperimental {
			t.Errorf("provider %q manifest mismatch: %#v", id, manifest)
		}
	}
}
