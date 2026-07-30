package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
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
	"github.com/uuos-ai/llmkit/localstore"
	"github.com/uuos-ai/llmkit/managed/httpbackend"
	"github.com/uuos-ai/llmkit/managed/httpcoord"
	"github.com/uuos-ai/llmkit/managed/servicetoken"
	"github.com/uuos-ai/llmkit/providers/all"
	"github.com/uuos-ai/llmkit/routing"
	"github.com/uuos-ai/llmkit/runtimeconfig"
	"github.com/uuos-ai/llmkit/sidecar/binding"
	"github.com/uuos-ai/llmkit/sidecar/protocol"
	"github.com/uuos-ai/llmkit/sidecar/server"
	"github.com/uuos-ai/llmkit/transport"
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
	registry, err := all.NewRegistryWithTransport(transport.New(transport.Config{HTTPClient: endpointpolicy.PublicHTTPClient(nil)}))
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
	return bindAndServe(ctx, listener, service, config, registry, nil)
}

func runLocalService(ctx context.Context, stop context.CancelFunc, config runtimeconfig.Config, registry *llmkit.Registry) error {
	enrollment, err := identity.LoadEnrollmentHashFile(config.ClientTokenHashFile)
	if err != nil {
		return err
	}
	pepper := make([]byte, 32)
	if _, err := rand.Read(pepper); err != nil {
		return errors.New("llmkitd: local token key generation failed")
	}
	tokens, err := identity.NewTokenManager(identity.TokenManagerConfig{Mode: identity.TokenLocal, Pepper: pepper, AccessTTL: 15 * time.Minute, FamilyTTL: 12 * time.Hour})
	clear(pepper)
	if err != nil {
		return err
	}
	var bindings identity.UserBindingStore = identity.NewMemoryUserBindingStore(identity.UserBindingAuthorizerFunc(func(ctx context.Context, _ identity.BindRequest) error {
		principal, ok := identity.FromContext(ctx)
		if !ok || !principal.HasScope(identity.ScopeUsersBind) {
			return identity.ErrBindingUnauthorized
		}
		return nil
	}))
	if config.Storage.CoordinationStore != "" {
		serviceToken, sourceErr := servicetoken.NewFileSource(config.BusinessServiceTokenFile)
		if sourceErr != nil {
			return sourceErr
		}
		coordination, coordinationErr := httpcoord.New(
			config.Storage.CoordinationStore, config.TLS.CertificateFile, config.TLS.PrivateKeyFile, serviceToken,
		)
		if coordinationErr != nil {
			return coordinationErr
		}
		bindings = coordination
	}
	listener, cleanup, err := listenLocal(config.Socket)
	if err != nil {
		return err
	}
	defer cleanup()
	shutdown := sync.OnceFunc(func() { stop(); _ = listener.Close() })
	authenticator := identity.BindingAuthenticator{
		Base: identity.MultiAuthenticator{tokens, enrollment}, Store: bindings,
	}
	service, err := server.New(server.Config{
		Authenticator: authenticator, RecoveryAuthenticator: tokens, MaxFrameBytes: config.MaxFrameBytes, Shutdown: shutdown,
		Build: server.BuildInfo{SidecarVersion: version, LLMKitVersion: llmkitVersion, BuildID: buildID, InstanceID: config.InstanceID},
	})
	if err != nil {
		return err
	}
	defer service.Close()
	if err := service.Register(protocol.MethodEnroll, func(ctx context.Context, _ json.RawMessage) (any, error) {
		principal, ok := identity.FromContext(ctx)
		if !ok || !principal.HasScope(identity.ScopeClientsEnroll) {
			return nil, &llmkit.ProviderError{Kind: llmkit.ErrorPermissionDenied, SafeMessage: "client enrollment is not permitted"}
		}
		issuedPrincipal, err := enrollment.Consume(principal)
		if err != nil {
			return nil, &llmkit.ProviderError{Kind: llmkit.ErrorAuthentication, SafeMessage: "enrollment token is invalid or already used"}
		}
		return tokens.Issue(issuedPrincipal)
	}); err != nil {
		return err
	}
	if err := service.Register(protocol.MethodRefreshToken, func(ctx context.Context, payload json.RawMessage) (any, error) {
		principal, ok := identity.FromContext(ctx)
		if !ok || !principal.HasScope(identity.ScopeTokensRefresh) {
			return nil, &llmkit.ProviderError{Kind: llmkit.ErrorPermissionDenied, SafeMessage: "token refresh is not permitted"}
		}
		var request protocol.RefreshTokenRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.RefreshToken == "" || request.IdempotencyKey == "" {
			return nil, &llmkit.ProviderError{Kind: llmkit.ErrorInvalidRequest, SafeMessage: "refresh_token and idempotency_key are required"}
		}
		return tokens.Refresh(request.RefreshToken, request.IdempotencyKey)
	}); err != nil {
		return err
	}
	if err := service.Register(protocol.MethodBindUser, func(ctx context.Context, payload json.RawMessage) (any, error) {
		principal, ok := identity.FromContext(ctx)
		if !ok || !principal.HasScope(identity.ScopeUsersBind) {
			return nil, &llmkit.ProviderError{Kind: llmkit.ErrorPermissionDenied, SafeMessage: "user binding is not permitted"}
		}
		var request protocol.BindUserRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.UserID == "" || request.IdempotencyKey == "" {
			return nil, &llmkit.ProviderError{Kind: llmkit.ErrorInvalidRequest, SafeMessage: "user_id and idempotency_key are required"}
		}
		if cached, ok := tokens.RecoverBindingResult(principal.ClientID, request.ExpectedBindingVersion, request.IdempotencyKey); ok {
			return protocol.BindUserResponse{Tokens: cached.Tokens, Binding: cached.Binding}, nil
		}
		_, binding, err := bindings.Bind(ctx, identity.BindRequest{ClientID: principal.ClientID, ClientInstanceID: principal.ClientInstanceID, UserID: request.UserID, ExpectedBindingVersion: request.ExpectedBindingVersion, Proof: request.Proof})
		if err != nil {
			return nil, &llmkit.ProviderError{Kind: llmkit.ErrorPermissionDenied, SafeMessage: "user binding was rejected"}
		}
		principal.UserID, principal.BindingVersion = binding.UserID, binding.BindingVersion
		pair, err := tokens.ReplaceClientPrincipal(principal)
		if err != nil {
			return nil, err
		}
		result := identity.BindingResult{Tokens: pair, Binding: binding}
		if err := tokens.RememberBindingResult(principal.ClientID, request.ExpectedBindingVersion, request.IdempotencyKey, result); err != nil {
			return nil, err
		}
		return protocol.BindUserResponse{Tokens: result.Tokens, Binding: result.Binding}, nil
	}); err != nil {
		return err
	}
	var managedStore binding.ManagedStore
	if config.Storage.SQLitePath != "" {
		store, openErr := localstore.Open(config.Storage.SQLitePath, config.InstanceID)
		if openErr != nil {
			return openErr
		}
		defer store.Close()
		managedStore = store
	}
	return bindAndServe(ctx, listener, service, config, registry, managedStore)
}

