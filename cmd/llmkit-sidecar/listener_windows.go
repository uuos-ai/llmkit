//go:build windows

package main

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const localPipePrefix = `\\.\pipe\`

func listenLocal(address string) (net.Listener, func(), error) {
	if !strings.HasPrefix(strings.ToLower(address), localPipePrefix) || len(address) == len(localPipePrefix) {
		return nil, nil, errors.New(`llmkit-sidecar: --socket must be a local \\.\pipe\NAME address`)
	}

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, nil, errors.New("llmkit-sidecar: current user could not be identified")
	}
	defer token.Close()
	tokenUser, err := token.GetTokenUser()
	if err != nil {
		return nil, nil, errors.New("llmkit-sidecar: current user could not be identified")
	}
	sid := tokenUser.User.Sid.String()
	listener, err := winio.ListenPipe(address, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")",
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("llmkit-sidecar: listen failed: %w", err)
	}
	cleanup := syncOnce(func() { _ = listener.Close() })
	return listener, cleanup, nil
}
