// Package openai implements OpenAI Chat Completions and Responses protocols.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/transport"
)

const (
	DefaultProviderID llmkit.ProviderID = "openai"
	DefaultEndpoint                     = "https://api.openai.com"
	OptionAPI                           = "openai.api"
)

type API string

const (
	APIChat      API = "chat_completions"
	APIResponses API = "responses"
)

type Config struct {
	ID       llmkit.ProviderID
	Endpoint string
	// APIPathPrefix is prepended to protocol paths. The default is "/v1".
	// Set it to "/" for compatible APIs whose base endpoint already routes
	// directly to /chat/completions.
	APIPathPrefix string
	DefaultAPI    API
	Transport     *transport.Client
	// ResolveURL maps logical OpenAI paths such as /v1/chat/completions to a
	// provider-specific URL. It is used by profiles such as Azure OpenAI.
	ResolveURL func(llmkit.Target, string) (string, error)
}

type Provider struct {
	id            llmkit.ProviderID
	endpoint      string
	apiPathPrefix string
	defaultAPI    API
	transport     *transport.Client
	resolveURL    func(llmkit.Target, string) (string, error)
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
		return nil, fmt.Errorf("openai: invalid endpoint: %w", err)
	}
	defaultAPI := config.DefaultAPI
	if defaultAPI == "" {
		defaultAPI = APIResponses
	}
	if defaultAPI != APIChat && defaultAPI != APIResponses {
		return nil, fmt.Errorf("openai: unsupported default API %q", defaultAPI)
	}
	client := config.Transport
	if client == nil {
		client = transport.New(transport.Config{})
	}
	pathPrefix := config.APIPathPrefix
	if pathPrefix == "" {
		pathPrefix = "/v1"
	} else if pathPrefix == "/" {
		pathPrefix = ""
	} else {
		pathPrefix = "/" + strings.Trim(pathPrefix, "/")
	}
	return &Provider{
		id: id, endpoint: endpoint, apiPathPrefix: pathPrefix,
		defaultAPI: defaultAPI, transport: client, resolveURL: config.ResolveURL,
	}, nil
}

func (p *Provider) ID() llmkit.ProviderID { return p.id }

func (p *Provider) Manifest() llmkit.AdapterManifest {
	return llmkit.AdapterManifest{ProviderID: p.id, AdapterVersion: "1.0.0", ProviderAPIVersion: "v1", Maturity: llmkit.AdapterConformant,
		Operations:   []llmkit.Operation{llmkit.OperationGenerate, llmkit.OperationEmbed, llmkit.OperationModerate},
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityEmbedding, llmkit.CapabilityTools, llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning, llmkit.CapabilityModeration},
		AuthSchemes:  []llmkit.AuthScheme{llmkit.AuthBearer}, Profile: "openai-native"}
}

func (p *Provider) Capabilities(_ context.Context, target llmkit.Target) (llmkit.Capabilities, error) {
	if err := p.validateTarget(target); err != nil {
		return llmkit.Capabilities{}, err
	}
	return llmkit.Capabilities{
		Provider: p.id,
		Models: map[llmkit.ModelID]llmkit.ModelCapabilities{
			target.Model: {
				Capabilities: []llmkit.Capability{
					llmkit.CapabilityGenerate,
					llmkit.CapabilityStreaming,
					llmkit.CapabilityEmbedding,
					llmkit.CapabilityTools,
					llmkit.CapabilityStructured,
					llmkit.CapabilityVision,
					llmkit.CapabilityReasoning,
					llmkit.CapabilityModeration,
				},
			},
		},
	}, nil
}

