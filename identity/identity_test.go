package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStaticTokensAuthenticateAndIsolate(t *testing.T) {
	tokenA := []byte("0123456789abcdef0123456789abcdef")
	tokenB := []byte("abcdef0123456789abcdef0123456789")
	store, err := NewStaticTokens([]TokenRecord{
		{ClientID: "client-a", UserID: "user-a", TokenSHA256: HashToken(tokenA), Scopes: []string{"generate"}},
		{ClientID: "client-b", UserID: "user-b", TokenSHA256: HashToken(tokenB)},
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, ok := store.Authenticate(context.Background(), tokenA)
	if !ok || principal.UserID != "user-a" || !principal.HasScope("generate") {
		t.Fatalf("principal = %#v, ok=%v", principal, ok)
	}
	if _, ok := store.Authenticate(context.Background(), []byte("wrong")); ok {
		t.Fatal("invalid token authenticated")
	}
}

func TestPrincipalContextClonesScopes(t *testing.T) {
	original := Principal{ClientID: "client", Scopes: map[string]struct{}{"generate": {}}}
	ctx := WithPrincipal(context.Background(), original)
	delete(original.Scopes, "generate")
	stored, ok := FromContext(ctx)
	if !ok || !stored.HasScope("generate") {
		t.Fatalf("stored principal = %#v", stored)
	}
}

func TestSingleTokenDestroyInvalidatesToken(t *testing.T) {
	token := []byte("0123456789abcdef0123456789abcdef")
	authenticator, err := SingleToken(token, Principal{ClientID: "client"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := authenticator.Authenticate(context.Background(), token); !ok {
		t.Fatal("token did not authenticate")
	}
	authenticator.(interface{ Destroy() }).Destroy()
	if _, ok := authenticator.Authenticate(context.Background(), token); ok {
		t.Fatal("destroyed token authenticated")
	}
}

func TestBindingAuthenticatorFencesAndEnrichesPrincipals(t *testing.T) {
	token := []byte("1123456789abcdef0123456789abcdef")
	store := NewMemoryUserBindingStore(nil)
	_, current, err := store.Bind(context.Background(), BindRequest{ClientID: "client", UserID: "current"})
	if err != nil {
		t.Fatal(err)
	}
	base, _ := SingleToken(token, Principal{ClientID: "client", Scopes: map[string]struct{}{ScopeInferenceExecute: {}}})
	authenticator := BindingAuthenticator{Base: base, Store: store}
	principal, ok := authenticator.Authenticate(context.Background(), token)
	if !ok || principal.UserID != "current" || principal.BindingVersion != current.BindingVersion || !principal.HasScope(ScopeInferenceExecute) {
		t.Fatalf("principal=%#v ok=%v", principal, ok)
	}

	stale, _ := SingleToken(token, Principal{ClientID: "client", UserID: "old", BindingVersion: current.BindingVersion})
	authenticator.Base = stale
	if _, ok := authenticator.Authenticate(context.Background(), token); ok {
		t.Fatal("stale user binding authenticated")
	}
	staleVersion, _ := SingleToken(token, Principal{ClientID: "client", UserID: "current", BindingVersion: current.BindingVersion + 1})
	authenticator.Base = staleVersion
	if _, ok := authenticator.Authenticate(context.Background(), token); ok {
		t.Fatal("stale binding version authenticated")
	}
}

func TestBindingAuthenticatorFailsClosedOnStoreError(t *testing.T) {
	token := []byte("2123456789abcdef0123456789abcdef")
	base, _ := SingleToken(token, Principal{ClientID: "client"})
	store := failingBindingStore{}
	if _, ok := (BindingAuthenticator{Base: base, Store: store}).Authenticate(context.Background(), token); ok {
		t.Fatal("binding store error authenticated a token")
	}
}

func TestBindingAuthenticatorRevokesInFlightPrincipalOnRebind(t *testing.T) {
	store := NewMemoryUserBindingStore(nil)
	_, current, err := store.Bind(context.Background(), BindRequest{ClientID: "client", UserID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	authenticator := BindingAuthenticator{Store: store}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	revoked, err := authenticator.WatchRevocation(ctx, Principal{
		ClientID: "client", UserID: current.UserID, BindingVersion: current.BindingVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-revoked:
		t.Fatal("principal revoked before the binding changed")
	default:
	}
	if _, _, err := store.Bind(context.Background(), BindRequest{
		ClientID: "client", UserID: "second", ExpectedBindingVersion: current.BindingVersion,
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-revoked:
	case <-time.After(time.Second):
		t.Fatal("principal was not revoked after rebind")
	}
}

type failingBindingStore struct{}

func (failingBindingStore) Current(context.Context, string) (UserBinding, bool, error) {
	return UserBinding{}, false, errors.New("unavailable")
}
func (failingBindingStore) Bind(context.Context, BindRequest) (UserBinding, UserBinding, error) {
	return UserBinding{}, UserBinding{}, errors.New("unavailable")
}
func (failingBindingStore) Watch(context.Context, string, uint64) (<-chan UserBinding, error) {
	return nil, errors.New("unavailable")
}
