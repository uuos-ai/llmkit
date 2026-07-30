package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
	"github.com/uuos-ai/llmkit/routing"
)

type testProvider struct{ authorization string }

func (*testProvider) ID() llmkit.ProviderID { return "fake" }
func (*testProvider) Capabilities(context.Context, llmkit.Target) (llmkit.Capabilities, error) {
	return llmkit.Capabilities{Provider: "fake"}, nil
}
func (p *testProvider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.invalid", nil)
	if err := call.Credential.Apply(ctx, call.Target, request); err != nil {
		return llmkit.Response{}, err
	}
	p.authorization = request.Header.Get("Authorization")
	return llmkit.Response{Message: llmkit.Message{Role: llmkit.RoleAssistant}, Usage: llmkit.Usage{Source: llmkit.UsageReported, TotalTokens: 3}, FinishReason: llmkit.FinishStop}, nil
}

type testStores struct {
	principal identity.Principal
	audits    []managed.AuditEvent
}

func (s *testStores) ProviderOptions(_ context.Context, principal identity.Principal) (routing.OptionsResponse, error) {
	s.principal = principal
	return routing.OptionsResponse{Revision: "r1", DefaultTargetID: "default"}, nil
}
func (s *testStores) ResolveTarget(_ context.Context, principal identity.Principal, targetID string) (managed.Target, error) {
	s.principal = principal
	if targetID == "" {
		targetID = "default"
	}
	return managed.Target{ID: targetID, Target: llmkit.Target{Provider: "fake", Model: "model"}, CredentialRef: "vault:one"}, nil
}
func (*testStores) OpenCredential(context.Context, identity.Principal, string) (llmkit.CredentialHandle, func(), error) {
	handle := &testCredential{value: []byte("secret")}
	return handle, func() { clear(handle.value) }, nil
}
func (s *testStores) Append(_ context.Context, event managed.AuditEvent) error {
	s.audits = append(s.audits, event)
	return nil
}

type testCredential struct{ value []byte }

func (c *testCredential) Apply(_ context.Context, _ llmkit.Target, request *http.Request) error {
	request.Header.Set("Authorization", "Bearer "+string(c.value))
	return nil
}

func TestGatewayAuthenticatesAndResolvesDefaultAtRequestTime(t *testing.T) {
	token := []byte("0123456789abcdef0123456789abcdef")
	auth, err := identity.SingleToken(token, identity.Principal{ClientID: "client", UserID: "user", BindingVersion: 1, Scopes: map[string]struct{}{identity.ScopeInferenceExecute: {}}})
	if err != nil {
		t.Fatal(err)
	}
	registry := llmkit.NewRegistry()
	provider := &testProvider{}
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	stores := &testStores{}
	service, err := New(Config{Registry: registry, Authenticator: auth, ConfigStore: stores, SecretStore: stores, AuditStore: stores})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(generateRequest{Request: llmkit.GenerateRequest{Messages: []llmkit.Message{{Role: llmkit.RoleUser}}}})
	request := httptest.NewRequest(http.MethodPost, "/v1/generate", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || provider.authorization != "Bearer secret" {
		t.Fatalf("status=%d body=%s auth=%q", response.Code, response.Body.String(), provider.authorization)
	}
	if stores.principal.UserID != "user" || stores.principal.ClientID != "client" || len(stores.audits) != 1 || stores.audits[0].TargetID != "default" {
		t.Fatalf("principal=%#v audits=%#v", stores.principal, stores.audits)
	}
}

func TestGatewayRejectsMissingToken(t *testing.T) {
	token := []byte("0123456789abcdef0123456789abcdef")
	auth, _ := identity.SingleToken(token, identity.Principal{ClientID: "client"})
	registry := llmkit.NewRegistry()
	stores := &testStores{}
	service, _ := New(Config{Registry: registry, Authenticator: auth, ConfigStore: stores, SecretStore: stores, AuditStore: stores})
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/provider-options", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}
