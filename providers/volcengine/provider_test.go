package volcengine

import (
	"context"
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestProfileCapabilities(t *testing.T) {
	provider, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	target := llmkit.Target{Provider: DefaultProviderID, Model: "doubao-seed"}
	capabilities, err := provider.Capabilities(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !capabilities.Supports(target.Model, llmkit.CapabilityStructured) ||
		capabilities.Supports(target.Model, llmkit.CapabilityEmbedding) {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}
