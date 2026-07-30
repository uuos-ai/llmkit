// Package gateway exposes the normalized llmkit contract over HTTPS JSON and
// server-sent events. TLS listener ownership stays with the command or host.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
	"github.com/uuos-ai/llmkit/routing"
	streamcontract "github.com/uuos-ai/llmkit/stream"
)

type Config struct {
	InstanceID      string
	Registry        *llmkit.Registry
	Authenticator   identity.Authenticator
	ConfigStore     managed.ConfigStore
	SecretStore     managed.SecretStore
	AuditStore      managed.AuditStore
	CustomProviders managed.CustomProviderStore
	EndpointPolicy  func(llmkit.Target) error
	MaxBodyBytes    int64
}

type Server struct {
	config         Config
	handler        http.Handler
	dataHandler    http.Handler
	controlHandler http.Handler
}

func New(config Config) (*Server, error) {
	if config.Registry == nil || config.Authenticator == nil || config.ConfigStore == nil || config.SecretStore == nil || config.AuditStore == nil {
		return nil, errors.New("gateway: registry, authenticator, and external config, secret, and audit stores are required")
	}
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 8 << 20
	}
	server := &Server{config: config}
	data := http.NewServeMux()
	data.HandleFunc("GET /v1/health", server.health)
	data.Handle("GET /v1/available-targets", server.authenticate(http.HandlerFunc(server.providerOptions)))
	data.Handle("GET /v1/provider-options", server.authenticate(http.HandlerFunc(server.providerOptions)))
	data.Handle("GET /v1/models", server.authenticate(http.HandlerFunc(server.openAIModels)))
	data.Handle("POST /v1/generate", server.authenticate(http.HandlerFunc(server.generate)))
	data.Handle("POST /v1/embed", server.authenticate(http.HandlerFunc(server.embed)))
	data.Handle("POST /v1/rerank", server.authenticate(http.HandlerFunc(server.rerank)))
	data.Handle("POST /v1/moderate", server.authenticate(http.HandlerFunc(server.moderate)))
	control := http.NewServeMux()
	control.Handle("PUT /v1/custom-providers", server.authenticate(http.HandlerFunc(server.upsertCustomProvider)))
	control.Handle("DELETE /v1/custom-providers/{provider_id}", server.authenticate(http.HandlerFunc(server.deleteCustomProvider)))
	combined := http.NewServeMux()
	combined.Handle("PUT /v1/custom-providers", control)
	combined.Handle("DELETE /v1/custom-providers/{provider_id}", control)
	combined.Handle("/", data)
	server.dataHandler, server.controlHandler = securityHeaders(data), securityHeaders(control)
	server.handler = securityHeaders(combined)
	return server, nil
}

func (s *Server) Handler() http.Handler        { return s.handler }
func (s *Server) DataHandler() http.Handler    { return s.dataHandler }
func (s *Server) ControlHandler() http.Handler { return s.controlHandler }

func (s *Server) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok", "instance_id": s.config.InstanceID})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		header := request.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			writeAPIError(writer, http.StatusUnauthorized, "authentication", "bearer token is required")
			return
		}
		token := []byte(strings.TrimPrefix(header, "Bearer "))
		principal, ok := s.config.Authenticator.Authenticate(request.Context(), token)
		clear(token)
		if !ok {
			writeAPIError(writer, http.StatusUnauthorized, "authentication", "client token is invalid")
			return
		}
		next.ServeHTTP(writer, request.WithContext(identity.WithPrincipal(request.Context(), principal)))
	})
}

func (s *Server) providerOptions(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeTargetsRead) {
		return
	}
	principal, _ := identity.FromContext(request.Context())
	options, err := s.config.ConfigStore.ProviderOptions(request.Context(), principal)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, options)
}

func (s *Server) openAIModels(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeTargetsRead) {
		return
	}
	principal, _ := identity.FromContext(request.Context())
	options, err := s.config.ConfigStore.ProviderOptions(request.Context(), principal)
	if err != nil {
		writeError(writer, err)
		return
	}
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	models := []model{{ID: "llmkit-default", Object: "model", OwnedBy: "llmkit"}}
	for _, provider := range options.Providers {
		for _, target := range provider.Targets {
			models = append(models, model{ID: target.ID, Object: "model", OwnedBy: string(target.OwnerScope)})
		}
	}
	writeJSON(writer, http.StatusOK, map[string]any{"object": "list", "data": models, "revision": options.Revision})
}

type generateRequest struct {
	OperationID string                 `json:"operation_id,omitempty"`
	TargetID    string                 `json:"target_id,omitempty"`
	Request     llmkit.GenerateRequest `json:"request"`
	Stream      bool                   `json:"stream,omitempty"`
}

