package identity

import (
	"context"
	"errors"
	"testing"
)

func TestUserBindingIsClientLocalAndVersioned(t *testing.T) {
	store := NewMemoryUserBindingStore(nil)
	_, first, err := store.Bind(context.Background(), BindRequest{ClientID: "a", UserID: "same"})
	if err != nil || first.BindingVersion != 1 {
		t.Fatalf("first bind = %#v, %v", first, err)
	}
	_, other, err := store.Bind(context.Background(), BindRequest{ClientID: "b", UserID: "same"})
	if err != nil || other.BindingVersion != 1 {
		t.Fatalf("same text in another client must be independent: %#v, %v", other, err)
	}
	previous, current, err := store.Bind(context.Background(), BindRequest{ClientID: "a", UserID: "next", ExpectedBindingVersion: 1})
	if err != nil || previous.UserID != "same" || current.UserID != "next" || current.BindingVersion != 2 {
		t.Fatalf("switch = %#v -> %#v, %v", previous, current, err)
	}
	_, _, err = store.Bind(context.Background(), BindRequest{ClientID: "a", UserID: "stale", ExpectedBindingVersion: 1})
	if !errors.Is(err, ErrBindingConflict) {
		t.Fatalf("stale switch error = %v", err)
	}
}

func TestUserBindingAuthorizationRunsBeforeMutation(t *testing.T) {
	store := NewMemoryUserBindingStore(UserBindingAuthorizerFunc(func(context.Context, BindRequest) error {
		return errors.New("denied")
	}))
	_, _, err := store.Bind(context.Background(), BindRequest{ClientID: "a", UserID: "u"})
	if !errors.Is(err, ErrBindingUnauthorized) {
		t.Fatalf("error = %v", err)
	}
	if _, ok, _ := store.Current(context.Background(), "a"); ok {
		t.Fatal("unauthorized bind mutated state")
	}
}
