package httpbackend

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
	"github.com/uuos-ai/llmkit/routing"
)

type tokenSourceFunc func(context.Context) ([]byte, error)

func (f tokenSourceFunc) ServiceToken(ctx context.Context) ([]byte, error) { return f(ctx) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestBackendStoreContractsAndCredentialLifecycle(t *testing.T) {
	principal := identity.Principal{ClientID: "client", UserID: "user", BindingVersion: 7}
	credentialCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer service-token" || request.Header.Get("X-LLMKit-Client-ID") != principal.ClientID || request.Header.Get("X-LLMKit-User-ID") != principal.UserID || request.Header.Get("X-LLMKit-Binding-Version") != "7" {
			t.Fatalf("missing service identity headers: %#v", request.Header)
		}
		switch request.URL.Host + request.URL.Path {
		case "config.example/v1/provider-options":
			return response(http.StatusOK, `{"revision":"r1","providers":[]}`), nil
		case "config.example/v1/targets/target-one":
			return response(http.StatusOK, `{"id":"target-one","target":{"provider":"fake","model":"model"},"credential_ref":"vault:one","provider_account":"quota-one"}`), nil
		case "secret.example/v1/credentials:open":
			credentialCalls++
			var input map[string]string
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input["credential_ref"] != "vault:one" {
				t.Fatalf("credential request=%#v err=%v", input, err)
			}
			if credentialCalls == 1 {
				return response(http.StatusOK, `{"type":"bearer","value":"c2VjcmV0"}`), nil
			}
			return response(http.StatusOK, `{"type":"header","header":"api-key","value":"YXp1cmU="}`), nil
		case "audit.example/v1/audit-events":
			return response(http.StatusNoContent, ""), nil
		case "config.example/v1/custom-providers":
			return response(http.StatusOK, `{"revision":"r2","providers":[]}`), nil
		case "config.example/v1/custom-providers/custom-one":
			return response(http.StatusOK, `{"revision":"r3","providers":[]}`), nil
		default:
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})}
	backend, err := NewWithClient("https://config.example", "https://secret.example", "https://audit.example", client, tokenSourceFunc(func(context.Context) ([]byte, error) {
		return []byte("service-token"), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	options, err := backend.ProviderOptions(context.Background(), principal)
	if err != nil || options.Revision != "r1" {
		t.Fatalf("options=%#v err=%v", options, err)
	}
	target, err := backend.ResolveTarget(context.Background(), principal, "target-one")
	if err != nil || target.ProviderAccount != "quota-one" || target.CredentialRef != "vault:one" {
		t.Fatalf("target=%#v err=%v", target, err)
	}

	handle, release, err := backend.OpenCredential(context.Background(), principal, target.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	providerRequest, _ := http.NewRequest(http.MethodPost, "https://provider.example", nil)
	if err := handle.Apply(context.Background(), llmkit.Target{}, providerRequest); err != nil || providerRequest.Header.Get("Authorization") != "Bearer secret" {
		t.Fatalf("authorization=%q err=%v", providerRequest.Header.Get("Authorization"), err)
	}
	release()
	providerRequest.Header.Del("Authorization")
	if err := handle.Apply(context.Background(), llmkit.Target{}, providerRequest); err == nil || providerRequest.Header.Get("Authorization") != "" {
		t.Fatal("released credential retained secret material")
	}

	handle, release, err = backend.OpenCredential(context.Background(), principal, target.CredentialRef)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	providerRequest, _ = http.NewRequest(http.MethodPost, "https://provider.example", nil)
	_ = handle.Apply(context.Background(), llmkit.Target{}, providerRequest)
	if providerRequest.Header.Get("api-key") != "azure" {
		t.Fatalf("api-key=%q", providerRequest.Header.Get("api-key"))
	}

	if err := backend.Append(context.Background(), managed.AuditEvent{ClientID: principal.ClientID, UserID: principal.UserID, BindingVersion: principal.BindingVersion}); err != nil {
		t.Fatal(err)
	}
	custom := managed.CustomProviderInput{Provider: routing.ProviderOption{ID: "custom-one", Source: routing.SourceCustom}, Credential: managed.CredentialInput{Type: "bearer", Value: []byte("custom-secret")}}
	if updated, err := backend.UpsertCustomProvider(context.Background(), principal, custom); err != nil || updated.Revision != "r2" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if updated, err := backend.DeleteCustomProvider(context.Background(), principal, "custom-one"); err != nil || updated.Revision != "r3" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
}

func TestBackendValidationAndStrictBoundedResponses(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{} {"trailing":true}`), nil
	})}
	if _, err := NewWithClient("http://config.example", "https://secret.example", "https://audit.example", client); err == nil {
		t.Fatal("non-HTTPS store URL was accepted")
	}
	if _, err := NewWithClient("https://config.example", "https://secret.example", "https://audit.example", nil); err == nil {
		t.Fatal("nil HTTP client was accepted")
	}
	backend, err := NewWithClient("https://config.example", "https://secret.example", "https://audit.example", client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.ProviderOptions(context.Background(), identity.Principal{}); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
	var decoded map[string]any
	if err := decodeBoundedJSON(strings.NewReader("{} "), 2, &decoded); err == nil {
		t.Fatal("oversized JSON response was accepted")
	}
	if err := decodeBoundedJSON(bytes.NewBufferString(`{"unknown":true}`), 1024, &struct{}{}); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
