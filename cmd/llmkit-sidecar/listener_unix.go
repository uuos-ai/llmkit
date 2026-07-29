//go:build !windows

package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

func listenLocal(address string) (net.Listener, func(), error) {
	if address == "" || !filepath.IsAbs(address) {
		return nil, nil, errors.New("llmkit-sidecar: --socket must be an absolute path")
	}
	if _, err := os.Lstat(address); err == nil {
		return nil, nil, errors.New("llmkit-sidecar: socket path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("llmkit-sidecar: socket path cannot be inspected")
	}

	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, nil, fmt.Errorf("llmkit-sidecar: listen failed: %w", err)
	}
	if err := os.Chmod(address, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(address)
		return nil, nil, errors.New("llmkit-sidecar: socket permissions could not be restricted")
	}
	cleanup := syncOnce(func() {
		_ = listener.Close()
		_ = os.Remove(address)
	})
	return listener, cleanup, nil
}
