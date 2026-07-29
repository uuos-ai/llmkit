//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

func e2eLocalAddress(*testing.T) string {
	return fmt.Sprintf(`\\.\pipe\llmkit-e2e-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

func dialE2ELocal(address string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(address, &timeout)
}
