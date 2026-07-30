package routing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/uuos-ai/llmkit/identity"
)

// HTTPSource fetches the business catalog on every client-triggered refresh.
// Authentication remains client-scoped; the business service must validate the
// trusted identity headers at its private boundary (typically mTLS/service mesh).
type HTTPSource struct {
	URL    string
	Client *http.Client
}

func (s HTTPSource) Options(ctx context.Context, principal identity.Principal) (OptionsResponse, error) {
	parsed, err := url.Parse(s.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return OptionsResponse{}, errors.New("routing: business catalog URL must be HTTPS without userinfo")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return OptionsResponse{}, errors.New("routing: business catalog request is invalid")
	}
	request.Header.Set("X-LLMKit-Tenant-ID", principal.TenantID)
	request.Header.Set("X-LLMKit-Client-ID", principal.ClientID)
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return OptionsResponse{}, errors.New("routing: business catalog is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return OptionsResponse{}, errors.New("routing: business catalog rejected the request")
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	decoder.DisallowUnknownFields()
	var options OptionsResponse
	if err := decoder.Decode(&options); err != nil {
		return OptionsResponse{}, errors.New("routing: business catalog response is malformed")
	}
	return options, nil
}
