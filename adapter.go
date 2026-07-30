package llmkit

import (
	"errors"
)

type Operation string

const (
	OperationGenerate Operation = "generate"
	OperationEmbed    Operation = "embed"
	OperationRerank   Operation = "rerank"
	OperationModerate Operation = "moderate"
)

type AdapterMaturity string

const (
	AdapterExperimental AdapterMaturity = "experimental"
	AdapterConformant   AdapterMaturity = "conformant"
	AdapterVerified     AdapterMaturity = "verified"
)

type AuthScheme string

const (
	AuthBearer        AuthScheme = "bearer"
	AuthAPIKeyHeader  AuthScheme = "api_key_header"
	AuthCloudWorkload AuthScheme = "cloud_workload"
	AuthNone          AuthScheme = "none"
)

// AdapterManifest is non-secret, versioned metadata. It is intersected with
// target policy and verification results; it never grants capabilities alone.
type AdapterManifest struct {
	ProviderID         ProviderID      `json:"provider_id"`
	AdapterVersion     string          `json:"adapter_version"`
	ProviderAPIVersion string          `json:"provider_api_version,omitempty"`
	Maturity           AdapterMaturity `json:"maturity"`
	Operations         []Operation     `json:"operations"`
	Capabilities       []Capability    `json:"capabilities,omitempty"`
	AuthSchemes        []AuthScheme    `json:"auth_schemes"`
	Profile            string          `json:"profile,omitempty"`
	VerifiedAt         string          `json:"verified_at,omitempty"`
}

func (m AdapterManifest) Validate() error {
	if m.ProviderID == "" || m.AdapterVersion == "" {
		return errors.New("llmkit: adapter provider ID and version are required")
	}
	if m.Maturity != AdapterExperimental && m.Maturity != AdapterConformant && m.Maturity != AdapterVerified {
		return errors.New("llmkit: invalid adapter maturity")
	}
	if len(m.Operations) == 0 || len(m.AuthSchemes) == 0 {
		return errors.New("llmkit: adapter operations and auth schemes are required")
	}
	return nil
}

type ManifestProvider interface {
	Provider
	Manifest() AdapterManifest
}
