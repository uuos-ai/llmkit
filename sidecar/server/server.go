// Package server implements authenticated llmkit-sidecar v1 connection
// dispatch. Network listener ownership stays with the command or host.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/sidecar/protocol"
)

type Handler func(context.Context, json.RawMessage) (any, error)

type Emitter interface {
	Emit(any) error
}

// EventSequence lets a handler retain backpressure: Emit writes one bounded
// frame synchronously before the next upstream event is read.
type EventSequence struct {
	Run func(context.Context, Emitter) error
}

type BuildInfo struct {
	SidecarVersion string
	LLMKitVersion  string
	BuildID        string
	InstanceID     string
}

type Config struct {
	SessionKey            []byte
	Authenticator         identity.Authenticator
	RecoveryAuthenticator identity.RecoveryAuthenticator
	MaxFrameBytes         uint32
	Build                 BuildInfo
	Shutdown              func()
}

type Server struct {
	sessionKey []byte
	auth       identity.Authenticator
	recovery   identity.RecoveryAuthenticator
	maxFrame   uint32
	build      BuildInfo
	shutdown   func()
	mu         sync.RWMutex
	handlers   map[protocol.Method]Handler
}

func New(config Config) (*Server, error) {
	if config.Authenticator != nil && len(config.SessionKey) != 0 {
		return nil, fmt.Errorf("sidecar server: configure either session key or authenticator")
	}
	authenticator := config.Authenticator
	var key []byte
	if authenticator == nil {
		if len(config.SessionKey) < 32 {
			return nil, fmt.Errorf("sidecar server: session key must contain at least 32 bytes")
		}
		key = append([]byte(nil), config.SessionKey...)
		var err error
		authenticator, err = identity.SingleToken(key, identity.Principal{
			ClientID: "sidecar-host", Scopes: map[string]struct{}{"shutdown": {}},
		})
		if err != nil {
			clear(key)
			return nil, err
		}
	}
	return &Server{
		sessionKey: key, auth: authenticator, recovery: config.RecoveryAuthenticator, maxFrame: config.MaxFrameBytes,
		build: config.Build, shutdown: config.Shutdown,
		handlers: make(map[protocol.Method]Handler),
	}, nil
}

func (s *Server) Close() {
	clear(s.sessionKey)
	if destroyer, ok := s.auth.(interface{ Destroy() }); ok {
		destroyer.Destroy()
	}
}

