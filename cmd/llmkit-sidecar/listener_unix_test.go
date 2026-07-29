//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListenLocalRestrictsSocketPermissions(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "llmkit-sidecar-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "sidecar.sock")
	listener, cleanup, err := listenLocal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if listener == nil {
		t.Fatal("listener is nil")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket permissions = %o, want 600", got)
	}
}

func TestListenLocalRejectsRelativePath(t *testing.T) {
	if _, _, err := listenLocal("sidecar.sock"); err == nil {
		t.Fatal("expected relative path to be rejected")
	}
}
