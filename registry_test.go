package llmkit

import (
	"context"
	"testing"
)

type testProvider struct{ id ProviderID }

func (p testProvider) ID() ProviderID { return p.id }
func (p testProvider) Capabilities(context.Context, Target) (Capabilities, error) {
	return Capabilities{Provider: p.id}, nil
}
func (p testProvider) Generate(context.Context, GenerateCall) (Response, error) {
	return Response{}, nil
}

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testProvider{id: "example"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testProvider{id: "example"}); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}

func TestRegistryFindsOptionalCapability(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testProvider{id: "example"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Generator("example"); !ok {
		t.Fatal("expected generator capability")
	}
	if _, ok := r.StreamGenerator("example"); ok {
		t.Fatal("provider unexpectedly implements streaming")
	}
	if _, ok := r.Embedder("missing"); ok {
		t.Fatal("missing provider unexpectedly implements embeddings")
	}
}
