package openai

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/transport"
)

func (p *Provider) Moderate(ctx context.Context, call llmkit.ModerateCall) (llmkit.ModerateResponse, error) {
	if err := p.validateTarget(call.Target); err != nil {
		return llmkit.ModerateResponse{}, err
	}
	var text strings.Builder
	for _, part := range call.Content {
		if part.Type != llmkit.ContentText {
			return llmkit.ModerateResponse{}, invalidRequest(call.Target, "OpenAI moderation currently accepts text content only")
		}
		text.WriteString(part.Text)
	}
	if text.Len() == 0 {
		return llmkit.ModerateResponse{}, invalidRequest(call.Target, "moderation content is required")
	}
	endpoint, err := p.urlFor(call.Target, "/v1/moderations")
	if err != nil {
		return llmkit.ModerateResponse{}, invalidRequest(call.Target, "provider endpoint is invalid")
	}
	request, err := transport.NewJSONRequest(ctx, call.Target, call.Credential, http.MethodPost, endpoint, map[string]any{"model": call.Target.Model, "input": text.String()}, nil)
	if err != nil {
		return llmkit.ModerateResponse{}, err
	}
	response, err := p.transport.Do(call.Target, request)
	if err != nil {
		return llmkit.ModerateResponse{}, p.classifyError(call.Target, err)
	}
	var wire struct {
		ID      string `json:"id"`
		Results []struct {
			Flagged        bool               `json:"flagged"`
			Categories     map[string]bool    `json:"categories"`
			CategoryScores map[string]float64 `json:"category_scores"`
		} `json:"results"`
	}
	if err := p.transport.DecodeJSON(call.Target, response, &wire); err != nil {
		return llmkit.ModerateResponse{}, err
	}
	if len(wire.Results) != 1 {
		return llmkit.ModerateResponse{}, malformed(call.Target, wire.ID, "OpenAI moderation response contained an unexpected result count")
	}
	result := wire.Results[0]
	names := make([]string, 0, len(result.Categories))
	for name := range result.Categories {
		names = append(names, name)
	}
	sort.Strings(names)
	categories := make([]llmkit.ModerationCategory, 0, len(names))
	for _, name := range names {
		var score *float64
		if value, ok := result.CategoryScores[name]; ok {
			copy := value
			score = &copy
		}
		categories = append(categories, llmkit.ModerationCategory{Name: name, Flagged: result.Categories[name], Score: score})
	}
	return llmkit.ModerateResponse{ProviderRequestID: wire.ID, Flagged: result.Flagged, Categories: categories, Usage: llmkit.Usage{Source: llmkit.UsageUnavailable}}, nil
}

var _ llmkit.Moderator = (*Provider)(nil)
