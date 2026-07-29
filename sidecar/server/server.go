// Package server implements authenticated llmkit-sidecar v1 connection
// dispatch. Network listener ownership stays with the command or host.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/uuos-ai/llmkit"
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
}

type Config struct {
	SessionKey    []byte
	MaxFrameBytes uint32
	Build         BuildInfo
	Shutdown      func()
}

type Server struct {
	sessionKey []byte
	maxFrame   uint32
	build      BuildInfo
	shutdown   func()
	mu         sync.RWMutex
	handlers   map[protocol.Method]Handler
}

func New(config Config) (*Server, error) {
	if len(config.SessionKey) < 32 {
		return nil, fmt.Errorf("sidecar server: session key must contain at least 32 bytes")
	}
	key := append([]byte(nil), config.SessionKey...)
	return &Server{
		sessionKey: key, maxFrame: config.MaxFrameBytes,
		build: config.Build, shutdown: config.Shutdown,
		handlers: make(map[protocol.Method]Handler),
	}, nil
}

func (s *Server) Close() { clear(s.sessionKey) }

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
	if first.Method != protocol.MethodHandshake || !s.authenticated(first.SessionKey) {
		_ = writer.write(errorResponse(first.RequestID, "authentication", "sidecar handshake rejected"))
		return errors.New("sidecar server: handshake rejected")
	}
	if err := s.handleHandshake(first, writer); err != nil {
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
		if !s.authenticated(request.SessionKey) || request.Version != protocol.Version {
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

func (s *Server) handleHandshake(request protocol.Request, writer *lockedWriter) error {
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
	payload := protocol.HandshakeResponse{
		ProtocolVersion: protocol.Version,
		SidecarVersion:  s.build.SidecarVersion, LLMKitVersion: s.build.LLMKitVersion,
		BuildID: s.build.BuildID,
		Methods: []protocol.Method{
			protocol.MethodHandshake, protocol.MethodHealth, protocol.MethodShutdown,
			protocol.MethodCancel, protocol.MethodListCapabilities,
			protocol.MethodValidateCredential, protocol.MethodGenerate, protocol.MethodEmbed,
		},
	}
	return writer.result(request.RequestID, payload)
}

func (s *Server) dispatch(ctx context.Context, request protocol.Request, writer *lockedWriter) {
	switch request.Method {
	case protocol.MethodHealth:
		_ = writer.result(request.RequestID, protocol.HealthResponse{Status: "ok"})
	case protocol.MethodShutdown:
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

func (s *Server) authenticated(candidate string) bool {
	want := s.sessionKey
	got := []byte(candidate)
	return len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
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
