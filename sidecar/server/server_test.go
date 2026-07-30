package server

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/sidecar/protocol"
)

const testSessionKey = "0123456789abcdef0123456789abcdef"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	server, err := New(Config{
		SessionKey: []byte(testSessionKey),
		Build:      BuildInfo{SidecarVersion: "dev", LLMKitVersion: "dev", BuildID: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(server.Close)
	return server
}

func TestRecoveryHandshakeCanOnlyCallRefreshOrBind(t *testing.T) {
	principal := identity.Principal{ClientID: "client", Scopes: map[string]struct{}{identity.ScopeTokensRefresh: {}, identity.ScopeUsersBind: {}}}
	server, err := New(Config{
		Authenticator:         identity.AuthenticatorFunc(func(context.Context, []byte) (identity.Principal, bool) { return identity.Principal{}, false }),
		RecoveryAuthenticator: recoveryAuthenticatorFunc(func(context.Context, []byte) (identity.Principal, bool) { return principal, true }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Register(protocol.MethodRefreshToken, func(context.Context, json.RawMessage) (any, error) { return map[string]bool{"ok": true}, nil }); err != nil {
		t.Fatal(err)
	}
	serverSide, clientSide := net.Pipe()
	go func() { _ = server.ServeConn(context.Background(), serverSide) }()
	codec := protocol.NewCodec(clientSide, clientSide, 0)
	payload, _ := json.Marshal(protocol.HandshakeRequest{SupportedVersions: []string{protocol.Version}, Purpose: "token_recovery"})
	if err := codec.WriteRequest(protocol.Request{Version: protocol.Version, SessionKey: "stale", RequestID: "handshake", Method: protocol.MethodHandshake, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if response, err := codec.ReadResponse(); err != nil || response.Type != protocol.TypeResult {
		t.Fatalf("handshake=%#v err=%v", response, err)
	}
	if err := codec.WriteRequest(protocol.Request{Version: protocol.Version, SessionKey: "stale", RequestID: "health", Method: protocol.MethodHealth}); err != nil {
		t.Fatal(err)
	}
	if response, err := codec.ReadResponse(); err != nil || response.Type != protocol.TypeError {
		t.Fatalf("health=%#v err=%v", response, err)
	}
	if err := codec.WriteRequest(protocol.Request{Version: protocol.Version, SessionKey: "stale", RequestID: "refresh", Method: protocol.MethodRefreshToken}); err != nil {
		t.Fatal(err)
	}
	if response, err := codec.ReadResponse(); err != nil || response.Type != protocol.TypeResult {
		t.Fatalf("refresh=%#v err=%v", response, err)
	}
	_ = clientSide.Close()
}

type recoveryAuthenticatorFunc func(context.Context, []byte) (identity.Principal, bool)

func (f recoveryAuthenticatorFunc) AuthenticateRecovery(ctx context.Context, token []byte) (identity.Principal, bool) {
	return f(ctx, token)
}

func handshake(t *testing.T, codec *protocol.Codec, key string) protocol.Response {
	t.Helper()
	payload, err := json.Marshal(protocol.HandshakeRequest{SupportedVersions: []string{protocol.Version}})
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.WriteRequest(protocol.Request{
		Version: protocol.Version, SessionKey: key,
		RequestID: "handshake", Method: protocol.MethodHandshake, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	response, err := codec.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestHandshakeAndHealth(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	server := newTestServer(t)
	done := make(chan error, 1)
	go func() { done <- server.ServeConn(context.Background(), serverSide) }()
	codec := protocol.NewCodec(clientSide, clientSide, 0)
	response := handshake(t, codec, testSessionKey)
	if response.Type != protocol.TypeResult {
		t.Fatalf("handshake = %#v", response)
	}
	if err := codec.WriteRequest(protocol.Request{
		Version: protocol.Version, SessionKey: testSessionKey,
		RequestID: "health", Method: protocol.MethodHealth,
	}); err != nil {
		t.Fatal(err)
	}
	response, err := codec.ReadResponse()
	if err != nil {
		t.Fatal(err)
	}
	if response.RequestID != "health" || response.Type != protocol.TypeResult {
		t.Fatalf("health = %#v", response)
	}
	_ = clientSide.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not close")
	}
}

func TestHandshakeFailsClosed(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	server := newTestServer(t)
	done := make(chan error, 1)
	go func() { done <- server.ServeConn(context.Background(), serverSide) }()
	codec := protocol.NewCodec(clientSide, clientSide, 0)
	response := handshake(t, codec, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	if response.Type != protocol.TypeError || response.Error == nil || response.Error.Kind != "authentication" {
		t.Fatalf("response = %#v", response)
	}
	_ = clientSide.Close()
	if err := <-done; err == nil {
		t.Fatal("expected handshake failure")
	}
}

func TestCancelStopsActiveHandler(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	server := newTestServer(t)
	started := make(chan struct{})
	if err := server.Register(protocol.MethodGenerate, func(ctx context.Context, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ServeConn(context.Background(), serverSide) }()
	codec := protocol.NewCodec(clientSide, clientSide, 0)
	_ = handshake(t, codec, testSessionKey)
	if err := codec.WriteRequest(protocol.Request{
		Version: protocol.Version, SessionKey: testSessionKey,
		RequestID: "generate", Method: protocol.MethodGenerate,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	payload, _ := json.Marshal(protocol.CancelRequest{RequestID: "generate"})
	if err := codec.WriteRequest(protocol.Request{
		Version: protocol.Version, SessionKey: testSessionKey,
		RequestID: "cancel", Method: protocol.MethodCancel, Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]protocol.Response{}
	for len(seen) < 2 {
		response, err := codec.ReadResponse()
		if err != nil {
			t.Fatal(err)
		}
		seen[response.RequestID] = response
	}
	if seen["cancel"].Type != protocol.TypeResult || seen["generate"].Type != protocol.TypeError {
		t.Fatalf("responses = %#v", seen)
	}
	_ = clientSide.Close()
}
