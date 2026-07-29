// Package gemini implements the native Google Gemini generateContent protocol.
package gemini

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
	DefaultProviderID llmkit.ProviderID = "gemini"
	DefaultEndpoint                     = "https://generativelanguage.googleapis.com"
	DefaultAPIVersion                   = "v1beta"
)

type Config struct {
	ID         llmkit.ProviderID
	Endpoint   string
	APIVersion string
	Transport  *transport.Client
}

type Provider struct {
	id         llmkit.ProviderID
	endpoint   string
	apiVersion string
	transport  *transport.Client
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
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("gemini: invalid endpoint")
	}
	version := strings.Trim(config.APIVersion, "/")
	if version == "" {
		version = DefaultAPIVersion
	}
	client := config.Transport
	if client == nil {
		client = transport.New(transport.Config{})
	}
	return &Provider{id: id, endpoint: endpoint, apiVersion: version, transport: client}, nil
}

func (p *Provider) ID() llmkit.ProviderID { return p.id }

func (p *Provider) Capabilities(_ context.Context, target llmkit.Target) (llmkit.Capabilities, error) {
	if err := p.validateTarget(target); err != nil {
		return llmkit.Capabilities{}, err
	}
	return llmkit.Capabilities{Provider: p.id, Models: map[llmkit.ModelID]llmkit.ModelCapabilities{
		target.Model: {Capabilities: []llmkit.Capability{
			llmkit.CapabilityGenerate, llmkit.CapabilityStreaming,
			llmkit.CapabilityEmbedding, llmkit.CapabilityTools,
			llmkit.CapabilityStructured, llmkit.CapabilityVision,
			llmkit.CapabilityReasoning,
		}},
	}}, nil
}

func (p *Provider) ValidateCredential(ctx context.Context, call llmkit.CredentialCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	endpoint := p.endpoint
	if call.Target.Endpoint != "" {
		endpoint = strings.TrimRight(call.Target.Endpoint, "/")
	}
	path := fmt.Sprintf("%s/%s/models/%s", endpoint, p.apiVersion, url.PathEscape(modelName(call.Target.Model)))
	request, err := transport.NewJSONRequest(
		ctx, call.Target, call.Credential, http.MethodGet, path, nil, nil,
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

func (p *Provider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	if err := p.validateCall(call); err != nil {
		return llmkit.Response{}, err
	}
	payload, err := encodeRequest(call)
	if err != nil {
		return llmkit.Response{}, invalidRequest(call.Target, err.Error())
	}
	request, err := p.newRequest(ctx, call.Target, call.Credential, "generateContent", "", payload)
	if err != nil {
		return llmkit.Response{}, err
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return llmkit.Response{}, p.classifyError(call.Target, err)
	}
	var wire generateResponse
	if err := p.transport.DecodeJSON(call.Target, response, &wire); err != nil {
		return llmkit.Response{}, err
	}
	return decodeResponse(call.Target, wire)
}

func (p *Provider) Stream(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	if err := p.validateCall(call); err != nil {
		return nil, err
	}
	payload, err := encodeRequest(call)
	if err != nil {
		return nil, invalidRequest(call.Target, err.Error())
	}
	request, err := p.newRequest(ctx, call.Target, call.Credential, "streamGenerateContent", "alt=sse", payload)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return nil, p.classifyError(call.Target, err)
	}
	return newGenerateStream(call.Target, response), nil
}

func (p *Provider) Embed(ctx context.Context, call llmkit.EmbedCall) (llmkit.EmbedResponse, error) {
	if err := p.validateTarget(call.Target); err != nil {
		return llmkit.EmbedResponse{}, err
	}
	if len(call.Input) == 0 {
		return llmkit.EmbedResponse{}, invalidRequest(call.Target, "at least one embedding input is required")
	}
	model := modelResource(call.Target.Model)
	requests := make([]map[string]any, 0, len(call.Input))
	for _, input := range call.Input {
		item := map[string]any{
			"model":   model,
			"content": map[string]any{"parts": []map[string]any{{"text": input}}},
		}
		if call.Dimensions != nil {
			item["embedContentConfig"] = map[string]any{"outputDimensionality": *call.Dimensions}
		}
		requests = append(requests, item)
	}
	payload := map[string]any{"requests": requests}
	request, err := p.newRequest(ctx, call.Target, call.Credential, "batchEmbedContents", "", payload)
	if err != nil {
		return llmkit.EmbedResponse{}, err
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return llmkit.EmbedResponse{}, p.classifyError(call.Target, err)
	}
	var wire struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
		Usage usageDTO `json:"usageMetadata"`
	}
	if err := p.transport.DecodeJSON(call.Target, response, &wire); err != nil {
		return llmkit.EmbedResponse{}, err
	}
	if len(wire.Embeddings) != len(call.Input) {
		return llmkit.EmbedResponse{}, malformed(call.Target, "provider returned an unexpected embedding count")
	}
	vectors := make([][]float32, len(wire.Embeddings))
	for index := range wire.Embeddings {
		if len(wire.Embeddings[index].Values) == 0 {
			return llmkit.EmbedResponse{}, malformed(call.Target, "provider returned an empty embedding")
		}
		vectors[index] = wire.Embeddings[index].Values
	}
	return llmkit.EmbedResponse{Vectors: vectors, Usage: normalizeUsage(wire.Usage)}, nil
}

