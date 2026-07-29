package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uuos-ai/llmkit"
)

type credentialFunc func(context.Context, llmkit.Target, *http.Request) error

func (f credentialFunc) Apply(ctx context.Context, target llmkit.Target, request *http.Request) error {
	return f(ctx, target, request)
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) { return f(request) }

func TestNewJSONRequestAppliesRequestCredential(t *testing.T) {
	target := llmkit.Target{Provider: "openai", Model: "model"}
	credential := credentialFunc(func(_ context.Context, got llmkit.Target, request *http.Request) error {
		if got != target {
			t.Fatalf("target = %#v, want %#v", got, target)
		}
		request.Header.Set("Authorization", "Bearer secret")
		return nil
	})
	request, err := NewJSONRequest(context.Background(), target, credential, http.MethodPost, "https://example.test", map[string]string{"a": "b"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if request.Header.Get("Authorization") != "Bearer secret" {
		t.Fatal("credential was not applied")
	}
}

func TestHTTPErrorDoesNotExposeBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":{"message":"secret prompt","code":"bad_key"}}`))
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Config{}).Do(llmkit.Target{Provider: "example", Model: "model"}, request)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %T, want *HTTPError", err)
	}
	if strings.Contains(err.Error(), "secret prompt") {
		t.Fatalf("error leaked body: %q", err)
	}
	var envelope map[string]any
	if err := httpErr.DecodeJSON(&envelope); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPErrorBodyIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"message":"this body is intentionally long"}`))
	}))
	defer server.Close()

	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	_, err := New(Config{MaxErrorBody: 8}).Do(llmkit.Target{}, request)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) || !httpErr.Truncated() {
		t.Fatalf("error = %#v, want truncated HTTPError", err)
	}
	var value any
	if err := httpErr.DecodeJSON(&value); err == nil {
		t.Fatal("truncated JSON unexpectedly decoded")
	}
}

func TestCanceledRequestIsNotRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.test", nil)
	client := New(Config{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.Canceled
	})})
	_, err := client.Do(llmkit.Target{Provider: "example"}, request)
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %T, want *ProviderError", err)
	}
	if providerErr.Kind != llmkit.ErrorCanceled || providerErr.Retryable {
		t.Fatalf("unexpected classification: %#v", providerErr)
	}
}

func TestDeadlineFaultIsNormalizedAndRetryable(t *testing.T) {
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.test", nil)
	client := New(Config{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})})
	_, err := client.Do(llmkit.Target{Provider: "example", Model: "model"}, request)
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != llmkit.ErrorTimeout || !providerErr.Retryable {
		t.Fatalf("error = %#v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	if got := ParseRetryAfter("3", now); got != 3*time.Second {
		t.Fatalf("seconds retry-after = %v", got)
	}
	if got := ParseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); got != 5*time.Second {
		t.Fatalf("date retry-after = %v", got)
	}
}

func TestHTTPErrorRecognizesProviderRequestIDHeaders(t *testing.T) {
	for _, headerName := range []string{"X-Request-ID", "Request-ID", "X-Goog-Request-ID"} {
		t.Run(headerName, func(t *testing.T) {
			client := New(Config{HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
				header := make(http.Header)
				header.Set(headerName, "req_123")
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Header:     header,
					Body:       io.NopCloser(strings.NewReader(`{}`)),
				}, nil
			})})
			request, err := http.NewRequest(http.MethodGet, "https://example.invalid", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Do(llmkit.Target{Provider: "test", Model: "model"}, request)
			var httpErr *HTTPError
			if !errors.As(err, &httpErr) || httpErr.RequestID() != "req_123" {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestDecodeJSONRejectsMalformedResponse(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
	}
	response.Body = readCloser{Reader: strings.NewReader("{")}
	var value map[string]any
	err := New(Config{}).DecodeJSON(llmkit.Target{Provider: "example"}, response, &value)
	var providerErr *llmkit.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Kind != llmkit.ErrorMalformedResponse {
		t.Fatalf("error = %#v", err)
	}
}

type readCloser struct {
	*strings.Reader
}

func (readCloser) Close() error { return nil }
