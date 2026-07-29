//go:build windows

package main

import (
	"context"
	"errors"
)

func watchParent(context.Context, int) error {
	return errors.New("Windows parent monitoring requires the named-pipe runtime")
}
