//go:build windows

package main

import (
	"errors"
	"net"
	"strings"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func listenLocal(address string) (net.Listener, func(), error) {
	const prefix = `\\.\pipe\`
	if !strings.HasPrefix(strings.ToLower(address), prefix) || len(address) == len(prefix) {
		return nil, nil, errors.New(`llmkitd: socket must be a local \\.\pipe\NAME address`)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, nil, errors.New("llmkitd: current user could not be identified")
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, nil, errors.New("llmkitd: current user could not be identified")
	}
	listener, err := winio.ListenPipe(address, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")", InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
	if err != nil {
		return nil, nil, errors.New("llmkitd: local listen failed")
	}
	cleanup := sync.OnceFunc(func() { _ = listener.Close() })
	return listener, cleanup, nil
}
