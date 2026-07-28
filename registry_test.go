package uukit

import (
	"context"
	"testing"
)

type testAdapter struct{ id ProviderID }

func (a testAdapter) ID() ProviderID { return a.id }
func (a testAdapter) Capabilities(context.Context) (Capabilities, error) {
	return Capabilities{Provider: a.id}, nil
}
func (a testAdapter) Invoke(context.Context, Request) (Response, error) { return Response{}, nil }
func (a testAdapter) Stream(context.Context, Request, func(StreamEvent) error) (Response, error) {
	return Response{}, nil
}

func TestRegistryRejectsDuplicateProvider(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(testAdapter{id: "example"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testAdapter{id: "example"}); err == nil {
		t.Fatal("expected duplicate registration error")
	}
}
