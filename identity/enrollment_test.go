package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestEnrollmentTokenIsPrefixBoundAndConsumedOnce(t *testing.T) {
	token := LocalEnrollmentPrefix + "0123456789abcdef0123456789abcdef"
	digest := sha256.Sum256([]byte(token))
	store, err := NewEnrollmentTokens([]TokenRecord{{ClientID: "client", ClientInstanceID: "instance", TokenSHA256: hex.EncodeToString(digest[:]), Scopes: []string{ScopeInferenceExecute, ScopeTokensRefresh}}})
	if err != nil {
		t.Fatal(err)
	}
	principal, ok := store.Authenticate(context.Background(), []byte(token))
	if !ok || !principal.HasScope(ScopeClientsEnroll) || principal.HasScope(ScopeInferenceExecute) {
		t.Fatalf("enrollment principal=%#v ok=%v", principal, ok)
	}
	issued, err := store.Consume(principal)
	if err != nil || !issued.HasScope(ScopeInferenceExecute) || issued.HasScope(ScopeClientsEnroll) {
		t.Fatalf("issued principal=%#v err=%v", issued, err)
	}
	if _, ok := store.Authenticate(context.Background(), []byte(token)); ok {
		t.Fatal("used enrollment token authenticated again")
	}
	if _, err := store.Consume(principal); err == nil {
		t.Fatal("used enrollment token consumed again")
	}
	wrongMode := AccessPrefix(TokenLocal) + token[len(LocalEnrollmentPrefix):]
	if _, ok := store.Authenticate(context.Background(), []byte(wrongMode)); ok {
		t.Fatal("wrong token prefix accepted")
	}
}
