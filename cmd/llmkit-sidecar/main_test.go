package main

import (
	"strings"
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestReadSessionKey(t *testing.T) {
	want := "0123456789abcdef0123456789abcdef"
	got, err := readSessionKey(strings.NewReader(want + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("key length = %d", len(got))
	}
	clear(got)
}

func TestReadSessionKeyRejectsShortInput(t *testing.T) {
	if _, err := readSessionKey(strings.NewReader("short\n")); err == nil {
		t.Fatal("expected error")
	}
}

func TestDefaultRegistry(t *testing.T) {
	registry, err := defaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"openai", "anthropic", "gemini", "deepseek", "dashscope",
		"minimax", "zhipu", "volcengine", "hunyuan",
		"moonshot",
	} {
		if _, ok := registry.Get(llmkit.ProviderID(id)); !ok {
			t.Fatalf("provider %q is not registered", id)
		}
	}
}