func (p *Provider) ValidateCredential(ctx context.Context, call llmkit.CredentialCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	endpoint, err := p.urlFor(call.Target, "/v1/models")
	if err != nil {
		return invalidRequest(call.Target, "provider model endpoint is invalid")
	}
	request, err := transport.NewJSONRequest(
		ctx, call.Target, call.Credential, http.MethodGet,
		endpoint, nil, nil,
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
	rawEndpoint, err := p.urlFor(call.Target, "/v1/models")
	if err != nil {
		return llmkit.ModelPage{}, invalidRequest(call.Target, "provider model endpoint is invalid")
	}
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil {
		return llmkit.ModelPage{}, invalidRequest(call.Target, "provider model endpoint is invalid")
	}
	query := endpoint.Query()
	if call.Cursor != "" {
		query.Set("after", call.Cursor)
	}
	if call.Limit > 0 {
		query.Set("limit", fmt.Sprint(call.Limit))
	}
	endpoint.RawQuery = query.Encode()
	request, err := transport.NewJSONRequest(
		ctx, call.Target, call.Credential, http.MethodGet, endpoint.String(), nil, nil,
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
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
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
			page.Models = append(page.Models, llmkit.ModelInfo{ID: llmkit.ModelID(model.ID), Owner: model.OwnedBy})
		}
	}
	if wire.HasMore {
		page.NextCursor = wire.LastID
		if page.NextCursor == "" && len(page.Models) > 0 {
			page.NextCursor = string(page.Models[len(page.Models)-1].ID)
		}
	}
	return page, nil
}

func (p *Provider) Generate(ctx context.Context, call llmkit.GenerateCall) (llmkit.Response, error) {
	if err := p.validateCall(call); err != nil {
		return llmkit.Response{}, err
	}
	api, err := p.apiFor(call.Request)
	if err != nil {
		return llmkit.Response{}, invalidRequest(call.Target, err.Error())
	}
	switch api {
	case APIChat:
		return p.generateChat(ctx, call)
	case APIResponses:
		return p.generateResponses(ctx, call)
	default:
		return llmkit.Response{}, invalidRequest(call.Target, "unsupported OpenAI API")
	}
}

func (p *Provider) Stream(ctx context.Context, call llmkit.GenerateCall) (llmkit.EventStream, error) {
	if err := p.validateCall(call); err != nil {
		return nil, err
	}
	api, err := p.apiFor(call.Request)
	if err != nil {
		return nil, invalidRequest(call.Target, err.Error())
	}
	switch api {
	case APIChat:
		return p.streamChat(ctx, call)
	case APIResponses:
		return p.streamResponses(ctx, call)
	default:
		return nil, invalidRequest(call.Target, "unsupported OpenAI API")
	}
}

func (p *Provider) Embed(ctx context.Context, call llmkit.EmbedCall) (llmkit.EmbedResponse, error) {
	if err := p.validateTarget(call.Target); err != nil {
		return llmkit.EmbedResponse{}, err
	}
	if len(call.Input) == 0 {
		return llmkit.EmbedResponse{}, invalidRequest(call.Target, "at least one embedding input is required")
	}
	payload := map[string]any{
		"model": string(call.Target.Model),
		"input": call.Input,
	}
	if call.Dimensions != nil {
		payload["dimensions"] = *call.Dimensions
	}
	generateCall := llmkit.GenerateCall{
		OperationID: call.OperationID,
		Target:      call.Target,
		Credential:  call.Credential,
		Metadata:    call.Metadata,
	}
	var wire struct {
		ID   string `json:"id"`
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage *usageDTO `json:"usage"`
	}
	if _, err := p.doJSON(ctx, generateCall, "/v1/embeddings", payload, &wire); err != nil {
		return llmkit.EmbedResponse{}, err
	}
	vectors := make([][]float32, len(wire.Data))
	for _, item := range wire.Data {
		if item.Index < 0 || item.Index >= len(vectors) {
			return llmkit.EmbedResponse{}, malformed(call.Target, wire.ID, "provider returned an invalid embedding index")
		}
		vectors[item.Index] = append([]float32(nil), item.Embedding...)
	}
	for _, vector := range vectors {
		if len(vector) == 0 {
			return llmkit.EmbedResponse{}, malformed(call.Target, wire.ID, "provider omitted an embedding vector")
		}
	}
	return llmkit.EmbedResponse{
		ProviderRequestID: wire.ID,
		Vectors:           vectors,
		Usage:             normalizeUsage(wire.Usage),
	}, nil
}

