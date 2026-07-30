package tokenhub

import (
	"context"
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestDedicatedProfile(t *testing.T) {
	provider, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	capabilities, err := provider.Capabilities(context.Background(), llmkit.Target{Provider: DefaultProviderID, Model: "configured-model"})
	if err != nil || !capabilities.Supports("configured-model", llmkit.CapabilityStreaming) {
		t.Fatalf("capabilities=%#v err=%v", capabilities, err)
	}
}
