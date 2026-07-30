// Package identity authenticates llmkitd clients. A client identifies one
// embedding business; UserID is that client's currently bound user and has no
// meaning outside the ClientID namespace.
package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
)

type Principal struct {
	ClientID         string              `json:"client_id"`
	ClientInstanceID string              `json:"client_instance_id,omitempty"`
	UserID           string              `json:"user_id,omitempty"`
	BindingVersion   uint64              `json:"binding_version,omitempty"`
	Scopes           map[string]struct{} `json:"scopes,omitempty"`
}

const (
	ScopeTargetsRead      = "targets:read"
	ScopeInferenceExecute = "inference:execute"
	ScopeProvidersWrite   = "providers:write"
	ScopeCredentialsWrite = "credentials:write"
	ScopeUsersBind        = "users:bind"
	ScopeBlobsWrite       = "blobs:write"
	ScopeTokensRefresh    = "tokens:refresh"
	ScopeAuditRead        = "audit:read"
	ScopeAdminClients     = "admin:clients"
	ScopeAdminRuntime     = "admin:runtime"
)

func (p Principal) Key() string { return p.ClientID + "\x00" + p.UserID }

func (p Principal) ClientKey() string { return p.ClientID }

func (p Principal) SessionKey() string {
	return p.ClientID + "\x00" + p.ClientInstanceID
}

func (p Principal) HasScope(scope string) bool {
	_, ok := p.Scopes[scope]
	return ok
}

type Authenticator interface {
	Authenticate(context.Context, []byte) (Principal, bool)
}

type AuthenticatorFunc func(context.Context, []byte) (Principal, bool)

func (f AuthenticatorFunc) Authenticate(ctx context.Context, token []byte) (Principal, bool) {
	return f(ctx, token)
}

func SingleToken(token []byte, principal Principal) (Authenticator, error) {
	if len(token) < 32 || principal.ClientID == "" {
		return nil, errors.New("identity: token must contain at least 32 bytes and client ID is required")
	}
	want := append([]byte(nil), token...)
	return &singleToken{want: want, principal: clonePrincipal(principal)}, nil
}

type singleToken struct {
	mu        sync.RWMutex
	want      []byte
	principal Principal
}

func (s *singleToken) Authenticate(_ context.Context, candidate []byte) (Principal, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ok := len(s.want) != 0 && len(candidate) == len(s.want) && subtle.ConstantTimeCompare(candidate, s.want) == 1
	return clonePrincipal(s.principal), ok
}

// Destroy clears the launch-scoped token copy.
func (s *singleToken) Destroy() {
	s.mu.Lock()
	clear(s.want)
	s.want = nil
	s.mu.Unlock()
}

type TokenRecord struct {
	ClientID         string   `json:"client_id"`
	ClientInstanceID string   `json:"client_instance_id,omitempty"`
	UserID           string   `json:"user_id,omitempty"`
	BindingVersion   uint64   `json:"binding_version,omitempty"`
	TokenSHA256      string   `json:"token_sha256"`
	Scopes           []string `json:"scopes,omitempty"`
}

type StaticTokens struct {
	records map[[sha256.Size]byte]Principal
}

func NewStaticTokens(records []TokenRecord) (*StaticTokens, error) {
	store := &StaticTokens{records: make(map[[sha256.Size]byte]Principal, len(records))}
	for _, record := range records {
		if record.ClientID == "" {
			return nil, errors.New("identity: client_id is required")
		}
		decoded, err := hex.DecodeString(record.TokenSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("identity: token_sha256 must be a SHA-256 hex digest")
		}
		var digest [sha256.Size]byte
		copy(digest[:], decoded)
		if _, duplicate := store.records[digest]; duplicate {
			return nil, errors.New("identity: duplicate token hash")
		}
		scopes := make(map[string]struct{}, len(record.Scopes))
		for _, scope := range record.Scopes {
			if scope = strings.TrimSpace(scope); scope != "" {
				scopes[scope] = struct{}{}
			}
		}
		store.records[digest] = Principal{
			ClientID: record.ClientID, ClientInstanceID: record.ClientInstanceID,
			UserID: record.UserID, BindingVersion: record.BindingVersion, Scopes: scopes,
		}
	}
	if len(store.records) == 0 {
		return nil, errors.New("identity: at least one client token is required")
	}
	return store, nil
}

func LoadTokenHashFile(path string) (*StaticTokens, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("identity: inspect token hash file: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("identity: token hash file permissions must not grant group or other access")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("identity: open token hash file: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var records []TokenRecord
	if err := decoder.Decode(&records); err != nil {
		return nil, errors.New("identity: token hash file is malformed")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("identity: token hash file must contain exactly one JSON value")
	}
	return NewStaticTokens(records)
}

func (s *StaticTokens) Authenticate(_ context.Context, token []byte) (Principal, bool) {
	digest := sha256.Sum256(token)
	principal, ok := s.records[digest]
	return clonePrincipal(principal), ok
}

func HashToken(token []byte) string {
	digest := sha256.Sum256(token)
	return hex.EncodeToString(digest[:])
}

type contextKey struct{}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, clonePrincipal(principal))
}

func FromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return clonePrincipal(principal), ok
}

func clonePrincipal(source Principal) Principal {
	result := source
	result.Scopes = make(map[string]struct{}, len(source.Scopes))
	for scope := range source.Scopes {
		result.Scopes[scope] = struct{}{}
	}
	return result
}
