package localstore

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
	"github.com/uuos-ai/llmkit/routing"
	"github.com/zalando/go-keyring"
)

func TestSQLiteCatalogAndOSSecretStoreAreClientIsolated(t *testing.T) {
	keyring.MockInit()
	store, err := Open(filepath.Join(t.TempDir(), "state.sqlite"), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	principal := identity.Principal{TenantID: "tenant", ClientID: "a"}
	input := managed.CustomProviderInput{
		Provider: routing.ProviderOption{ID: "custom", Source: routing.SourceCustom, Targets: []routing.TargetOption{{
			ID: "custom-model", Target: llmkit.Target{Provider: "openai", Model: "model"},
		}}},
		Credential: managed.CredentialInput{Type: "bearer", Value: []byte("secret")},
	}
	options, err := store.UpsertCustomProvider(context.Background(), principal, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Providers) != 1 {
		t.Fatalf("options = %#v", options)
	}
	target, err := store.ResolveTarget(context.Background(), principal, "custom-model")
	if err != nil {
		t.Fatal(err)
	}
	handle, release, err := store.OpenCredential(context.Background(), principal, target.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
	if err := handle.Apply(context.Background(), target.Target, request); err != nil {
		t.Fatal(err)
	}
	release()
	if request.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
	}
	other := identity.Principal{TenantID: "tenant", ClientID: "b"}
	if _, err := store.ResolveTarget(context.Background(), other, "custom-model"); err == nil {
		t.Fatal("expected isolated target")
	}
	if _, err := store.DeleteCustomProvider(context.Background(), principal, "custom"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ResolveTarget(context.Background(), principal, "custom-model"); err == nil {
		t.Fatal("expected deleted target")
	}
}
