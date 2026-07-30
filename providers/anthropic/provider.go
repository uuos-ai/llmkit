// Package anthropic implements the Anthropic Messages protocol.
package anthropic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/transport"
)

const (
	DefaultProviderID llmkit.ProviderID = "anthropic"
	DefaultEndpoint                     = "https://api.anthropic.com"
	DefaultVersion                      = "2023-06-01"
)

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Version   string
	Transport *transport.Client
}

type Provider struct {
	id        llmkit.ProviderID
	endpoint  string
	version   string
	transport *transport.Client
}

func New(config Config) (*Provider, error) {
	id := config.ID
	if id == "" {
		id = DefaultProviderID
	}
	endpoint := strings.TrimRight(config.Endpoint, "/")
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("anthropic: invalid endpoint: %w", err)
	}
	version := config.Version
	if version == "" {
		version = DefaultVersion
	}
	client := config.Transport
	if client == nil {
		client = transport.New(transport.Config{})
	}
	return &Provider{id: id, endpoint: endpoint, version: version, transport: client}, nil
}

func (p *Provider) ID() llmkit.ProviderID { return p.id }

func (p *Provider) Manifest() llmkit.AdapterManifest {
	return llmkit.AdapterManifest{ProviderID: p.id, AdapterVersion: "1.0.0", ProviderAPIVersion: p.version, Maturity: llmkit.AdapterConformant,
		Operations:   []llmkit.Operation{llmkit.OperationGenerate},
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityTools, llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning},
		AuthSchemes:  []llmkit.AuthScheme{llmkit.AuthAPIKeyHeader}, Profile: "anthropic-messages"}
}

func (p *Provider) Capabilities(_ context.Context, target llmkit.Target) (llmkit.Capabilities, error) {
	if err := p.validateTarget(target); err != nil {
		return llmkit.Capabilities{}, err
	}
	return llmkit.Capabilities{
		Provider: p.id,
		Models: map[llmkit.ModelID]llmkit.ModelCapabilities{
			target.Model: {Capabilities: []llmkit.Capability{
				llmkit.CapabilityGenerate,
				llmkit.CapabilityStreaming,
				llmkit.CapabilityTools,
				llmkit.CapabilityStructured,
				llmkit.CapabilityVision,
				llmkit.CapabilityReasoning,
			}},
		},
	}, nil
}

func (p *Provider) ValidateCredential(ctx context.Context, call llmkit.CredentialCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	headers := make(http.Header)
	headers.Set("anthropic-version", p.version)
	request, err := transport.NewJSONRequest(
		ctx, call.Target, call.Credential, http.MethodGet,
		p.endpointFor(call.Target)+"/v1/models?limit=1", nil, headers,
	)
	if err != nil {
		return err
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return p.classifyError(call.Target, err)
	}
	_ = response.Body.Close()
	return nil
}

func (p *Provider) ListModels(ctx context.Context, call llmkit.ListModelsCall) (llmkit.ModelPage, error) {
	if call.Target.Provider != p.id {
		return llmkit.ModelPage{}, invalidRequest(call.Target, "target provider does not match adapter")
	}
	if call.Limit < 0 || call.Limit > 1000 {
		return llmkit.ModelPage{}, invalidRequest(call.Target, "model list limit must be between 1 and 1000")
	}
	endpoint, err := url.Parse(p.endpointFor(call.Target) + "/v1/models")
	if err != nil {
		return llmkit.ModelPage{}, invalidRequest(call.Target, "provider model endpoint is invalid")
	}
	query := endpoint.Query()
	if call.Cursor != "" {
		query.Set("after_id", call.Cursor)
	}
	if call.Limit > 0 {
		query.Set("limit", fmt.Sprint(call.Limit))
	}
	endpoint.RawQuery = query.Encode()
	headers := make(http.Header)
	headers.Set("anthropic-version", p.version)
	request, err := transport.NewJSONRequest(
		ctx, call.Target, call.Credential, http.MethodGet, endpoint.String(), nil, headers,
	)
	if err != nil {
		return llmkit.ModelPage{}, err
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return llmkit.ModelPage{}, p.classifyError(call.Target, err)
	}
	var wire struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := p.transport.DecodeJSON(call.Target, response, &wire); err != nil {
		return llmkit.ModelPage{}, err
	}
	page := llmkit.ModelPage{Provider: p.id, Models: make([]llmkit.ModelInfo, 0, len(wire.Data))}
	for _, model := range wire.Data {
		if model.ID != "" {
			page.Models = append(page.Models, llmkit.ModelInfo{
				ID: llmkit.ModelID(model.ID), DisplayName: model.DisplayName,
			})
		}
	}
	if wire.HasMore {
		page.NextCursor = wire.LastID
	}
	return page, nil
}