func (s *Server) generate(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeInferenceExecute) {
		return
	}
	var input generateRequest
	if !s.decode(writer, request, &input) {
		return
	}
	principal, _ := identity.FromContext(request.Context())
	target, credential, release, err := s.resolveCredential(request.Context(), principal, input.TargetID)
	if err != nil {
		s.audit(request.Context(), principal, "generate", input.OperationID, input.TargetID, llmkit.Usage{}, err)
		writeError(writer, err)
		return
	}
	defer release()
	call := llmkit.GenerateCall{OperationID: input.OperationID, Target: target.Target, Credential: credential, Request: input.Request}
	if input.Stream {
		s.streamGenerate(writer, request, principal, target, call)
		return
	}
	generator, ok := s.config.Registry.Generator(target.Target.Provider)
	if !ok {
		err = providerError(target.Target, llmkit.ErrorUnsupported, "provider does not support generation")
	} else {
		var response llmkit.Response
		response, err = generator.Generate(request.Context(), call)
		if err == nil {
			s.audit(request.Context(), principal, "generate", input.OperationID, target.ID, response.Usage, nil)
			writeJSON(writer, http.StatusOK, map[string]any{"target_id": target.ID, "target": target.Target, "response": response})
			return
		}
	}
	s.audit(request.Context(), principal, "generate", input.OperationID, target.ID, llmkit.Usage{}, err)
	writeError(writer, err)
}

func (s *Server) streamGenerate(writer http.ResponseWriter, request *http.Request, principal identity.Principal, target managed.Target, call llmkit.GenerateCall) {
	generator, ok := s.config.Registry.StreamGenerator(target.Target.Provider)
	if !ok {
		writeError(writer, providerError(target.Target, llmkit.ErrorUnsupported, "provider does not support streaming"))
		return
	}
	stream, err := generator.Stream(request.Context(), call)
	if err != nil {
		writeError(writer, err)
		return
	}
	defer stream.Close()
	flusher, ok := writer.(http.Flusher)
	if !ok {
		writeAPIError(writer, http.StatusInternalServerError, "internal", "streaming is unavailable")
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	normalizer := streamcontract.NewNormalizer(call.OperationID, call.OperationID)
	created, _ := normalizer.Start()
	created.TargetID = target.ID
	writeSSE(writer, string(created.Type), created)
	flusher.Flush()
	usage := llmkit.Usage{Source: llmkit.UsageMissing}
	for {
		event, receiveErr := stream.Recv()
		if receiveErr == io.EOF {
			if !normalizer.Terminal() {
				failed := normalizer.Fail(&llmkit.ProviderError{Kind: llmkit.ErrorProtocol, SafeMessage: "provider stream ended without a terminal event"})
				writeSSE(writer, string(failed.Type), failed)
			}
			flusher.Flush()
			s.audit(request.Context(), principal, "generate_stream", call.OperationID, target.ID, usage, nil)
			return
		}
		if receiveErr != nil {
			failed := normalizer.Fail(receiveErr)
			writeSSE(writer, string(failed.Type), failed)
			flusher.Flush()
			s.audit(request.Context(), principal, "generate_stream", call.OperationID, target.ID, usage, receiveErr)
			return
		}
		if event.Usage != nil {
			usage = *event.Usage
		}
		normalized, normalizeErr := normalizer.Accept(event)
		if normalizeErr != nil {
			failed := normalizer.Fail(&llmkit.ProviderError{Kind: llmkit.ErrorProtocol, SafeMessage: "provider emitted an invalid stream event"})
			writeSSE(writer, string(failed.Type), failed)
			flusher.Flush()
			s.audit(request.Context(), principal, "generate_stream", call.OperationID, target.ID, usage, normalizeErr)
			return
		}
		writeSSE(writer, string(normalized.Type), normalized)
		flusher.Flush()
	}
}

type embedRequest struct {
	OperationID string   `json:"operation_id,omitempty"`
	TargetID    string   `json:"target_id,omitempty"`
	Input       []string `json:"input"`
	Dimensions  *int     `json:"dimensions,omitempty"`
}

func (s *Server) embed(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeInferenceExecute) {
		return
	}
	var input embedRequest
	if !s.decode(writer, request, &input) {
		return
	}
	principal, _ := identity.FromContext(request.Context())
	target, credential, release, err := s.resolveCredential(request.Context(), principal, input.TargetID)
	if err != nil {
		writeError(writer, err)
		return
	}
	defer release()
	embedder, ok := s.config.Registry.Embedder(target.Target.Provider)
	if !ok {
		writeError(writer, providerError(target.Target, llmkit.ErrorUnsupported, "provider does not support embeddings"))
		return
	}
	response, err := embedder.Embed(request.Context(), llmkit.EmbedCall{OperationID: input.OperationID, Target: target.Target, Credential: credential, Input: input.Input, Dimensions: input.Dimensions})
	s.audit(request.Context(), principal, "embed", input.OperationID, target.ID, response.Usage, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"target_id": target.ID, "target": target.Target, "response": response})
}