func bindAndServe(ctx context.Context, listener net.Listener, service *server.Server, config runtimeconfig.Config, registry *llmkit.Registry, managedStore binding.ManagedStore) error {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	source := routing.Source(routing.RegistrySource(registry))
	if config.BusinessTargetsURL != "" {
		source = routing.HTTPSource{URL: config.BusinessTargetsURL}
	}
	if managedStore != nil {
		localSource := routing.SourceFunc(func(ctx context.Context, principal identity.Principal) (routing.OptionsResponse, error) {
			return managedStore.ProviderOptions(ctx, principal)
		})
		source = routing.CombineSources(source, localSource)
	}
	routes := routing.NewSessionCatalogWithPolicy(source, config.CustomProviderSync == runtimeconfig.SyncDisabled)
	if err := binding.Register(service, binding.Config{
		Registry: registry, Routes: routes, EndpointPolicy: endpointpolicy.PublicHTTPS(ctx, nil), ManagedStore: managedStore,
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
	serviceToken, err := servicetoken.NewFileSource(config.BusinessServiceTokenFile)
	if err != nil {
		return err
	}
	backend, err := httpbackend.New(
		config.Storage.ConfigStore, config.Storage.SecretStore, config.Storage.AuditStore,
		config.TLS.CertificateFile, config.TLS.PrivateKeyFile, serviceToken,
	)
	if err != nil {
		return err
	}
	coordination, err := httpcoord.New(config.Storage.CoordinationStore, config.TLS.CertificateFile, config.TLS.PrivateKeyFile, serviceToken)
	if err != nil {
		return err
	}
	service, err := gateway.New(gateway.Config{
		InstanceID: config.InstanceID, Registry: registry, Authenticator: identity.MultiAuthenticator{coordination, authenticator}, ConfigStore: backend,
		SecretStore: backend, AuditStore: backend, CustomProviders: backend,
		SessionStore: coordination, BindingStore: coordination, RateLimitStore: coordination, IdentityService: coordination,
		EndpointPolicy: endpointpolicy.PublicHTTPS(ctx, nil), MaxBodyBytes: int64(config.MaxFrameBytes),
	})
	if err != nil {
		return err
	}
	dataServer := &http.Server{
		Addr: config.Listen, Handler: service.DataHandler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, WriteTimeout: 0, IdleTimeout: 90 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	adminServer := &http.Server{
		Addr: config.AdminListen, Handler: service.ControlHandler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 90 * time.Second,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	done := make(chan error, 2)
	go func() { done <- dataServer.ListenAndServeTLS(config.TLS.CertificateFile, config.TLS.PrivateKeyFile) }()
	go func() { done <- adminServer.ListenAndServeTLS(config.TLS.CertificateFile, config.TLS.PrivateKeyFile) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := dataServer.Shutdown(shutdownCtx); err != nil {
			return errors.New("llmkitd: gateway shutdown timed out")
		}
		if err := adminServer.Shutdown(shutdownCtx); err != nil {
			return errors.New("llmkitd: gateway admin shutdown timed out")
		}
		for range 2 {
			if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
		}
		return nil
	case err := <-done:
		_ = dataServer.Close()
		_ = adminServer.Close()
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
	set.Var(optionalString{&overrides.AdminListen}, "admin-listen", "gateway control-plane HTTPS listen address")
	set.Var(optionalInt{&overrides.ParentPID}, "parent-pid", "sidecar host process ID")
	set.Var(optionalString{&overrides.ClientTokenHashFile}, "client-token-hash-file", "client token hash file")
	set.Var(optionalString{&overrides.BusinessTargetsURL}, "business-targets-url", "business available-target-list HTTPS URL")
	set.Var(optionalString{&overrides.BusinessServiceTokenFile}, "business-service-token-file", "restricted file containing a rotating business API service token")
	set.Var(optionalString{&syncMode}, "custom-provider-sync", "disabled or managed")
	set.Var(optionalUint32{&overrides.MaxFrameBytes}, "max-frame-bytes", "maximum local frame or gateway body size")
	set.Var(optionalString{&overrides.TLSCertificateFile}, "tls-certificate-file", "gateway TLS certificate")
	set.Var(optionalString{&overrides.TLSPrivateKeyFile}, "tls-private-key-file", "gateway TLS private key")
	set.Var(optionalString{&overrides.ConfigStore}, "config-store", "external HTTPS ConfigStore URL")
	set.Var(optionalString{&overrides.SecretStore}, "secret-store", "external HTTPS SecretStore URL")
	set.Var(optionalString{&overrides.AuditStore}, "audit-store", "external HTTPS AuditStore URL")
	set.Var(optionalString{&overrides.CoordinationStore}, "coordination-store", "external HTTPS binding store for local-service, or session/binding/rate-limit store for gateway")
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
