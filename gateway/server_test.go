package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/blobstore"
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
	return llmkit.Response{Message: llmkit.Message{Role: llmkit.RoleAssistant, Parts: []llmkit.ContentPart{{Type: llmkit.ContentText, Text: "hello"}}}, Usage: llmkit.Usage{Source: llmkit.UsageReported, InputTokens: 1, OutputTokens: 2, TotalTokens: 3}, FinishReason: llmkit.FinishStop}, nil
}
func (p *testProvider) Embed(ctx context.Context, call llmkit.EmbedCall) (llmkit.EmbedResponse, error) {
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://provider.invalid", nil)
	if err := call.Credential.Apply(ctx, call.Target, request); err != nil {
		return llmkit.EmbedResponse{}, err
	}
	p.authorization = request.Header.Get("Authorization")
	return llmkit.EmbedResponse{Vectors: [][]float32{{1, 2}}, Usage: llmkit.Usage{Source: llmkit.UsageReported, InputTokens: 1, TotalTokens: 1}}, nil
}
func (*testProvider) Stream(context.Context, llmkit.GenerateCall) (llmkit.EventStream, error) {
	return &testEventStream{events: []llmkit.StreamEvent{{Type: llmkit.EventTextDelta, Text: "hello"}, {Type: llmkit.EventFinish, FinishReason: llmkit.FinishStop}}}, nil
}

type testEventStream struct {
	events []llmkit.StreamEvent
	index  int
}

func (s *testEventStream) Recv() (llmkit.StreamEvent, error) {
	if s.index == len(s.events) {
		return llmkit.StreamEvent{}, io.EOF
	}
	event := s.events[s.index]
	s.index++
	return event, nil
}
func (*testEventStream) Close() error { return nil }

type testStores struct {
	principal identity.Principal
	audits    []managed.AuditEvent
	binding   identity.UserBinding
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
func (s *testStores) Current(_ context.Context, clientID string) (identity.UserBinding, bool, error) {
	return s.binding, s.binding.ClientID == clientID, nil
}
func (s *testStores) Bind(_ context.Context, request identity.BindRequest) (identity.UserBinding, identity.UserBinding, error) {
	previous := s.binding
	s.binding = identity.UserBinding{ClientID: request.ClientID, UserID: request.UserID, BindingVersion: previous.BindingVersion + 1}
	return previous, s.binding, nil
}
func (*testStores) Watch(context.Context, string, uint64) (<-chan identity.UserBinding, error) {
	updates := make(chan identity.UserBinding)
	close(updates)
	return updates, nil
}
func (*testStores) GetSession(context.Context, string) (managed.Session, bool, error) {
	return managed.Session{}, false, nil
}
func (*testStores) ReplaceSession(_ context.Context, session managed.Session, _ uint64) (managed.Session, error) {
	return session, nil
}
func (*testStores) RevokeSession(context.Context, string, uint64, string) error { return nil }
func (*testStores) Reserve(context.Context, managed.RateLimitRequest) (managed.RateLimitLease, error) {
	return managed.RateLimitLease{LeaseID: "lease", Allowed: true}, nil
}
func (*testStores) Commit(context.Context, string, managed.RateLimitUsage) error { return nil }
func (*testStores) Release(context.Context, string) error                        { return nil }

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
	stores := &testStores{binding: identity.UserBinding{ClientID: "client", UserID: "user", BindingVersion: 1}}
	service, err := newTestServer(registry, auth, stores)
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
	service, _ := newTestServer(registry, auth, stores)
	response := httptest.NewRecorder()
	service.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v1/provider-options", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestGatewayBlobUploadUsesBoundPrincipal(t *testing.T) {
	token := []byte("abcdef0123456789abcdef0123456789")
	auth, _ := identity.SingleToken(token, identity.Principal{ClientID: "client", UserID: "user", BindingVersion: 3, Scopes: map[string]struct{}{identity.ScopeBlobsWrite: {}}})
	registry := llmkit.NewRegistry()
	stores := &testStores{binding: identity.UserBinding{ClientID: "client", UserID: "user", BindingVersion: 3}}
	blobs, _ := blobstore.NewMemory(blobstore.Config{})
	service, err := newTestServer(registry, auth, stores, func(config *Config) { config.BlobStore = blobs })
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/blobs", bytes.NewBufferString("image"))
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set("Content-Type", "image/png")
	response := httptest.NewRecorder()
	service.DataHandler().ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var metadata managed.BlobMetadata
	if err := json.Unmarshal(response.Body.Bytes(), &metadata); err != nil || metadata.Ref == "" || metadata.SizeBytes != 5 {
		t.Fatalf("metadata=%#v err=%v", metadata, err)
	}
}

