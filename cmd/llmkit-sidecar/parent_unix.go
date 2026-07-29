//go:build !windows

package main

import (
	"context"
	"errors"
	"syscall"
	"time"
)

func watchParent(ctx context.Context, pid int) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := syscall.Kill(pid, 0); err != nil && errors.Is(err, syscall.ESRCH) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
