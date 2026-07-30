package httpcoord

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/uuos-ai/llmkit/managed"
)

type tokenSourceFunc func(context.Context) ([]byte, error)

func (f tokenSourceFunc) ServiceToken(ctx context.Context) ([]byte, error) { return f(ctx) }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestSessionAndRateLimitContracts(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "Bearer service-token-0123456789abcdef012345" {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/v1/sessions/s":
			return testResponse(200, `{"session_id":"s","fencing_token":4,"status":"active","principal":{"client_id":"c","scopes":{}}}`), nil
		case "/v1/rate-limits:reserve":
			return testResponse(200, `{"lease_id":"l","allowed":true}`), nil
		default:
			return testResponse(204, ""), nil
		}
	})}
	backend, err := NewWithClient("https://coord.example", client, tokenSourceFunc(func(context.Context) ([]byte, error) {
		return []byte("service-token-0123456789abcdef012345"), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	session, ok, err := backend.GetSession(context.Background(), "s")
	if err != nil || !ok || session.FencingToken != 4 {
		t.Fatalf("session=%#v ok=%v err=%v", session, ok, err)
	}
	lease, err := backend.Reserve(context.Background(), managed.RateLimitRequest{ClientID: "c", Requests: 1})
	if err != nil || !lease.Allowed {
		t.Fatalf("lease=%#v err=%v", lease, err)
	}
}

func testResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
