// Package httpbackend adapts external HTTPS business services to gateway store
// ports. The configured client certificate is used for service authentication.
package httpbackend

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
	"github.com/uuos-ai/llmkit/routing"
)

type Backend struct {
	configURL string
	secretURL string
	auditURL  string
	client    *http.Client
}

func New(configURL, secretURL, auditURL, certificateFile, privateKeyFile string) (*Backend, error) {
	for _, raw := range []string{configURL, secretURL, auditURL} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return nil, errors.New("managed http backend: store references must be HTTPS URLs without userinfo")
		}
	}
	certificate, err := tls.LoadX509KeyPair(certificateFile, privateKeyFile)
	if err != nil {
		return nil, errors.New("managed http backend: client certificate could not be loaded")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	return &Backend{
		configURL: strings.TrimRight(configURL, "/"), secretURL: strings.TrimRight(secretURL, "/"), auditURL: strings.TrimRight(auditURL, "/"),
		client: &http.Client{Timeout: 15 * time.Second, Transport: transport},
	}, nil
}

func (b *Backend) ProviderOptions(ctx context.Context, principal identity.Principal) (routing.OptionsResponse, error) {
	var response routing.OptionsResponse
	err := b.call(ctx, principal, http.MethodGet, b.configURL+"/v1/provider-options", nil, &response)
	return response, err
}

func (b *Backend) ResolveTarget(ctx context.Context, principal identity.Principal, targetID string) (managed.Target, error) {
	path := "_default"
	if targetID != "" {
		path = url.PathEscape(targetID)
	}
	var target managed.Target
	err := b.call(ctx, principal, http.MethodGet, b.configURL+"/v1/targets/"+path, nil, &target)
	return target, err
}

type credentialResponse struct {
	Type   string `json:"type"`
	Header string `json:"header,omitempty"`
	Value  []byte `json:"value"`
}

func (b *Backend) OpenCredential(ctx context.Context, principal identity.Principal, reference string) (llmkit.CredentialHandle, func(), error) {
	var credential credentialResponse
	err := b.call(ctx, principal, http.MethodPost, b.secretURL+"/v1/credentials:open", map[string]string{"credential_ref": reference}, &credential)
	if err != nil {
		return nil, func() {}, err
	}
	handle, err := newCredentialHandle(credential)
	clear(credential.Value)
	if err != nil {
		return nil, func() {}, err
	}
	return handle, handle.clear, nil
}

func (b *Backend) Append(ctx context.Context, event managed.AuditEvent) error {
	return b.call(ctx, identity.Principal{TenantID: event.TenantID, ClientID: event.ClientID}, http.MethodPost, b.auditURL+"/v1/audit-events", event, nil)
}

func (b *Backend) UpsertCustomProvider(ctx context.Context, principal identity.Principal, input managed.CustomProviderInput) (routing.OptionsResponse, error) {
	defer clear(input.Credential)
	var response routing.OptionsResponse
	err := b.call(ctx, principal, http.MethodPut, b.configURL+"/v1/custom-providers", input, &response)
	return response, err
}

func (b *Backend) DeleteCustomProvider(ctx context.Context, principal identity.Principal, providerID string) (routing.OptionsResponse, error) {
	var response routing.OptionsResponse
	err := b.call(ctx, principal, http.MethodDelete, b.configURL+"/v1/custom-providers/"+url.PathEscape(providerID), nil, &response)
	return response, err
}

func (b *Backend) call(ctx context.Context, principal identity.Principal, method, endpoint string, input, output any) error {
	var body io.Reader
	var encoded []byte
	if input != nil {
		var err error
		encoded, err = json.Marshal(input)
		if err != nil {
			return errors.New("managed http backend: request encoding failed")
		}
		defer clear(encoded)
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("managed http backend: request construction failed")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LLMKit-Tenant-ID", principal.TenantID)
	request.Header.Set("X-LLMKit-Client-ID", principal.ClientID)
	response, err := b.client.Do(request)
	if err != nil {
		return errors.New("managed http backend: store is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return errors.New("managed http backend: store rejected the operation")
	}
	if output == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return errors.New("managed http backend: store response is malformed")
	}
	return nil
}

type credentialHandle struct {
	header string
	value  []byte
}

func newCredentialHandle(input credentialResponse) (*credentialHandle, error) {
	header := input.Header
	value := append([]byte(nil), input.Value...)
	switch input.Type {
	case "bearer":
		header = "Authorization"
		prefixed := make([]byte, 0, len("Bearer ")+len(value))
		prefixed = append(prefixed, "Bearer "...)
		prefixed = append(prefixed, value...)
		clear(value)
		value = prefixed
	case "header":
		switch strings.ToLower(header) {
		case "authorization", "x-api-key", "x-goog-api-key":
		default:
			clear(value)
			return nil, errors.New("managed http backend: unsupported credential header")
		}
	default:
		clear(value)
		return nil, errors.New("managed http backend: unsupported credential type")
	}
	return &credentialHandle{header: header, value: value}, nil
}

func (h *credentialHandle) Apply(_ context.Context, _ llmkit.Target, request *http.Request) error {
	request.Header.Set(h.header, string(h.value))
	return nil
}

func (h *credentialHandle) clear() { clear(h.value) }

var _ managed.ConfigStore = (*Backend)(nil)
var _ managed.SecretStore = (*Backend)(nil)
var _ managed.AuditStore = (*Backend)(nil)
var _ managed.CustomProviderStore = (*Backend)(nil)
