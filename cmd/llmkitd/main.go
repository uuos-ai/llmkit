package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/endpointpolicy"
	"github.com/uuos-ai/llmkit/gateway"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed/httpbackend"
	"github.com/uuos-ai/llmkit/providers/all"
	"github.com/uuos-ai/llmkit/routing"
	"github.com/uuos-ai/llmkit/runtimeconfig"
	"github.com/uuos-ai/llmkit/sidecar/binding"
	"github.com/uuos-ai/llmkit/sidecar/server"
)

var (
	version       = "dev"
	llmkitVersion = "dev"
	buildID       = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	configPath, overrides, err := parseFlags(arguments)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	config, err := runtimeconfig.Load(configPath, nil, overrides)
	if err != nil {
		return err
	}
	registry, err := all.NewRegistry()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch config.Mode {
	case runtimeconfig.ModeSidecar:
		return runSidecar(ctx, stop, config, registry)
	case runtimeconfig.ModeLocalService:
		return runLocalService(ctx, stop, config, registry)
	case runtimeconfig.ModeGateway:
		return runGateway(ctx, config, registry)
	default:
		return errors.New("llmkitd: unsupported mode")
	}
}

func runSidecar(ctx context.Context, stop context.CancelFunc, config runtimeconfig.Config, registry *llmkit.Registry) error {
	if config.ParentPID == os.Getpid() {
		return errors.New("llmkitd: parent_pid must identify the host process")
	}
	sessionKey, err := readSessionKey(os.Stdin)
	if err != nil {
		return err
	}
	defer clear(sessionKey)
	listener, cleanup, err := listenLocal(config.Socket)
	if err != nil {
		return err
	}
	defer cleanup()
	shutdown := sync.OnceFunc(func() { stop(); _ = listener.Close() })
	go func() {
		_ = watchParent(ctx, config.ParentPID)
		shutdown()
	}()
	service, err := server.New(server.Config{
		SessionKey: sessionKey, MaxFrameBytes: config.MaxFrameBytes, Shutdown: shutdown,
		Build: server.BuildInfo{SidecarVersion: version, LLMKitVersion: llmkitVersion, BuildID: buildID, InstanceID: config.InstanceID},
	})
	if err != nil {
		return err
	}
	defer service.Close()
	return bindAndServe(ctx, listener, service, config, registry)
}

func runLocalService(ctx context.Context, stop context.CancelFunc, config runtimeconfig.Config, registry *llmkit.Registry) error {
	authenticator, err := identity.LoadTokenHashFile(config.ClientTokenHashFile)
	if err != nil {
		return err
	}
	listener, cleanup, err := listenLocal(config.Socket)
	if err != nil {
		return err
	}
	defer cleanup()
	shutdown := sync.OnceFunc(func() { stop(); _ = listener.Close() })
	service, err := server.New(server.Config{
		Authenticator: authenticator, MaxFrameBytes: config.MaxFrameBytes, Shutdown: shutdown,
		Build: server.BuildInfo{SidecarVersion: version, LLMKitVersion: llmkitVersion, BuildID: buildID, InstanceID: config.InstanceID},
	})
	if err != nil {
		return err
	}
	defer service.Close()
	return bindAndServe(ctx, listener, service, config, registry)
}

func bindAndServe(ctx context.Context, listener net.Listener, service *server.Server, config runtimeconfig.Config, registry *llmkit.Registry) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	source := routing.Source(routing.RegistrySource(registry))
	if config.BusinessCatalogURL != "" {
		source = routing.HTTPSource{URL: config.BusinessCatalogURL}
	}
	routes := routing.NewSessionCatalogWithPolicy(source, config.CustomProviderSync == runtimeconfig.SyncDisabled)
	if err := binding.Register(service, binding.Config{
		Registry: registry, Routes: routes, EndpointPolicy: endpointpolicy.PublicHTTPS(ctx, nil),
	}); err != nil {
		return err
	}
	var connections sync.WaitGroup
	defer connections.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return errors.New("llmkitd: local accept failed")
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			_ = service.ServeConn(ctx, connection)
		}()
	}
}