func (p *Provider) newRequest(
	ctx context.Context,
	target llmkit.Target,
	credential llmkit.CredentialHandle,
	method string,
	rawQuery string,
	payload any,
) (*http.Request, error) {
	endpoint := p.endpoint
	if target.Endpoint != "" {
		endpoint = strings.TrimRight(target.Endpoint, "/")
	}
	path := fmt.Sprintf("%s/%s/models/%s:%s", endpoint, p.apiVersion, url.PathEscape(modelName(target.Model)), method)
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	return transport.NewJSONRequest(ctx, target, credential, http.MethodPost, path, payload, nil)
}

func (p *Provider) validateCall(call llmkit.GenerateCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	if len(call.Request.Messages) == 0 {
		return invalidRequest(call.Target, "at least one message is required")
	}
	if len(call.Request.ProviderOptions) != 0 {
		return invalidRequest(call.Target, "Gemini provider options are not yet supported")
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

func (p *Provider) classifyError(target llmkit.Target, err error) error {
	var httpErr *transport.HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	var envelope struct {
		Error struct {
			Status string `json:"status"`
		} `json:"error"`
	}
	_ = httpErr.DecodeJSON(&envelope)
	kind, retryable := llmkit.ErrorUnknown, false
	switch httpErr.StatusCode() {
	case http.StatusBadRequest:
		kind = llmkit.ErrorInvalidRequest
	case http.StatusUnauthorized:
		kind = llmkit.ErrorAuthentication
	case http.StatusForbidden:
		kind = llmkit.ErrorPermission
	case http.StatusNotFound:
		kind = llmkit.ErrorModelNotFound
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		kind, retryable = llmkit.ErrorRateLimit, true
	default:
		if httpErr.StatusCode() >= 500 {
			kind, retryable = llmkit.ErrorOverloaded, true
		}
	}
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model, Kind: kind,
		StatusCode: httpErr.StatusCode(), Retryable: retryable,
		RetryAfter: httpErr.RetryAfter(), SafeMessage: safeMessage(kind),
		ProviderCode: envelope.Error.Status, RequestID: httpErr.RequestID(),
	}
}

func modelName(model llmkit.ModelID) string {
	return strings.TrimPrefix(string(model), "models/")
}

func modelResource(model llmkit.ModelID) string { return "models/" + modelName(model) }

func invalidRequest(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorInvalidRequest, SafeMessage: message,
	}
}

func malformed(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider: target.Provider, Model: target.Model,
		Kind: llmkit.ErrorMalformedResponse, Phase: llmkit.PhaseDecode, SafeMessage: message,
	}
}

func safeMessage(kind llmkit.ErrorKind) string {
	switch kind {
	case llmkit.ErrorAuthentication:
		return "provider authentication failed"
	case llmkit.ErrorPermission:
		return "provider permission denied"
	case llmkit.ErrorModelNotFound:
		return "provider model not found"
	case llmkit.ErrorRateLimit:
		return "provider rate limit exceeded"
	case llmkit.ErrorOverloaded:
		return "provider is temporarily unavailable"
	default:
		return "provider rejected the request"
	}
}

var _ llmkit.Generator = (*Provider)(nil)
var _ llmkit.StreamGenerator = (*Provider)(nil)
var _ llmkit.Embedder = (*Provider)(nil)
var _ llmkit.CredentialValidator = (*Provider)(nil)