type rerankRequest struct {
	OperationID string   `json:"operation_id,omitempty"`
	TargetID    string   `json:"target_id,omitempty"`
	Query       string   `json:"query"`
	Documents   []string `json:"documents"`
	TopN        *int     `json:"top_n,omitempty"`
}

func (s *Server) rerank(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeInferenceExecute) {
		return
	}
	var input rerankRequest
	if !s.decode(writer, request, &input) {
		return
	}
	principal, _ := identity.FromContext(request.Context())
	target, credential, release, err := s.resolveCredential(request.Context(), principal, input.TargetID)
	if err != nil {
		writeError(writer, err)
		return
	}
	defer release()
	reranker, ok := s.config.Registry.Reranker(target.Target.Provider)
	if !ok {
		writeError(writer, providerError(target.Target, llmkit.ErrorCapabilityNotSupported, "provider does not support reranking"))
		return
	}
	response, err := reranker.Rerank(request.Context(), llmkit.RerankCall{OperationID: input.OperationID, Target: target.Target, Credential: credential, Query: input.Query, Documents: input.Documents, TopN: input.TopN})
	s.audit(request.Context(), principal, "rerank", input.OperationID, target.ID, response.Usage, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"target_id": target.ID, "response": response})
}

type moderateRequest struct {
	OperationID string               `json:"operation_id,omitempty"`
	TargetID    string               `json:"target_id,omitempty"`
	Content     []llmkit.ContentPart `json:"content"`
}

func (s *Server) moderate(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeInferenceExecute) {
		return
	}
	var input moderateRequest
	if !s.decode(writer, request, &input) {
		return
	}
	principal, _ := identity.FromContext(request.Context())
	target, credential, release, err := s.resolveCredential(request.Context(), principal, input.TargetID)
	if err != nil {
		writeError(writer, err)
		return
	}
	defer release()
	moderator, ok := s.config.Registry.Moderator(target.Target.Provider)
	if !ok {
		writeError(writer, providerError(target.Target, llmkit.ErrorCapabilityNotSupported, "provider does not support moderation"))
		return
	}
	response, err := moderator.Moderate(request.Context(), llmkit.ModerateCall{OperationID: input.OperationID, Target: target.Target, Credential: credential, Content: input.Content})
	s.audit(request.Context(), principal, "moderate", input.OperationID, target.ID, response.Usage, err)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"target_id": target.ID, "response": response})
}

func (s *Server) resolveCredential(ctx context.Context, principal identity.Principal, targetID string) (managed.Target, llmkit.CredentialHandle, func(), error) {
	if targetID == "llmkit-default" {
		targetID = ""
	}
	target, err := s.config.ConfigStore.ResolveTarget(ctx, principal, targetID)
	if err != nil {
		return managed.Target{}, nil, func() {}, err
	}
	if target.ID == "" || target.Target.Provider == "" || target.Target.Model == "" || target.CredentialRef == "" {
		return managed.Target{}, nil, func() {}, &llmkit.ProviderError{Kind: llmkit.ErrorInvalidRequest, SafeMessage: "resolved target is incomplete"}
	}
	if targetID != "" && target.ID != targetID {
		return managed.Target{}, nil, func() {}, &llmkit.ProviderError{Kind: llmkit.ErrorPermission, SafeMessage: "resolved target does not match requested target_id"}
	}
	if target.Target.Endpoint != "" {
		if s.config.EndpointPolicy == nil {
			return managed.Target{}, nil, func() {}, providerError(target.Target, llmkit.ErrorPermission, "custom endpoint is not permitted")
		}
		if err := s.config.EndpointPolicy(target.Target); err != nil {
			return managed.Target{}, nil, func() {}, err
		}
	}
	handle, release, err := s.config.SecretStore.OpenCredential(ctx, principal, target.CredentialRef)
	if release == nil {
		release = func() {}
	}
	if err == nil && handle == nil {
		err = errors.New("gateway: secret store returned an empty credential handle")
	}
	return target, handle, release, err
}

