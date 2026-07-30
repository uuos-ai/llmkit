package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"sync"
)

type enrollmentRecord struct {
	principal Principal
	used      bool
}

// EnrollmentTokens authenticates pre-provisioned llmk_le1_ tokens and
// consumes each client-instance enrollment exactly once per store lifetime.
type EnrollmentTokens struct {
	mu      sync.Mutex
	records map[[sha256.Size]byte]*enrollmentRecord
}

func NewEnrollmentTokens(records []TokenRecord) (*EnrollmentTokens, error) {
	store := &EnrollmentTokens{records: make(map[[sha256.Size]byte]*enrollmentRecord, len(records))}
	instances := make(map[string]struct{}, len(records))
	for _, record := range records {
		if record.ClientID == "" || record.ClientInstanceID == "" {
			return nil, errors.New("identity: enrollment requires client_id and client_instance_id")
		}
		key := record.ClientID + "\x00" + record.ClientInstanceID
		if _, duplicate := instances[key]; duplicate {
			return nil, errors.New("identity: duplicate enrollment client instance")
		}
		instances[key] = struct{}{}
		decoded, err := hex.DecodeString(record.TokenSHA256)
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("identity: token_sha256 must be a SHA-256 hex digest")
		}
		var digest [sha256.Size]byte
		copy(digest[:], decoded)
		if _, duplicate := store.records[digest]; duplicate {
			return nil, errors.New("identity: duplicate enrollment token hash")
		}
		scopes := make(map[string]struct{}, len(record.Scopes))
		for _, scope := range record.Scopes {
			if scope != "" && scope != ScopeClientsEnroll {
				scopes[scope] = struct{}{}
			}
		}
		store.records[digest] = &enrollmentRecord{principal: Principal{ClientID: record.ClientID, ClientInstanceID: record.ClientInstanceID, UserID: record.UserID, BindingVersion: record.BindingVersion, Scopes: scopes}}
	}
	if len(store.records) == 0 {
		return nil, errors.New("identity: at least one enrollment token is required")
	}
	return store, nil
}

func LoadEnrollmentHashFile(path string) (*EnrollmentTokens, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, errors.New("identity: enrollment token file could not be inspected")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("identity: enrollment token file permissions must not grant group or other access")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("identity: enrollment token file could not be opened")
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var records []TokenRecord
	if err := decoder.Decode(&records); err != nil {
		return nil, errors.New("identity: enrollment token file is malformed")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("identity: enrollment token file must contain exactly one JSON value")
	}
	return NewEnrollmentTokens(records)
}

func (s *EnrollmentTokens) Authenticate(_ context.Context, token []byte) (Principal, bool) {
	if len(token) <= len(LocalEnrollmentPrefix) || string(token[:len(LocalEnrollmentPrefix)]) != LocalEnrollmentPrefix {
		return Principal{}, false
	}
	digest := sha256.Sum256(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[digest]
	if record == nil || record.used {
		return Principal{}, false
	}
	principal := clonePrincipal(record.principal)
	principal.Scopes = map[string]struct{}{ScopeClientsEnroll: {}}
	return principal, true
}

func (s *EnrollmentTokens) Consume(principal Principal) (Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if !record.used && record.principal.ClientID == principal.ClientID && record.principal.ClientInstanceID == principal.ClientInstanceID {
			record.used = true
			return clonePrincipal(record.principal), nil
		}
	}
	return Principal{}, errors.New("identity: enrollment token was already used")
}

var _ Authenticator = (*EnrollmentTokens)(nil)
