// Package siliconflow implements SiliconFlow's OpenAI-compatible generation,
// embedding, and dedicated rerank protocols.
package siliconflow

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/providers/internal/openaicompat"
	"github.com/uuos-ai/llmkit/providers/openai"
	"github.com/uuos-ai/llmkit/transport"
)

const DefaultProviderID llmkit.ProviderID = "siliconflow"
const DefaultEndpoint = "https://api.siliconflow.cn/v1"

type Config struct {
	ID        llmkit.ProviderID
	Endpoint  string
	Transport *transport.Client
}

type Provider struct {
	*openaicompat.FullProvider
	id, endpoint string
	transport    *transport.Client
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
	client := config.Transport
	if client == nil {
		client = transport.New(transport.Config{})
	}
	full, err := openaicompat.NewFull(openaicompat.Config{ID: id, Endpoint: endpoint, DefaultAPI: openai.APIChat, Transport: client,
		Capabilities: []llmkit.Capability{llmkit.CapabilityGenerate, llmkit.CapabilityStreaming, llmkit.CapabilityEmbedding, llmkit.CapabilityTools, llmkit.CapabilityStructured, llmkit.CapabilityVision, llmkit.CapabilityReasoning, llmkit.CapabilityRerank}})
	if err != nil {
		return nil, err
	}
	return &Provider{FullProvider: full, id: string(id), endpoint: endpoint, transport: client}, nil
}

func (p *Provider) Manifest() llmkit.AdapterManifest {
	manifest := p.FullProvider.Manifest()
	manifest.Operations = append(manifest.Operations, llmkit.OperationRerank)
	manifest.Profile = "siliconflow"
	return manifest
}

func (p *Provider) Rerank(ctx context.Context, call llmkit.RerankCall) (llmkit.RerankResponse, error) {
	if string(call.Target.Provider) != p.id || call.Target.Model == "" || call.Query == "" || len(call.Documents) == 0 || (call.TopN != nil && (*call.TopN <= 0 || *call.TopN > len(call.Documents))) {
		return llmkit.RerankResponse{}, providerError(call.Target, llmkit.ErrorInvalidRequest, 0, false, "invalid SiliconFlow rerank request")
	}
	endpoint := p.endpoint
	if call.Target.Endpoint != "" {
		endpoint = strings.TrimRight(call.Target.Endpoint, "/")
	}
	payload := map[string]any{"model": call.Target.Model, "query": call.Query, "documents": call.Documents, "return_documents": true}
	if call.TopN != nil {
		payload["top_n"] = *call.TopN
	}
	request, err := transport.NewJSONRequest(ctx, call.Target, call.Credential, http.MethodPost, endpoint+"/rerank", payload, nil)
	if err != nil {
		return llmkit.RerankResponse{}, err
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return llmkit.RerankResponse{}, classify(call.Target, err)
	}
	var wire struct {
		ID      string `json:"id"`
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Document       *struct {
				Text string `json:"text"`
			} `json:"document"`
		} `json:"results"`
		Meta *struct {
			Tokens *struct {
				InputTokens int64 `json:"input_tokens"`
			} `json:"tokens"`
		} `json:"meta"`
	}
	if err := p.transport.DecodeJSON(call.Target, response, &wire); err != nil {
		return llmkit.RerankResponse{}, err
	}
	if len(wire.Results) == 0 {
		return llmkit.RerankResponse{}, providerError(call.Target, llmkit.ErrorProtocol, http.StatusOK, false, "SiliconFlow rerank response contained no results")
	}
	results := make([]llmkit.RerankResult, len(wire.Results))
	for index, result := range wire.Results {
		if result.Index < 0 || result.Index >= len(call.Documents) {
			return llmkit.RerankResponse{}, providerError(call.Target, llmkit.ErrorProtocol, http.StatusOK, false, "SiliconFlow rerank response contained an invalid document index")
		}
		document := ""
		if result.Document != nil {
			document = result.Document.Text
		}
		results[index] = llmkit.RerankResult{Index: result.Index, Score: result.RelevanceScore, Document: document}
	}
	usage := llmkit.Usage{Source: llmkit.UsageUnavailable}
	if wire.Meta != nil && wire.Meta.Tokens != nil {
		usage = llmkit.Usage{Source: llmkit.UsageReported, InputTokens: wire.Meta.Tokens.InputTokens, TotalTokens: wire.Meta.Tokens.InputTokens}
	}
	return llmkit.RerankResponse{ProviderRequestID: wire.ID, Results: results, Usage: usage}, nil
}

func classify(target llmkit.Target, err error) error {
	var httpError *transport.HTTPError
	if !errors.As(err, &httpError) {
		return err
	}
	kind, retryable := llmkit.ErrorProvider, false
	switch httpError.StatusCode() {
	case http.StatusUnauthorized:
		kind = llmkit.ErrorAuthentication
	case http.StatusForbidden:
		kind = llmkit.ErrorPermission
	case http.StatusTooManyRequests:
		kind, retryable = llmkit.ErrorRateLimited, true
	default:
		if httpError.StatusCode() >= 500 {
			kind, retryable = llmkit.ErrorProviderUnavailable, true
		}
	}
	return &llmkit.ProviderError{Provider: target.Provider, Model: target.Model, Kind: kind, StatusCode: httpError.StatusCode(), Retryable: retryable, RetryAfter: httpError.RetryAfter(), RequestID: httpError.RequestID(), SafeMessage: "SiliconFlow rejected the request"}
}

func providerError(target llmkit.Target, kind llmkit.ErrorKind, status int, retryable bool, message string) error {
	return &llmkit.ProviderError{Provider: target.Provider, Model: target.Model, Kind: kind, StatusCode: status, Retryable: retryable, SafeMessage: message}
}

var _ llmkit.Reranker = (*Provider)(nil)
var _ llmkit.ManifestProvider = (*Provider)(nil)