func runGateway(ctx context.Context, config runtimeconfig.Config, registry *llmkit.Registry) error {
	authenticator, err := identity.LoadTokenHashFile(config.ClientTokenHashFile)
	if err != nil {
		return err
	}
	backend, err := httpbackend.New(
		config.Storage.ConfigStore, config.Storage.SecretStore, config.Storage.AuditStore,
		config.TLS.CertificateFile, config.TLS.PrivateKeyFile,
	)
	if err != nil {
		return err
	}
	service, err := gateway.New(gateway.Config{
		InstanceID: config.InstanceID, Registry: registry, Authenticator: authenticator, ConfigStore: backend,
		SecretStore: backend, AuditStore: backend, CustomProviders: backend,
		EndpointPolicy: endpointpolicy.PublicHTTPS(ctx, nil), MaxBodyBytes: int64(config.MaxFrameBytes),
	})
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr: config.Listen, Handler: service.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, WriteTimeout: 0, IdleTimeout: 90 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	done := make(chan error, 1)
	go func() { done <- httpServer.ListenAndServeTLS(config.TLS.CertificateFile, config.TLS.PrivateKeyFile) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return errors.New("llmkitd: gateway shutdown timed out")
		}
		err := <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func readSessionKey(reader io.Reader) ([]byte, error) {
	buffered := bufio.NewReaderSize(reader, 4097)
	line, err := buffered.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, errors.New("llmkitd: session key could not be read")
	}
	if len(line) > 4096 {
		return nil, errors.New("llmkitd: session key is too large")
	}
	key := []byte(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"))
	if len(key) < 32 {
		clear(key)
		return nil, errors.New("llmkitd: session key must contain at least 32 bytes")
	}
	return key, nil
}

type optionalString struct{ value **string }

func (o optionalString) String() string         { return "" }
func (o optionalString) Set(value string) error { copy := value; *o.value = &copy; return nil }

type optionalInt struct{ value **int }

func (o optionalInt) String() string { return "" }
func (o optionalInt) Set(value string) error {
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	*o.value = &parsed
	return nil
}

type optionalUint32 struct{ value **uint32 }

func (o optionalUint32) String() string { return "" }
func (o optionalUint32) Set(value string) error {
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return err
	}
	converted := uint32(parsed)
	*o.value = &converted
	return nil
}

func parseFlags(arguments []string) (string, runtimeconfig.Overrides, error) {
	set := flag.NewFlagSet("llmkitd", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	var configPath string
	var overrides runtimeconfig.Overrides
	var mode, syncMode *string
	set.StringVar(&configPath, "config", "", "strict YAML or JSON configuration file")
	set.Var(optionalString{&mode}, "mode", "sidecar, local-service, or gateway")
	set.Var(optionalString{&overrides.InstanceID}, "instance-id", "independent daemon instance ID")
	set.Var(optionalString{&overrides.Socket}, "socket", "Unix socket or Windows named pipe")
	set.Var(optionalString{&overrides.Listen}, "listen", "gateway HTTPS listen address")
	set.Var(optionalInt{&overrides.ParentPID}, "parent-pid", "sidecar host process ID")
	set.Var(optionalString{&overrides.ClientTokenHashFile}, "client-token-hash-file", "client token hash file")
	set.Var(optionalString{&overrides.BusinessCatalogURL}, "business-catalog-url", "business provider catalog HTTPS URL")
	set.Var(optionalString{&syncMode}, "custom-provider-sync", "disabled or managed")
	set.Var(optionalUint32{&overrides.MaxFrameBytes}, "max-frame-bytes", "maximum local frame or gateway body size")
	set.Var(optionalString{&overrides.TLSCertificateFile}, "tls-certificate-file", "gateway TLS certificate")
	set.Var(optionalString{&overrides.TLSPrivateKeyFile}, "tls-private-key-file", "gateway TLS private key")
	set.Var(optionalString{&overrides.ConfigStore}, "config-store", "external HTTPS ConfigStore URL")
	set.Var(optionalString{&overrides.SecretStore}, "secret-store", "external HTTPS SecretStore URL")
	set.Var(optionalString{&overrides.AuditStore}, "audit-store", "external HTTPS AuditStore URL")
	set.Var(optionalString{&overrides.SQLitePath}, "sqlite-path", "optional local-service SQLite path")
	if err := set.Parse(arguments); err != nil {
		return "", runtimeconfig.Overrides{}, fmt.Errorf("llmkitd: parse flags: %w", err)
	}
	if set.NArg() != 0 {
		return "", runtimeconfig.Overrides{}, errors.New("llmkitd: unexpected positional arguments")
	}
	if mode != nil {
		converted := runtimeconfig.Mode(*mode)
		overrides.Mode = &converted
	}
	if syncMode != nil {
		converted := runtimeconfig.CustomProviderSync(*syncMode)
		overrides.CustomProviderSync = &converted
	}
	return configPath, overrides, nil
}
