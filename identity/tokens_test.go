package identity

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTokenRefreshRotatesBothAndSupportsExactRetry(t *testing.T) {
	now := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	manager, err := NewTokenManager(TokenManagerConfig{Mode: TokenGateway, Pepper: []byte(strings.Repeat("p", 32)), AccessTTL: 15 * time.Minute, FamilyTTL: 12 * time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := manager.Issue(Principal{ClientID: "business", UserID: "user", BindingVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pair.AccessToken, "llmk_g1_") || !strings.HasPrefix(pair.RefreshToken, "llmk_gr1_") {
		t.Fatalf("prefixes: %#v", pair)
	}
	next, err := manager.Refresh(pair.RefreshToken, "idem")
	if err != nil || next.AccessToken == pair.AccessToken || next.RefreshToken == pair.RefreshToken {
		t.Fatalf("refresh = %#v, %v", next, err)
	}
	if _, ok := manager.Authenticate(context.Background(), []byte(pair.AccessToken)); ok {
		t.Fatal("old access remained valid")
	}
	retry, err := manager.Refresh(pair.RefreshToken, "idem")
	if err != nil || retry.AccessToken != next.AccessToken || retry.RefreshToken != next.RefreshToken {
		t.Fatalf("idempotent retry = %#v, %v", retry, err)
	}
	if _, err := manager.Refresh(pair.RefreshToken, "different"); !errors.Is(err, ErrTokenReplay) {
		t.Fatalf("replay error = %v", err)
	}
	if _, ok := manager.Authenticate(context.Background(), []byte(next.AccessToken)); ok {
		t.Fatal("replay did not revoke family")
	}
}

func TestRefreshExpiresWithAccess(t *testing.T) {
	now := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	manager, _ := NewTokenManager(TokenManagerConfig{Mode: TokenLocal, Pepper: []byte(strings.Repeat("p", 32)), AccessTTL: time.Minute, FamilyTTL: time.Hour, Now: func() time.Time { return now }})
	pair, _ := manager.Issue(Principal{ClientID: "business"})
	now = now.Add(time.Minute)
	if _, err := manager.Refresh(pair.RefreshToken, "idem"); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("error = %v", err)
	}
}

func TestReplaceClientPrincipalRevokesEveryOldBindingFamily(t *testing.T) {
	manager, err := NewTokenManager(TokenManagerConfig{Mode: TokenLocal, Pepper: []byte(strings.Repeat("p", 32)), AccessTTL: time.Minute, FamilyTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := manager.Issue(Principal{ClientID: "client", ClientInstanceID: "one", UserID: "old", BindingVersion: 1})
	second, _ := manager.Issue(Principal{ClientID: "client", ClientInstanceID: "two", UserID: "old", BindingVersion: 1})
	other, _ := manager.Issue(Principal{ClientID: "other", UserID: "old", BindingVersion: 1})
	replacement, err := manager.ReplaceClientPrincipal(Principal{ClientID: "client", ClientInstanceID: "one", UserID: "new", BindingVersion: 2})
	if err != nil {
		t.Fatal(err)
	}
	for _, old := range []TokenPair{first, second} {
		if _, ok := manager.Authenticate(context.Background(), []byte(old.AccessToken)); ok {
			t.Fatal("old client family remained valid")
		}
	}
	if principal, ok := manager.Authenticate(context.Background(), []byte(replacement.AccessToken)); !ok || principal.UserID != "new" || principal.BindingVersion != 2 {
		t.Fatalf("replacement principal=%#v ok=%v", principal, ok)
	}
	if _, ok := manager.Authenticate(context.Background(), []byte(other.AccessToken)); !ok {
		t.Fatal("unrelated client family was revoked")
	}
	recovery, ok := manager.AuthenticateRecovery(context.Background(), []byte(first.AccessToken))
	if !ok || !recovery.HasScope(ScopeUsersBind) || recovery.HasScope(ScopeInferenceExecute) {
		t.Fatalf("recovery principal=%#v ok=%v", recovery, ok)
	}
	result := BindingResult{Tokens: replacement, Binding: UserBinding{ClientID: "client", UserID: "new", BindingVersion: 2}}
	if err := manager.RememberBindingResult("client", 1, "bind-idempotency", result); err != nil {
		t.Fatal(err)
	}
	recovered, ok := manager.RecoverBindingResult("client", 1, "bind-idempotency")
	if !ok || recovered.Tokens.AccessToken != replacement.AccessToken || recovered.Binding.UserID != "new" {
		t.Fatalf("recovered=%#v ok=%v", recovered, ok)
	}
}

func TestTokenPepperRotationRetainsAndRetiresVersions(t *testing.T) {
	oldPepper := []byte(strings.Repeat("o", 32))
	newPepper := []byte(strings.Repeat("n", 32))
	manager, err := NewTokenManager(TokenManagerConfig{
		Mode: TokenGateway, Pepper: oldPepper, CurrentPepperVersion: "2026-01",
		AccessTTL: time.Hour, FamilyTTL: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldPair, err := manager.Issue(Principal{ClientID: "client", UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}
	result := BindingResult{Tokens: oldPair, Binding: UserBinding{ClientID: "client", UserID: "user", BindingVersion: 1}}
	if err := manager.RememberBindingResult("client", 0, "bind", result); err != nil {
		t.Fatal(err)
	}

	if err := manager.RotatePepper(PepperKey{Version: "2026-07", Key: newPepper}, PepperKey{Version: "2026-01", Key: oldPepper}); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Authenticate(context.Background(), []byte(oldPair.AccessToken)); !ok {
		t.Fatal("retained pepper version did not validate an existing token")
	}
	if recovered, ok := manager.RecoverBindingResult("client", 0, "bind"); !ok || recovered.Tokens.AccessToken != oldPair.AccessToken {
		t.Fatalf("pre-rotation binding recovery was lost: recovered=%#v ok=%v", recovered, ok)
	}
	newPair, err := manager.Issue(Principal{ClientID: "other", UserID: "user"})
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.RotatePepper(PepperKey{Version: "2026-07", Key: newPepper}); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.Authenticate(context.Background(), []byte(oldPair.AccessToken)); ok {
		t.Fatal("retired pepper version continued validating old tokens")
	}
	if _, ok := manager.Authenticate(context.Background(), []byte(newPair.AccessToken)); !ok {
		t.Fatal("current pepper version did not validate a new token")
	}
	if _, ok := manager.RecoverBindingResult("client", 0, "bind"); ok {
		t.Fatal("binding recovery tied to a retired pepper remained addressable")
	}
}

func TestTokenPepperRingValidation(t *testing.T) {
	_, err := NewTokenManager(TokenManagerConfig{
		Mode: TokenLocal, Pepper: []byte(strings.Repeat("p", 32)), CurrentPepperVersion: "same",
		PreviousPeppers: []PepperKey{{Version: "same", Key: []byte(strings.Repeat("q", 32))}},
		AccessTTL:       time.Minute, FamilyTTL: time.Hour,
	})
	if err == nil {
		t.Fatal("duplicate pepper version was accepted")
	}
}
