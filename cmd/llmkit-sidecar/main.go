package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/anthropic"
	"github.com/uuos-ai/llmkit/providers/dashscope"
	"github.com/uuos-ai/llmkit/providers/deepseek"
	"github.com/uuos-ai/llmkit/providers/gemini"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/sidecar/binding"
	"github.com/uuos-ai/llmkit/sidecar/server"
)

var (
	version       = "dev"
	llmkitVersion = "dev"
	buildID       = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var socketPath string
	flag.StringVar(&socketPath, "socket", "", "absolute Unix domain socket path")
	flag.Parse()
	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return errors.New("llmkit-sidecar: --socket must be an absolute path")
	}
	if _, err := os.Lstat(socketPath); err == nil {
		return errors.New("llmkit-sidecar: socket path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("llmkit-sidecar: socket path cannot be inspected")
	}

	sessionKey, err := readSessionKey(os.Stdin)
	if err != nil {
		return err
	}
	defer clear(sessionKey)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("llmkit-sidecar: listen failed: %w", err)
	}
	defer listener.Close()
	defer os.Remove(socketPath)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return errors.New("llmkit-sidecar: socket permissions could not be restricted")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdown := sync.OnceFunc(func() {
		stop()
		_ = listener.Close()
	})
	service, err := server.New(server.Config{
		SessionKey: sessionKey,
		Build: server.BuildInfo{
			SidecarVersion: version, LLMKitVersion: llmkitVersion, BuildID: buildID,
		},
		Shutdown: shutdown,
	})
	if err != nil {
		return err
	}
	defer service.Close()
	registry, err := defaultRegistry()
	if err != nil {
		return err
	}
	if err := binding.Register(service, binding.Config{Registry: registry}); err != nil {
		return err
	}

	var connections sync.WaitGroup
	defer connections.Wait()
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return errors.New("llmkit-sidecar: accept failed")
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			_ = service.ServeConn(ctx, connection)
		}()
	}
}

func readSessionKey(reader io.Reader) ([]byte, error) {
	buffered := bufio.NewReaderSize(reader, 4097)
	line, err := buffered.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, errors.New("llmkit-sidecar: session key could not be read")
	}
	if len(line) > 4096 {
		return nil, errors.New("llmkit-sidecar: session key is too large")
	}
	key := []byte(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
	if len(key) < 32 {
		clear(key)
		return nil, errors.New("llmkit-sidecar: session key must contain at least 32 bytes")
	}
	return key, nil
}

func defaultRegistry() (*llmkit.Registry, error) {
	registry := llmkit.NewRegistry()
	constructors := []func() (llmkit.Provider, error){
		func() (llmkit.Provider, error) { return openai.New(openai.Config{}) },
		func() (llmkit.Provider, error) { return anthropic.New(anthropic.Config{}) },
		func() (llmkit.Provider, error) { return gemini.New(gemini.Config{}) },
		func() (llmkit.Provider, error) { return deepseek.New(deepseek.Config{}) },
		func() (llmkit.Provider, error) { return dashscope.New(dashscope.Config{}) },
	}
	for _, construct := range constructors {
		provider, err := construct()
		if err != nil {
			return nil, err
		}
		if err := registry.Register(provider); err != nil {
			return nil, err
		}
	}
	return registry, nil
}
