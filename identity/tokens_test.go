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