func (p *Provider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	if err := p.validateCall(call); err != nil {
		return llmkit.Response{}, err
	}
	payload, err := encodeRequest(call, false)
	if err != nil {
		return llmkit.Response{}, invalidRequest(call.Target, err.Error())
	}
	request, err := p.newRequest(ctx, call, payload)
	if err != nil {
		return llmkit.Response{}, err
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return llmkit.Response{}, p.classifyError(call.Target, err)
	}
	var wire messageResponse
	if err := p.transport.DecodeJSON(call.Target, response, &wire); err != nil {
		return llmkit.Response{}, err
	}
	return decodeResponse(call.Target, wire)
}

func (p *Provider) Stream(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	if err := p.validateCall(call); err != nil {
		return nil, err
	}
	payload, err := encodeRequest(call, true)
	if err != nil {
		return nil, invalidRequest(call.Target, err.Error())
	}
	request, err := p.newRequest(ctx, call, payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return nil, p.classifyError(call.Target, err)
	}
	return newMessageStream(call.Target, response), nil
}

func (p *Provider) newRequest(ctx context.Context, call llmkit.GenerateCall, payload any) (*http.Request, error) {
	headers := make(http.Header)
	headers.Set("anthropic-version", p.version)
	request, err := transport.NewJSONRequest(
		ctx,
		call.Target,
		call.Credential,
		http.MethodPost,
		p.endpointFor(call.Target)+"/v1/messages",
		payload,
		headers,
	)
	if err != nil {
		return nil, err
	}
	if call.OperationID != "" {
		request.Header.Set("X-Client-Request-ID", call.OperationID)
	}
	return request, nil
}

func (p *Provider) validateCall(call llmkit.GenerateCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	if len(call.Request.Messages) == 0 {
		return invalidRequest(call.Target, "at least one message is required")
	}
	if len(call.Request.ProviderOptions) != 0 {
		return invalidRequest(call.Target, "Anthropic provider options are not yet supported")
	}
	return nil
}

func (p *Provider) validateTarget(target llmkit.Target) error {
	if target.Provider != p.id {
		return invalidRequest(target, "target provider does not match adapter")
	}
	if target.Model == "" {
		return invalidRequest(target, "target model is required")
	}
	return nil
}

func (p *Provider) endpointFor(target llmkit.Target) string {
	if target.Endpoint != "" {
		return strings.TrimRight(target.Endpoint, "/")
	}
	return p.endpoint
}

func (p *Provider) classifyError(target llmkit.Target, err error) error {
	var httpErr *transport.HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	var envelope struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	_ = httpErr.DecodeJSON(&envelope)
	kind := llmkit.ErrorUnknown
	retryable := false
	switch httpErr.StatusCode() {
	case http.StatusUnauthorized:
		kind = llmkit.ErrorAuthentication
	case http.StatusForbidden:
		kind = llmkit.ErrorPermission
	case http.StatusNotFound:
		kind = llmkit.ErrorModelNotFound
	case http.StatusRequestEntityTooLarge:
		kind = llmkit.ErrorContextLength
	case http.StatusTooManyRequests:
		kind = llmkit.ErrorRateLimit
		retryable = true
	case 529:
		kind = llmkit.ErrorOverloaded
		retryable = true
	default:
		if httpErr.StatusCode() >= 500 {
			kind = llmkit.ErrorOverloaded
			retryable = true
		} else if httpErr.StatusCode() >= 400 {
			kind = llmkit.ErrorInvalidRequest
		}
	}
	return &llmkit.ProviderError{
		Provider:     target.Provider,
		Model:        target.Model,
		Kind:         kind,
		StatusCode:   httpErr.StatusCode(),
		Retryable:    retryable,
		RetryAfter:   httpErr.RetryAfter(),
		SafeMessage:  safeMessage(kind),
		ProviderCode: envelope.Error.Type,
		RequestID:    httpErr.RequestID(),
	}
}

func invalidRequest(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorInvalidRequest, SafeMessage: message,
	}
}

func safeMessage(kind llmkit.ErrorKind) string {
	switch kind {
	case llmkit.ErrorAuthentication:
		return "provider authentication failed"
	case llmkit.ErrorPermission:
		return "provider permission denied"
	case llmkit.ErrorRateLimit:
		return "provider rate limit exceeded"
	case llmkit.ErrorModelNotFound:
		return "provider model not found"
	case llmkit.ErrorContextLength:
		return "provider context length exceeded"
	case llmkit.ErrorOverloaded:
		return "provider is temporarily unavailable"
	default:
		return "provider rejected the request"
	}
}

var _ llmkit.Generator = (*Provider)(nil)
var _ llmkit.StreamGenerator = (*Provider)(nil)
var _ llmkit.CredentialValidator = (*Provider)(nil)
var _ llmkit.ModelLister = (*Provider)(nil)
