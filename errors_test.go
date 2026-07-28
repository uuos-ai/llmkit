package llmkit

import (
	"strings"
	"testing"
)

func TestProviderErrorExposesOnlySafeFields(t *testing.T) {
	err := &ProviderError{
		Provider:    "example",
		Model:       "model",
		Kind:        ErrorAuthentication,
		SafeMessage: "authentication failed",
	}
	message := err.Error()
	if !strings.Contains(message, "authentication failed") {
		t.Fatalf("safe message missing from %q", message)
	}
	if strings.Contains(message, "api-key") {
		t.Fatalf("unexpected credential material in %q", message)
	}
}

func TestNilProviderError(t *testing.T) {
	var err *ProviderError
	if err.Error() != "<nil>" {
		t.Fatalf("unexpected nil error text: %q", err.Error())
	}
	if err.Unwrap() != nil {
		t.Fatal("nil provider error must not unwrap")
	}
}