func (s *Server) upsertCustomProvider(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeProvidersWrite) || !requireScope(writer, request, identity.ScopeCredentialsWrite) {
		return
	}
	if s.config.CustomProviders == nil {
		writeAPIError(writer, http.StatusNotImplemented, "unsupported", "custom provider synchronization is unavailable")
		return
	}
	var input managed.CustomProviderInput
	if !s.decode(writer, request, &input) {
		return
	}
	defer clear(input.Credential.Value)
	principal, _ := identity.FromContext(request.Context())
	if input.BindingVersion != 0 && input.BindingVersion != principal.BindingVersion {
		writeAPIError(writer, http.StatusConflict, "user_binding_changed", "user binding changed")
		return
	}
	if input.IdempotencyKey == "" {
		input.IdempotencyKey = request.Header.Get("Idempotency-Key")
	}
	if input.IdempotencyKey == "" {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "Idempotency-Key is required")
		return
	}
	if err := routing.ValidateCustomProvider(input.Provider); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	options, err := s.config.CustomProviders.UpsertCustomProvider(request.Context(), principal, input)
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, options)
}

func (s *Server) deleteCustomProvider(writer http.ResponseWriter, request *http.Request) {
	if !requireScope(writer, request, identity.ScopeProvidersWrite) {
		return
	}
	if s.config.CustomProviders == nil {
		writeAPIError(writer, http.StatusNotImplemented, "unsupported", "custom provider synchronization is unavailable")
		return
	}
	principal, _ := identity.FromContext(request.Context())
	options, err := s.config.CustomProviders.DeleteCustomProvider(request.Context(), principal, request.PathValue("provider_id"))
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, options)
}

func (s *Server) decode(writer http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(writer, request.Body, s.config.MaxBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "request body is malformed")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeAPIError(writer, http.StatusBadRequest, "invalid_request", "exactly one JSON value is required")
		return false
	}
	return true
}

func (s *Server) audit(ctx context.Context, principal identity.Principal, operation, operationID, targetID string, usage llmkit.Usage, operationErr error) {
	event := managed.AuditEvent{Timestamp: time.Now().UTC(), ClientID: principal.ClientID, UserID: principal.UserID, BindingVersion: principal.BindingVersion, Operation: operation, OperationID: operationID, TargetID: targetID, Outcome: "success", Usage: usage}
	if operationErr != nil {
		event.Outcome = "error"
		event.ErrorKind = normalizedError(operationErr).Kind
	}
	_ = s.config.AuditStore.Append(ctx, event)
}

type apiError struct {
	Kind         string `json:"kind"`
	Message      string `json:"message"`
	Retryable    bool   `json:"retryable,omitempty"`
	RetryAfterMS int64  `json:"retry_after_ms,omitempty"`
	StatusCode   int    `json:"status_code,omitempty"`
	ProviderCode string `json:"provider_code,omitempty"`
	RequestID    string `json:"provider_request_id,omitempty"`
}

func normalizedError(err error) apiError {
	var providerErr *llmkit.ProviderError
	if errors.As(err, &providerErr) {
		return apiError{Kind: string(providerErr.Kind), Message: providerErr.SafeMessage, Retryable: providerErr.Retryable, RetryAfterMS: providerErr.RetryAfter.Milliseconds(), StatusCode: providerErr.StatusCode, ProviderCode: providerErr.ProviderCode, RequestID: providerErr.RequestID}
	}
	return apiError{Kind: "internal", Message: "request failed"}
}

func writeError(writer http.ResponseWriter, err error) {
	normalized := normalizedError(err)
	status := normalized.StatusCode
	if status < 400 || status > 599 {
		status = http.StatusBadGateway
		if normalized.Kind == string(llmkit.ErrorInvalidRequest) {
			status = http.StatusBadRequest
		}
	}
	writeJSON(writer, status, map[string]any{"error": normalized})
}

func writeAPIError(writer http.ResponseWriter, status int, kind, message string) {
	writeJSON(writer, status, map[string]any{"error": apiError{Kind: kind, Message: message}})
}

func requireScope(writer http.ResponseWriter, request *http.Request, scope string) bool {
	principal, ok := identity.FromContext(request.Context())
	if !ok || !principal.HasScope(scope) {
		writeAPIError(writer, http.StatusForbidden, string(llmkit.ErrorPermissionDenied), "operation is not permitted")
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeSSE(writer io.Writer, event string, value any) {
	encoded, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, encoded)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(writer, request)
	})
}

func providerError(target llmkit.Target, kind llmkit.ErrorKind, message string) error {
	return &llmkit.ProviderError{Provider: target.Provider, Model: target.Model, Kind: kind, SafeMessage: message}
}