func (p *Provider) validateCall(call llmkit.GenerateCall) error {
	if err := p.validateTarget(call.Target); err != nil {
		return err
	}
	if len(call.Request.Messages) == 0 {
		return invalidRequest(call.Target, "at least one message is required")
	}
	for name := range call.Request.ProviderOptions {
		if name != OptionAPI {
			return invalidRequest(call.Target, fmt.Sprintf("unsupported provider option %q", name))
		}
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

func (p *Provider) urlFor(target llmkit.Target, logicalPath string) (string, error) {
	if p.resolveURL != nil {
		return p.resolveURL(target, logicalPath)
	}
	return p.endpointFor(target) + p.apiPathPrefix + strings.TrimPrefix(logicalPath, "/v1"), nil
}

func (p *Provider) apiFor(request llmkit.GenerateRequest) (API, error) {
	raw, ok := request.ProviderOptions[OptionAPI]
	if !ok {
		return p.defaultAPI, nil
	}
	value := strings.TrimSpace(string(raw))
	var decoded string
	if json.Unmarshal(raw, &decoded) == nil {
		value = decoded
	}
	api := API(value)
	if api != APIChat && api != APIResponses {
		return "", fmt.Errorf("%s must be %q or %q", OptionAPI, APIChat, APIResponses)
	}
	return api, nil
}

func (p *Provider) doJSON(
	ctx context.Context,
	call llmkit.GenerateCall,
	path string,
	payload any,
	destination any,
) (*http.Response, error) {
	endpoint, err := p.urlFor(call.Target, path)
	if err != nil {
		return nil, invalidRequest(call.Target, "provider endpoint is invalid")
	}
	request, err := transport.NewJSONRequest(
		ctx,
		call.Target,
		call.Credential,
		http.MethodPost,
		endpoint,
		payload,
		nil,
	)
	if err != nil {
		return nil, err
	}
	if call.OperationID != "" {
		request.Header.Set("X-Client-Request-ID", call.OperationID)
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return nil, p.classifyError(call.Target, err)
	}
	if destination != nil {
		if err := p.transport.DecodeJSON(call.Target, response, destination); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return response, nil
}

func (p *Provider) classifyError(target llmkit.Target, err error) error {
	var httpErr *transport.HTTPError
	if !errors.As(err, &httpErr) {
		return err
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
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
	case http.StatusRequestTimeout:
		kind = llmkit.ErrorTimeout
		retryable = true
	case http.StatusTooManyRequests:
		kind = llmkit.ErrorRateLimit
		retryable = true
	default:
		switch {
		case httpErr.StatusCode() >= 500:
			kind = llmkit.ErrorOverloaded
			retryable = true
		case httpErr.StatusCode() >= 400:
			kind = llmkit.ErrorInvalidRequest
		}
	}
	switch envelope.Error.Code {
	case "insufficient_quota":
		kind = llmkit.ErrorQuotaExhausted
		retryable = false
	case "context_length_exceeded":
		kind = llmkit.ErrorContextLength
		retryable = false
	case "model_not_found":
		kind = llmkit.ErrorModelNotFound
		retryable = false
	}
	return &llmkit.ProviderError{
		Provider:     target.Provider,
		Model:        target.Model,
		Kind:         kind,
		StatusCode:   httpErr.StatusCode(),
		Retryable:    retryable,
		RetryAfter:   httpErr.RetryAfter(),
		SafeMessage:  safeMessage(kind),
		ProviderCode: envelope.Error.Code,
		RequestID:    httpErr.RequestID(),
	}
}

func invalidRequest(target llmkit.Target, message string) error {
	return &llmkit.ProviderError{
		Provider:    target.Provider,
		Model:       target.Model,
		Kind:        llmkit.ErrorInvalidRequest,
		SafeMessage: message,
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
	case llmkit.ErrorQuotaExhausted:
		return "provider quota exhausted"
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
var _ llmkit.Embedder = (*Provider)(nil)
var _ llmkit.CredentialValidator = (*Provider)(nil)
var _ llmkit.ModelLister = (*Provider)(nil)