func (s *Server) Register(method protocol.Method, handler Handler) error {
	if handler == nil {
		return fmt.Errorf("sidecar server: handler is required")
	}
	switch method {
	case protocol.MethodHandshake, protocol.MethodHealth, protocol.MethodShutdown, protocol.MethodCancel:
		return fmt.Errorf("sidecar server: method %q is reserved", method)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.handlers[method]; exists {
		return fmt.Errorf("sidecar server: method %q already registered", method)
	}
	s.handlers[method] = handler
	return nil
}

func (s *Server) ServeConn(ctx context.Context, connection io.ReadWriteCloser) error {
	defer connection.Close()
	connectionCtx, cancelConnection := context.WithCancel(ctx)
	defer cancelConnection()
	codec := protocol.NewCodec(connection, connection, s.maxFrame)
	writer := &lockedWriter{codec: codec}

	first, err := codec.ReadRequest()
	if err != nil {
		return err
	}
	principal, authenticated := s.auth.Authenticate(ctx, []byte(first.SessionKey))
	recoveryOnly := false
	if first.Method == protocol.MethodHandshake && !authenticated && s.recovery != nil {
		var handshake protocol.HandshakeRequest
		if json.Unmarshal(first.Payload, &handshake) == nil && handshake.Purpose == "token_recovery" {
			principal, authenticated = s.recovery.AuthenticateRecovery(ctx, []byte(first.SessionKey))
			recoveryOnly = authenticated
		}
	}
	if first.Method != protocol.MethodHandshake || !authenticated {
		_ = writer.write(errorResponse(first.RequestID, "authentication", "sidecar handshake rejected"))
		return errors.New("sidecar server: handshake rejected")
	}
	connectionCtx = identity.WithPrincipal(connectionCtx, principal)
	if err := s.handleHandshake(first, principal, writer); err != nil {
		return err
	}

	var requests sync.WaitGroup
	var activeMu sync.Mutex
	active := make(map[string]context.CancelFunc)
	defer func() {
		activeMu.Lock()
		for _, cancel := range active {
			cancel()
		}
		activeMu.Unlock()
		requests.Wait()
	}()

	for {
		request, readErr := codec.ReadRequest()
		if errors.Is(readErr, io.EOF) {
			return nil
		}
		if readErr != nil {
			return readErr
		}
		requestPrincipal, ok := s.auth.Authenticate(connectionCtx, []byte(request.SessionKey))
		if !ok && recoveryOnly && (request.Method == protocol.MethodRefreshToken || request.Method == protocol.MethodBindUser) {
			requestPrincipal, ok = s.recovery.AuthenticateRecovery(connectionCtx, []byte(request.SessionKey))
		}
		if recoveryOnly && request.Method != protocol.MethodRefreshToken && request.Method != protocol.MethodBindUser && request.Method != protocol.MethodCancel {
			ok = false
		}
		if !ok || requestPrincipal.Key() != principal.Key() || request.Version != protocol.Version {
			_ = writer.write(errorResponse(request.RequestID, "authentication", "sidecar request rejected"))
			continue
		}
		if request.Method == protocol.MethodCancel {
			s.handleCancel(request, writer, &activeMu, active)
			continue
		}
		if request.RequestID == "" {
			_ = writer.write(errorResponse("", "invalid_request", "request_id is required"))
			continue
		}
		requestCtx, cancel := context.WithCancel(connectionCtx)
		activeMu.Lock()
		if _, duplicate := active[request.RequestID]; duplicate {
			activeMu.Unlock()
			cancel()
			_ = writer.write(errorResponse(request.RequestID, "invalid_request", "request_id is already active"))
			continue
		}
		active[request.RequestID] = cancel
		activeMu.Unlock()

		requests.Add(1)
		go func(request protocol.Request) {
			defer requests.Done()
			defer clear(request.Payload)
			defer func() {
				cancel()
				activeMu.Lock()
				delete(active, request.RequestID)
				activeMu.Unlock()
			}()
			s.dispatch(requestCtx, request, writer)
		}(request)
	}
}

func (s *Server) handleHandshake(request protocol.Request, principal identity.Principal, writer *lockedWriter) error {
	if request.Version != protocol.Version {
		_ = writer.write(errorResponse(request.RequestID, "unsupported_version", "protocol version is not supported"))
		return errors.New("sidecar server: incompatible protocol")
	}
	var handshake protocol.HandshakeRequest
	if err := json.Unmarshal(request.Payload, &handshake); err != nil {
		_ = writer.write(errorResponse(request.RequestID, "invalid_request", "malformed handshake"))
		return errors.New("sidecar server: malformed handshake")
	}
	compatible := false
	for _, version := range handshake.SupportedVersions {
		if version == protocol.Version {
			compatible = true
			break
		}
	}
	if !compatible {
		_ = writer.write(errorResponse(request.RequestID, "unsupported_version", "no compatible protocol version"))
		return errors.New("sidecar server: incompatible protocol")
	}
	s.mu.RLock()
	methods := make([]protocol.Method, 0, len(s.handlers)+4)
	methods = append(methods, protocol.MethodHandshake, protocol.MethodHealth, protocol.MethodShutdown, protocol.MethodCancel)
	for method := range s.handlers {
		methods = append(methods, method)
	}
	s.mu.RUnlock()
	sort.Slice(methods, func(i, j int) bool { return methods[i] < methods[j] })
	payload := protocol.HandshakeResponse{
		ProtocolVersion: protocol.Version,
		SidecarVersion:  s.build.SidecarVersion, LLMKitVersion: s.build.LLMKitVersion,
		BuildID: s.build.BuildID, InstanceID: s.build.InstanceID,
		ClientID: principal.ClientID, ClientInstanceID: principal.ClientInstanceID,
		UserID: principal.UserID, BindingVersion: principal.BindingVersion,
		Methods: methods,
	}
	return writer.result(request.RequestID, payload)
}

func (s *Server) dispatch(ctx context.Context, request protocol.Request, writer *lockedWriter) {
	switch request.Method {
	case protocol.MethodHealth:
		_ = writer.result(request.RequestID, protocol.HealthResponse{Status: "ok"})
	case protocol.MethodShutdown:
		principal, _ := identity.FromContext(ctx)
		if !principal.HasScope("shutdown") {
			_ = writer.write(errorResponse(request.RequestID, "permission", "shutdown is not permitted"))
			return
		}
		_ = writer.result(request.RequestID, struct{}{})
		if s.shutdown != nil {
			s.shutdown()
		}
	default:
		s.mu.RLock()
		handler := s.handlers[request.Method]
		s.mu.RUnlock()
		if handler == nil {
			_ = writer.write(errorResponse(request.RequestID, "unsupported_method", "sidecar method is not available"))
			return
		}
		payload, err := handler(ctx, request.Payload)
		if err != nil {
			_ = writer.write(responseForError(request.RequestID, err))
			return
		}
		if sequence, ok := payload.(EventSequence); ok {
			if sequence.Run == nil {
				_ = writer.write(errorResponse(request.RequestID, "internal", "sidecar event bridge is invalid"))
				return
			}
			emitter := eventEmitter{requestID: request.RequestID, writer: writer}
			if err := sequence.Run(ctx, emitter); err != nil {
				_ = writer.write(responseForError(request.RequestID, err))
				return
			}
			_ = writer.write(protocol.Response{
				Version: protocol.Version, RequestID: request.RequestID, Type: protocol.TypeEnd,
			})
			return
		}
		_ = writer.result(request.RequestID, payload)
	}
}

type eventEmitter struct {
	requestID string
	writer    *lockedWriter
}

func (e eventEmitter) Emit(value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return errors.New("sidecar server: event encoding failed")
	}
	defer clear(payload)
	return e.writer.write(protocol.Response{
		Version: protocol.Version, RequestID: e.requestID,
		Type: protocol.TypeEvent, Payload: payload,
	})
}

