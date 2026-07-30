// Package localstore provides the optional local-service persistence profile:
// non-secret catalog metadata in SQLite and credentials in the OS keyring.
package localstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uuos-ai/llmkit"
	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
	"github.com/uuos-ai/llmkit/routing"
	"github.com/zalando/go-keyring"
	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	service string
}

func Open(path, instanceID string) (*Store, error) {
	if path == "" || !filepath.IsAbs(path) || instanceID == "" {
		return nil, errors.New("localstore: absolute SQLite path and instance ID are required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, errors.New("localstore: SQLite directory could not be created")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, errors.New("localstore: SQLite could not be opened")
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, service: "llmkit/" + instanceID}
	if err := store.initialize(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = db.Close()
		return nil, errors.New("localstore: SQLite permissions could not be restricted")
	}
	return store, nil
}

func (s *Store) initialize() error {
	_, err := s.db.Exec(`
PRAGMA busy_timeout=5000;
PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS custom_providers (
 client_id TEXT NOT NULL,
 user_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 definition_json BLOB NOT NULL,
 credential_ref TEXT NOT NULL,
 updated_at TEXT NOT NULL,
 PRIMARY KEY (client_id, user_id, provider_id)
);
CREATE TABLE IF NOT EXISTS custom_targets (
 client_id TEXT NOT NULL,
 user_id TEXT NOT NULL,
 target_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 target_json BLOB NOT NULL,
 credential_ref TEXT NOT NULL,
 PRIMARY KEY (client_id, user_id, target_id),
 FOREIGN KEY (client_id, user_id, provider_id)
   REFERENCES custom_providers(client_id, user_id, provider_id) ON DELETE CASCADE
);`)
	if err != nil {
		return errors.New("localstore: SQLite schema could not be initialized")
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) ProviderOptions(ctx context.Context, principal identity.Principal) (routing.OptionsResponse, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT definition_json FROM custom_providers WHERE client_id=? AND user_id=? ORDER BY provider_id`, principal.ClientID, principal.UserID)
	if err != nil {
		return routing.OptionsResponse{}, errors.New("localstore: catalog query failed")
	}
	defer rows.Close()
	response := routing.OptionsResponse{GeneratedAt: time.Now().UTC()}
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return routing.OptionsResponse{}, errors.New("localstore: catalog row is invalid")
		}
		var provider routing.ProviderOption
		if err := json.Unmarshal(encoded, &provider); err != nil {
			return routing.OptionsResponse{}, errors.New("localstore: catalog definition is invalid")
		}
		response.Providers = append(response.Providers, provider)
	}
	if err := rows.Err(); err != nil {
		return routing.OptionsResponse{}, errors.New("localstore: catalog query failed")
	}
	return response, nil
}

func (s *Store) ResolveTarget(ctx context.Context, principal identity.Principal, targetID string) (managed.Target, error) {
	if targetID == "" {
		return managed.Target{}, &llmkit.ProviderError{Kind: llmkit.ErrorInvalidRequest, SafeMessage: "local custom target_id is required"}
	}
	var encoded []byte
	var reference string
	err := s.db.QueryRowContext(ctx, `SELECT target_json, credential_ref FROM custom_targets WHERE client_id=? AND user_id=? AND target_id=?`, principal.ClientID, principal.UserID, targetID).Scan(&encoded, &reference)
	if errors.Is(err, sql.ErrNoRows) {
		return managed.Target{}, &llmkit.ProviderError{Kind: llmkit.ErrorModelNotFound, SafeMessage: "local custom target was not found"}
	}
	if err != nil {
		return managed.Target{}, errors.New("localstore: target query failed")
	}
	var target llmkit.Target
	if err := json.Unmarshal(encoded, &target); err != nil {
		return managed.Target{}, errors.New("localstore: target definition is invalid")
	}
	return managed.Target{ID: targetID, Target: target, CredentialRef: reference}, nil
}

func (s *Store) UpsertCustomProvider(ctx context.Context, principal identity.Principal, input managed.CustomProviderInput) (routing.OptionsResponse, error) {
	defer clear(input.Credential.Value)
	if err := routing.ValidateCustomProvider(input.Provider); err != nil {
		return routing.OptionsResponse{}, err
	}
	if len(input.Credential.Value) == 0 {
		return routing.OptionsResponse{}, errors.New("localstore: credential is required")
	}
	credentialJSON, err := encodeCredential(input.Credential)
	if err != nil {
		return routing.OptionsResponse{}, err
	}
	defer clear(credentialJSON)
	reference := "custom/" + input.Provider.ID
	account := accountName(principal, reference)
	oldSecret, oldErr := keyring.Get(s.service, account)
	if oldErr != nil && !errors.Is(oldErr, keyring.ErrNotFound) {
		return routing.OptionsResponse{}, errors.New("localstore: OS secret store read failed")
	}
	hadOldSecret := oldErr == nil
	restoreSecret := func() {
		if hadOldSecret {
			_ = keyring.Set(s.service, account, oldSecret)
		} else {
			_ = keyring.Delete(s.service, account)
		}
	}
	if err := keyring.Set(s.service, account, string(credentialJSON)); err != nil {
		return routing.OptionsResponse{}, errors.New("localstore: OS secret store write failed")
	}
	providerJSON, _ := json.Marshal(input.Provider)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		restoreSecret()
		return routing.OptionsResponse{}, errors.New("localstore: SQLite transaction failed")
	}
	rollback := func() {
		_ = tx.Rollback()
		restoreSecret()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO custom_providers(client_id,user_id,provider_id,definition_json,credential_ref,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(client_id,user_id,provider_id) DO UPDATE SET definition_json=excluded.definition_json,credential_ref=excluded.credential_ref,updated_at=excluded.updated_at`, principal.ClientID, principal.UserID, input.Provider.ID, providerJSON, reference, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		rollback()
		return routing.OptionsResponse{}, errors.New("localstore: provider write failed")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM custom_targets WHERE client_id=? AND user_id=? AND provider_id=?`, principal.ClientID, principal.UserID, input.Provider.ID); err != nil {
		rollback()
		return routing.OptionsResponse{}, errors.New("localstore: target replacement failed")
	}
	for _, option := range input.Provider.Targets {
		targetJSON, _ := json.Marshal(option.Target)
		if _, err := tx.ExecContext(ctx, `INSERT INTO custom_targets(client_id,user_id,target_id,provider_id,target_json,credential_ref) VALUES(?,?,?,?,?,?)`, principal.ClientID, principal.UserID, option.ID, input.Provider.ID, targetJSON, reference); err != nil {
			rollback()
			return routing.OptionsResponse{}, errors.New("localstore: target write failed")
		}
	}
	if err := tx.Commit(); err != nil {
		restoreSecret()
		return routing.OptionsResponse{}, errors.New("localstore: SQLite commit failed")
	}
	return s.ProviderOptions(ctx, principal)
}

func (s *Store) DeleteCustomProvider(ctx context.Context, principal identity.Principal, providerID string) (routing.OptionsResponse, error) {
	if providerID == "" {
		return routing.OptionsResponse{}, errors.New("localstore: provider ID is required")
	}
	var reference string
	err := s.db.QueryRowContext(ctx, `SELECT credential_ref FROM custom_providers WHERE client_id=? AND user_id=? AND provider_id=?`, principal.ClientID, principal.UserID, providerID).Scan(&reference)
	if errors.Is(err, sql.ErrNoRows) {
		return s.ProviderOptions(ctx, principal)
	}
	if err != nil {
		return routing.OptionsResponse{}, errors.New("localstore: provider query failed")
	}
	account := accountName(principal, reference)
	oldSecret, getErr := keyring.Get(s.service, account)
	if getErr != nil && !errors.Is(getErr, keyring.ErrNotFound) {
		return routing.OptionsResponse{}, errors.New("localstore: OS secret store read failed")
	}
	if err := keyring.Delete(s.service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return routing.OptionsResponse{}, errors.New("localstore: OS secret store delete failed")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM custom_providers WHERE client_id=? AND user_id=? AND provider_id=?`, principal.ClientID, principal.UserID, providerID); err != nil {
		if getErr == nil {
			_ = keyring.Set(s.service, account, oldSecret)
		}
		return routing.OptionsResponse{}, errors.New("localstore: provider delete failed")
	}
	return s.ProviderOptions(ctx, principal)
}

func (s *Store) OpenCredential(_ context.Context, principal identity.Principal, reference string) (llmkit.CredentialHandle, func(), error) {
	secret, err := keyring.Get(s.service, accountName(principal, reference))
	if err != nil {
		return nil, func() {}, errors.New("localstore: credential is unavailable")
	}
	var input managed.CredentialInput
	if err := json.Unmarshal([]byte(secret), &input); err != nil {
		return nil, func() {}, errors.New("localstore: stored credential is malformed")
	}
	handle, err := newCredentialHandle(input)
	clear(input.Value)
	if err != nil {
		return nil, func() {}, err
	}
	return handle, handle.clear, nil
}

type credentialHandle struct {
	header string
	value  []byte
}

func (h *credentialHandle) Apply(_ context.Context, _ llmkit.Target, request *http.Request) error {
	request.Header.Set(h.header, string(h.value))
	return nil
}
func (h *credentialHandle) clear() { clear(h.value) }

func encodeCredential(input managed.CredentialInput) ([]byte, error) {
	handle, err := newCredentialHandle(input)
	if err != nil {
		return nil, err
	}
	handle.clear()
	encoded, err := json.Marshal(input)
	if err != nil {
		return nil, errors.New("localstore: credential encoding failed")
	}
	return encoded, nil
}

func newCredentialHandle(input managed.CredentialInput) (*credentialHandle, error) {
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
			return nil, errors.New("localstore: unsupported credential header")
		}
	default:
		clear(value)
		return nil, errors.New("localstore: unsupported credential type")
	}
	return &credentialHandle{header: header, value: value}, nil
}

func accountName(principal identity.Principal, reference string) string {
	return fmt.Sprintf("%x/%x/%s", principal.ClientID, principal.UserID, strings.ReplaceAll(reference, "/", "_"))
}

var _ managed.ConfigStore = (*Store)(nil)
var _ managed.SecretStore = (*Store)(nil)
var _ managed.CustomProviderStore = (*Store)(nil)
