package main

import "sync"

func syncOnce(fn func()) func() {
	return sync.OnceFunc(fn)
}
