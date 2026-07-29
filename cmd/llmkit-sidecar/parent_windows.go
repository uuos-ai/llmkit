//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func watchParent(ctx context.Context, pid int) error {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("open parent process: %w", err)
	}
	defer windows.CloseHandle(process)

	for {
		result, err := windows.WaitForSingleObject(process, 1000)
		if err != nil {
			return fmt.Errorf("wait for parent process: %w", err)
		}
		switch result {
		case windows.WAIT_OBJECT_0:
			return nil
		case uint32(windows.WAIT_TIMEOUT):
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		default:
			return errors.New("wait for parent process returned an unexpected status")
		}
	}
}
