package endpointpolicy

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"github.com/uuos-ai/llmkit"
)

type sequenceResolver struct {
	mu      sync.Mutex
	answers [][]net.IPAddr
}

func (r *sequenceResolver) LookupIPAddr(context.Context, string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	answer := r.answers[0]
	r.answers = r.answers[1:]
	return answer, nil
}

func TestPublicHTTPSRejectsUnsafeEndpoints(t *testing.T) {
	policy := PublicHTTPS(context.Background(), net.DefaultResolver)
	for _, endpoint := range []string{"http://example.com/v1", "https://localhost/v1", "https://127.0.0.1/v1", "https://user:pass@example.com/v1", "https://example.com:8443/v1", "https://example.com/a/%2e%2e/b"} {
		if err := policy(llmkit.Target{Provider: "openai", Model: "m", Endpoint: endpoint}); err == nil {
			t.Fatalf("expected %q to be rejected", endpoint)
		}
	}
}

func TestDialRevalidatesDNSAndBlocksRebinding(t *testing.T) {
	resolver := &sequenceResolver{answers: [][]net.IPAddr{
		{{IP: net.ParseIP("8.8.8.8")}},
		{{IP: net.ParseIP("127.0.0.1")}},
	}}
	policy := PublicHTTPS(context.Background(), resolver)
	if err := policy(llmkit.Target{Provider: "custom", Model: "m", Endpoint: "https://rebind.example/v1"}); err != nil {
		t.Fatal(err)
	}
	transport := PublicHTTPClient(resolver).Transport.(*http.Transport)
	if _, err := transport.DialContext(context.Background(), "tcp", "rebind.example:443"); err == nil {
		t.Fatal("dial accepted a rebound loopback address")
	}
}

func TestRedirectsCannotCrossOrigin(t *testing.T) {
	previous := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.example"}}
	same := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.example"}}
	if err := NoCrossHostRedirect(same, []*http.Request{previous}); err != nil {
		t.Fatal(err)
	}
	cross := &http.Request{URL: &url.URL{Scheme: "https", Host: "evil.example"}}
	if err := NoCrossHostRedirect(cross, []*http.Request{previous}); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
}
