//go:build !windows

package main

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
)

func listenLocal(address string) (net.Listener, func(), error) {
	if address == "" || !filepath.IsAbs(address) {
		return nil, nil, errors.New("llmkitd: socket must be an absolute path")
	}
	if _, err := os.Lstat(address); err == nil {
		return nil, nil, errors.New("llmkitd: socket path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, errors.New("llmkitd: socket path cannot be inspected")
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, nil, errors.New("llmkitd: local listen failed")
	}
	if err := os.Chmod(address, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(address)
		return nil, nil, errors.New("llmkitd: socket permissions could not be restricted")
	}
	cleanup := sync.OnceFunc(func() { _ = listener.Close(); _ = os.Remove(address) })
	return listener, cleanup, nil
}
