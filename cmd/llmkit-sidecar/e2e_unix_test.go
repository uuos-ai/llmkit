//go:build !windows

package main

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func e2eLocalAddress(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "llmkit-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return filepath.Join(directory, "sidecar.sock")
}

func dialE2ELocal(address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", address, timeout)
}
