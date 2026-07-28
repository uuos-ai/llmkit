package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/uuos-ai/llmkit"
)

const (
	defaultMaxErrorBody    = 64 << 10
	defaultMaxResponseBody = 16 << 20
)

type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

type Config struct {
	HTTPClient      Doer
	MaxErrorBody    int64
	MaxResponseBody int64
}

type Client struct {
	httpClient      Doer
	maxErrorBody    int64
	maxResponseBody int64
}

func New(config Config) *Client {
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	maxErrorBody := config.MaxErrorBody
	if maxErrorBody <= 0 {
		maxErrorBody = defaultMaxErrorBody
	}
	maxResponseBody := config.MaxResponseBody
	if maxResponseBody <= 0 {
		maxResponseBody = defaultMaxResponseBody
	}
	return &Client{
		httpClient:      httpClient,
		maxErrorBody:    maxErrorBody,
		maxResponseBody: maxResponseBody,
	}
}

// NewJSONRequest builds a request for exactly one target and applies its
// request-scoped credential after all non-secret headers have been set.
func NewJSONRequest(
	ctx context.Context,
	target llmkit.Target,
	credential llmkit.CredentialHandle,
	method string,
	url string,
	payload any,
	headers http.Header,
) (*http.Request, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, &llmkit.ProviderError{
				Provider:    target.Provider,
				Model:       target.Model,
				Kind:        llmkit.ErrorInvalidRequest,
				Phase:       llmkit.PhaseWriteRequest,
				SafeMessage: "failed to encode provider request",
				Cause:       errors.New("request encoding failed"),
			}
		}
		body = bytes.NewReader(data)
	}

	request, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, &llmkit.ProviderError{
			Provider:    target.Provider,
			Model:       target.Model,
			Kind:        llmkit.ErrorInvalidRequest,
			Phase:       llmkit.PhaseWriteRequest,
			SafeMessage: "failed to create provider request",
			Cause:       errors.New("request creation failed"),
		}
	}
	request.Header.Set("Content-Type", "application/json")
	for key, values := range headers {
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
	if credential == nil {
		return nil, &llmkit.ProviderError{
			Provider:    target.Provider,
			Model:       target.Model,
			Kind:        llmkit.ErrorAuthentication,
			Phase:       llmkit.PhaseWriteRequest,
			SafeMessage: "credential is required",
		}
	}
	if err := credential.Apply(ctx, target, request); err != nil {
		return nil, &llmkit.ProviderError{
			Provider:    target.Provider,
			Model:       target.Model,
			Kind:        llmkit.ErrorAuthentication,
			Phase:       llmkit.PhaseWriteRequest,
			SafeMessage: "credential could not be applied",
			Cause:       errors.New("credential application failed"),
		}
	}
	return request, nil
}

// Do returns successful responses without consuming their bodies. Non-2xx
// bodies are bounded and retained privately in HTTPError for safe decoding.
func (c *Client) Do(target llmkit.Target, request *http.Request) (*http.Response, error) {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, classifyTransportError(target, request.Context(), err)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxErrorBody+1))
	if readErr != nil {
		return nil, &llmkit.ProviderError{
			Provider:    target.Provider,
			Model:       target.Model,
			Kind:        llmkit.ErrorTransport,
			Phase:       llmkit.PhaseReadBody,
			StatusCode:  response.StatusCode,
			SafeMessage: "failed to read provider error response",
			Cause:       errors.New("error response read failed"),
		}
	}
	truncated := int64(len(body)) > c.maxErrorBody
	if truncated {
		body = body[:c.maxErrorBody]
	}
	return nil, &HTTPError{
		statusCode: response.StatusCode,
		retryAfter: ParseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
		requestID:  requestID(response.Header),
		body:       body,
		truncated:  truncated,
	}
}

func (c *Client) DecodeJSON(target llmkit.Target, response *http.Response, destination any) error {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBody+1))
	if err != nil || int64(len(body)) > c.maxResponseBody {
		return &llmkit.ProviderError{
			Provider:    target.Provider,
			Model:       target.Model,
			Kind:        llmkit.ErrorMalformedResponse,
			Phase:       llmkit.PhaseReadBody,
			StatusCode:  response.StatusCode,
			SafeMessage: "provider response exceeded the safe body limit",
			RequestID:   requestID(response.Header),
			Cause:       errors.New("response body limit exceeded"),
		}
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return &llmkit.ProviderError{
			Provider:    target.Provider,
			Model:       target.Model,
			Kind:        llmkit.ErrorMalformedResponse,
			Phase:       llmkit.PhaseDecode,
			StatusCode:  response.StatusCode,
			SafeMessage: "provider returned malformed JSON",
			RequestID:   requestID(response.Header),
			Cause:       errors.New("response decoding failed"),
		}
	}
	return nil
}

func requestID(header http.Header) string {
	for _, name := range []string{"X-Request-ID", "Request-ID", "X-Goog-Request-ID"} {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}

type HTTPError struct {
	statusCode int
	retryAfter time.Duration
	requestID  string
	body       []byte
	truncated  bool
}

func (e *HTTPError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("provider returned HTTP status %d", e.statusCode)
}

func (e *HTTPError) StatusCode() int           { return e.statusCode }
func (e *HTTPError) RetryAfter() time.Duration { return e.retryAfter }
func (e *HTTPError) RequestID() string         { return e.requestID }
func (e *HTTPError) Truncated() bool           { return e.truncated }

func (e *HTTPError) DecodeJSON(destination any) error {
	if e == nil || len(e.body) == 0 {
		return io.EOF
	}
	return json.Unmarshal(e.body, destination)
}

func ParseRetryAfter(value string, now time.Time) time.Duration {
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func classifyTransportError(target llmkit.Target, ctx context.Context, err error) error {
	kind := llmkit.ErrorTransport
	phase := llmkit.PhaseConnect
	retryable := true
	message := "provider transport failed"

	switch {
	case errors.Is(ctx.Err(), context.Canceled), errors.Is(err, context.Canceled):
		kind = llmkit.ErrorCanceled
		retryable = false
		message = "provider request canceled"
	case errors.Is(ctx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		kind = llmkit.ErrorTimeout
		retryable = true
		message = "provider request timed out"
	default:
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			kind = llmkit.ErrorTimeout
			message = "provider request timed out"
		}
	}

	return &llmkit.ProviderError{
		Provider:    target.Provider,
		Model:       target.Model,
		Kind:        kind,
		Phase:       phase,
		Retryable:   retryable,
		SafeMessage: message,
		Cause:       errors.New("transport operation failed"),
	}
}
