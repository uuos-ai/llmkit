package endpointpolicy

import (
	"context"
	"net"
	"testing"

	"github.com/uuos-ai/llmkit"
)

func TestPublicHTTPSRejectsUnsafeEndpoints(t *testing.T) {
	policy := PublicHTTPS(context.Background(), net.DefaultResolver)
	for _, endpoint := range []string{"http://example.com/v1", "https://localhost/v1", "https://127.0.0.1/v1", "https://user:pass@example.com/v1", "https://example.com:8443/v1", "https://example.com/a/%2e%2e/b"} {
		if err := policy(llmkit.Target{Provider: "openai", Model: "m", Endpoint: endpoint}); err == nil {
			t.Fatalf("expected %q to be rejected", endpoint)
		}
	}
}