func TestDataAndControlHandlersArePhysicallySeparated(t *testing.T) {
	token := []byte("fedcba9876543210fedcba9876543210")
	auth, _ := identity.SingleToken(token, identity.Principal{ClientID: "client", Scopes: map[string]struct{}{identity.ScopeProvidersWrite: {}, identity.ScopeCredentialsWrite: {}, identity.ScopeInferenceExecute: {}}})
	stores := &testStores{}
	service, err := newTestServer(llmkit.NewRegistry(), auth, stores)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPut, "/v1/custom-providers", bytes.NewBufferString(`{}`))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response := httptest.NewRecorder()
	service.DataHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("control route exposed on data plane: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/generate", bytes.NewBufferString(`{}`))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response = httptest.NewRecorder()
	service.ControlHandler().ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("data route exposed on control plane: %d", response.Code)
	}
}

func TestOpenAIChatAndEmbeddingCompatibility(t *testing.T) {
	token := []byte("00112233445566778899aabbccddeeff")
	auth, _ := identity.SingleToken(token, identity.Principal{ClientID: "client", UserID: "user", BindingVersion: 2, Scopes: map[string]struct{}{identity.ScopeInferenceExecute: {}}})
	registry := llmkit.NewRegistry()
	provider := &testProvider{}
	if err := registry.Register(provider); err != nil {
		t.Fatal(err)
	}
	stores := &testStores{binding: identity.UserBinding{ClientID: "client", UserID: "user", BindingVersion: 2}}
	service, err := newTestServer(registry, auth, stores)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(`{"model":"llmkit-default","messages":[{"role":"user","content":"hi"}]}`))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response := httptest.NewRecorder()
	service.DataHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"object":"chat.completion"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"content":"hello"`)) {
		t.Fatalf("chat status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewBufferString(`{"model":"llmkit-default","input":"hi"}`))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response = httptest.NewRecorder()
	service.DataHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"object":"embedding"`)) {
		t.Fatalf("embedding status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestOpenAIResponsesStreamingCompatibility(t *testing.T) {
	token := []byte("10112233445566778899aabbccddeef0")
	auth, _ := identity.SingleToken(token, identity.Principal{ClientID: "client", UserID: "user", BindingVersion: 2, Scopes: map[string]struct{}{identity.ScopeInferenceExecute: {}}})
	registry := llmkit.NewRegistry()
	if err := registry.Register(&testProvider{}); err != nil {
		t.Fatal(err)
	}
	stores := &testStores{binding: identity.UserBinding{ClientID: "client", UserID: "user", BindingVersion: 2}}
	service, err := newTestServer(registry, auth, stores)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(`{"model":"llmkit-default","input":"hi","stream":true}`))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response := httptest.NewRecorder()
	service.DataHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("response.output_text.delta")) || !bytes.Contains(response.Body.Bytes(), []byte("response.completed")) || !bytes.Contains(response.Body.Bytes(), []byte("[DONE]")) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestGatewayRejectsStaleUserBinding(t *testing.T) {
	token := []byte("ffeeddccbbaa99887766554433221100")
	auth, _ := identity.SingleToken(token, identity.Principal{ClientID: "client", UserID: "old", BindingVersion: 1, Scopes: map[string]struct{}{identity.ScopeInferenceExecute: {}}})
	stores := &testStores{binding: identity.UserBinding{ClientID: "client", UserID: "new", BindingVersion: 2}}
	service, err := newTestServer(llmkit.NewRegistry(), auth, stores)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/generate", bytes.NewBufferString(`{"request":{"messages":[]}}`))
	request.Header.Set("Authorization", "Bearer "+string(token))
	response := httptest.NewRecorder()
	service.DataHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !bytes.Contains(response.Body.Bytes(), []byte("user_binding_changed")) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func newTestServer(registry *llmkit.Registry, auth identity.Authenticator, stores *testStores, options ...func(*Config)) (*Server, error) {
	config := Config{Registry: registry, Authenticator: auth, ConfigStore: stores, SecretStore: stores, AuditStore: stores, SessionStore: stores, BindingStore: stores, RateLimitStore: stores}
	for _, option := range options {
		option(&config)
	}
	return New(config)
}
