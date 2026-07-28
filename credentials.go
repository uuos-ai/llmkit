package llmkit

import (
	"context"
	"net/http"
)

// CredentialHandle applies authentication for this call without exposing the
// underlying secret to llmkit. Implementations may resolve short-lived tokens
// or consult a host-owned secret store on every request.
type CredentialHandle interface {
	Apply(ctx context.Context, target Target, request *http.Request) error
}
