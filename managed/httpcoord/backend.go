// Package httpcoord adapts strongly consistent gateway coordination services.
package httpcoord

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
)

type Backend struct {
	base         string
	client       *http.Client
	serviceToken managed.ServiceTokenSource
}

func New(base, certificateFile, privateKeyFile string, sources ...managed.ServiceTokenSource) (*Backend, error) {
	certificate, err := tls.LoadX509KeyPair(certificateFile, privateKeyFile)
	if err != nil {
		return nil, errors.New("httpcoord: client certificate could not be loaded")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	return NewWithClient(base, &http.Client{Transport: transport}, sources...)
}

func NewWithClient(base string, client *http.Client, sources ...managed.ServiceTokenSource) (*Backend, error) {
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || client == nil {
		return nil, errors.New("httpcoord: HTTPS base URL without userinfo and HTTP client are required")
	}
	var source managed.ServiceTokenSource
	if len(sources) > 1 {
		return nil, errors.New("httpcoord: at most one service token source is allowed")
	}
	if len(sources) == 1 {
		source = sources[0]
	}
	return &Backend{base: strings.TrimRight(base, "/"), client: client, serviceToken: source}, nil
}

func (b *Backend) GetSession(ctx context.Context, id string) (managed.Session, bool, error) {
	var value managed.Session
	status, err := b.call(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(id), nil, &value)
	if status == http.StatusNotFound {
		return managed.Session{}, false, nil
	}
	return value, err == nil, err
}
func (b *Backend) ReplaceSession(ctx context.Context, session managed.Session, expected uint64) (managed.Session, error) {
	var value managed.Session
	_, err := b.call(ctx, http.MethodPut, "/v1/sessions/"+url.PathEscape(session.SessionID)+"?expected_fencing="+strconv.FormatUint(expected, 10), session, &value)
	return value, err
}
func (b *Backend) RevokeSession(ctx context.Context, id string, fencing uint64, reason string) error {
	_, err := b.call(ctx, http.MethodPost, "/v1/sessions/"+url.PathEscape(id)+":revoke", map[string]any{"fencing_token": fencing, "reason": reason}, nil)
	return err
}

func (b *Backend) Current(ctx context.Context, clientID string) (identity.UserBinding, bool, error) {
	var value identity.UserBinding
	status, err := b.call(ctx, http.MethodGet, "/v1/user-bindings/"+url.PathEscape(clientID), nil, &value)
	if status == http.StatusNotFound {
		return identity.UserBinding{}, false, nil
	}
	return value, err == nil, err
}
func (b *Backend) Bind(ctx context.Context, request identity.BindRequest) (identity.UserBinding, identity.UserBinding, error) {
	var value struct {
		Previous identity.UserBinding `json:"previous"`
		Current  identity.UserBinding `json:"current"`
	}
	_, err := b.call(ctx, http.MethodPut, "/v1/user-bindings/"+url.PathEscape(request.ClientID), request, &value)
	return value.Previous, value.Current, err
}
func (b *Backend) Watch(ctx context.Context, clientID string, after uint64) (<-chan identity.UserBinding, error) {
	updates := make(chan identity.UserBinding, 1)
	go func() {
		defer close(updates)
		version := after
		for ctx.Err() == nil {
			var next identity.UserBinding
			_, err := b.call(ctx, http.MethodGet, "/v1/user-bindings/"+url.PathEscape(clientID)+":wait?after="+strconv.FormatUint(version, 10), nil, &next)
			if err != nil {
				return
			}
			if next.BindingVersion <= version {
				return
			}
			version = next.BindingVersion
			select {
			case updates <- next:
			case <-ctx.Done():
				return
			}
		}
	}()
	return updates, nil
}

func (b *Backend) Reserve(ctx context.Context, request managed.RateLimitRequest) (managed.RateLimitLease, error) {
	var lease managed.RateLimitLease
	_, err := b.call(ctx, http.MethodPost, "/v1/rate-limits:reserve", request, &lease)
	return lease, err
}
func (b *Backend) Commit(ctx context.Context, id string, usage managed.RateLimitUsage) error {
	_, err := b.call(ctx, http.MethodPost, "/v1/rate-limit-leases/"+url.PathEscape(id)+":commit", usage, nil)
	return err
}
func (b *Backend) Release(ctx context.Context, id string) error {
	_, err := b.call(ctx, http.MethodPost, "/v1/rate-limit-leases/"+url.PathEscape(id)+":release", nil, nil)
	return err
}

func (b *Backend) Authenticate(ctx context.Context, token []byte) (identity.Principal, bool) {
	if !strings.HasPrefix(string(token), identity.AccessPrefix(identity.TokenGateway)) {
		return identity.Principal{}, false
	}
	var principal identity.Principal
	_, err := b.call(ctx, http.MethodPost, "/v1/tokens:authenticate", map[string]string{"access_token": string(token)}, &principal)
	return principal, err == nil && principal.ClientID != ""
}

func (b *Backend) ExchangeOIDC(ctx context.Context, request managed.OIDCExchangeRequest) (identity.TokenPair, error) {
	var pair identity.TokenPair
	_, err := b.call(ctx, http.MethodPost, "/v1/oidc:exchange", request, &pair)
	return pair, err
}

func (b *Backend) RefreshToken(ctx context.Context, request managed.RefreshTokenRequest) (identity.TokenPair, error) {
	var pair identity.TokenPair
	_, err := b.call(ctx, http.MethodPost, "/v1/tokens:refresh", request, &pair)
	return pair, err
}

func (b *Backend) BindUser(ctx context.Context, request managed.BindUserRequest) (identity.TokenPair, identity.UserBinding, error) {
	var response struct {
		Tokens  identity.TokenPair   `json:"tokens"`
		Binding identity.UserBinding `json:"binding"`
	}
	_, err := b.call(ctx, http.MethodPost, "/v1/sessions:bind-user", request, &response)
	return response.Tokens, response.Binding, err
}

func (b *Backend) call(ctx context.Context, method, path string, input, output any) (int, error) {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return 0, errors.New("httpcoord: encode failed")
		}
		defer clear(encoded)
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, b.base+path, body)
	if err != nil {
		return 0, errors.New("httpcoord: request failed")
	}
	request.Header.Set("Content-Type", "application/json")
	if b.serviceToken != nil {
		token, err := b.serviceToken.ServiceToken(ctx)
		if err != nil {
			return 0, errors.New("httpcoord: service token unavailable")
		}
		request.Header.Set("Authorization", "Bearer "+string(token))
		clear(token)
	}
	response, err := b.client.Do(request)
	if err != nil {
		return 0, errors.New("httpcoord: service unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode, fmt.Errorf("httpcoord: service rejected operation: %d", response.StatusCode)
	}
	if output != nil {
		decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(output); err != nil {
			return response.StatusCode, errors.New("httpcoord: malformed response")
		}
	}
	return response.StatusCode, nil
}

var _ managed.SessionStore = (*Backend)(nil)
var _ identity.UserBindingStore = (*Backend)(nil)
var _ managed.RateLimitStore = (*Backend)(nil)
var _ managed.IdentityService = (*Backend)(nil)
