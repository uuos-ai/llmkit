package blobstore

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
)

func TestBlobIsolationIncludesClientUserAndBinding(t *testing.T) {
	store, _ := NewMemory(Config{})
	owner := identity.Principal{ClientID: "a", UserID: "u", BindingVersion: 1}
	metadata, err := store.Put(context.Background(), owner, managed.BlobMetadata{MediaType: "image/png"}, bytes.NewReader([]byte("content")))
	if err != nil {
		t.Fatal(err)
	}
	reader, _, err := store.Open(context.Background(), owner, metadata.Ref)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(reader)
	_ = reader.Close()
	if string(got) != "content" {
		t.Fatalf("content=%q", got)
	}
	for _, other := range []identity.Principal{{ClientID: "b", UserID: "u", BindingVersion: 1}, {ClientID: "a", UserID: "v", BindingVersion: 1}, {ClientID: "a", UserID: "u", BindingVersion: 2}} {
		if reader, _, err := store.Open(context.Background(), other, metadata.Ref); err == nil {
			_ = reader.Close()
			t.Fatalf("cross-boundary read allowed: %#v", other)
		}
	}
}
