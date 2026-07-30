package identity

import (
	"context"
	"testing"
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
