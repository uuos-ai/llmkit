package servicetoken

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileSourceReloadsRotatedToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-token")
	write := func(value string) {
		if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("first-service-token-0123456789abcdef")
	source, err := NewFileSource(path)
	if err != nil {
		t.Fatal(err)
	}
	write("second-service-token-0123456789abcde")
	token, err := source.ServiceToken(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer clear(token)
	if string(token) != "second-service-token-0123456789abcde" {
		t.Fatalf("token=%q", token)
	}
}