func (s *Server) handleCancel(
	request protocol.Request,
	writer *lockedWriter,
	activeMu *sync.Mutex,
	active map[string]context.CancelFunc,
) {
	var cancelRequest protocol.CancelRequest
	if json.Unmarshal(request.Payload, &cancelRequest) != nil || cancelRequest.RequestID == "" {
		_ = writer.write(errorResponse(request.RequestID, "invalid_request", "cancel request_id is required"))
		return
	}
	activeMu.Lock()
	cancel := active[cancelRequest.RequestID]
	activeMu.Unlock()
	if cancel != nil {
		cancel()
	}
	_ = writer.result(request.RequestID, struct{}{})
}

type lockedWriter struct {
	mu    sync.Mutex
	codec *protocol.Codec
}

func (w *lockedWriter) write(response protocol.Response) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.codec.WriteResponse(response)
}

func (w *lockedWriter) result(requestID string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	defer clear(payload)
	return w.write(protocol.Response{
		Version: protocol.Version, RequestID: requestID,
		Type: protocol.TypeResult, Payload: payload,
	})
}

func errorResponse(requestID, kind, message string) protocol.Response {
	return protocol.Response{
		Version: protocol.Version, RequestID: requestID, Type: protocol.TypeError,
		Error: &protocol.Error{Kind: kind, Message: message},
	}
}

func responseForError(requestID string, err error) protocol.Response {
	var providerErr *llmkit.ProviderError
	if errors.As(err, &providerErr) {
		return protocol.Response{
			Version: protocol.Version, RequestID: requestID, Type: protocol.TypeError,
			Error: &protocol.Error{
				Kind: string(providerErr.Kind), Message: providerErr.SafeMessage,
				Retryable:    providerErr.Retryable,
				RetryAfterMS: providerErr.RetryAfter.Milliseconds(),
				StatusCode:   providerErr.StatusCode, ProviderCode: providerErr.ProviderCode,
				RequestID: providerErr.RequestID,
			},
		}
	}
	return errorResponse(requestID, "internal", "sidecar operation failed")
}
