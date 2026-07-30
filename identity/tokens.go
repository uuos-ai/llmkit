package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TokenMode string

const (
	TokenSidecar TokenMode = "sidecar"
	TokenLocal   TokenMode = "local"
	TokenGateway TokenMode = "gateway"
)

var (
	ErrTokenInvalid = errors.New("identity: token is invalid")
	ErrTokenExpired = errors.New("identity: token is expired")
	ErrTokenReplay  = errors.New("identity: refresh token replay detected")
)

func AccessPrefix(mode TokenMode) string {
	switch mode {
	case TokenSidecar:
		return "llmk_s1_"
	case TokenLocal:
		return "llmk_l1_"
	case TokenGateway:
		return "llmk_g1_"
	default:
		return ""
	}
}

func RefreshPrefix(mode TokenMode) string {
	switch mode {
	case TokenSidecar:
		return "llmk_sr1_"
	case TokenLocal:
		return "llmk_lr1_"
	case TokenGateway:
		return "llmk_gr1_"
	default:
		return ""
	}
}

const (
	LocalEnrollmentPrefix = "llmk_le1_"
	LocalAdminPrefix      = "llmk_la1_"
)

type TokenPair struct {
	AccessToken     string    `json:"access_token"`
	RefreshToken    string    `json:"refresh_token"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
	FamilyExpiresAt time.Time `json:"family_expires_at"`
}

type TokenManagerConfig struct {
	Mode           TokenMode
	Pepper         []byte
	AccessTTL      time.Duration
	FamilyTTL      time.Duration
	IdempotencyTTL time.Duration
	Now            func() time.Time
}

type tokenRecord struct {
	id, secretMAC, familyID string
	generation              uint64
	principal               Principal
	expiresAt               time.Time
	used                    bool
	idempotencyKey          string
	cached                  []byte
	cacheExpiresAt          time.Time
}

type familyRecord struct {
	createdAt, expiresAt time.Time
	generation           uint64
	revoked              bool
}

type BindingResult struct {
	Tokens  TokenPair   `json:"tokens"`
	Binding UserBinding `json:"binding"`
}

type encryptedRecovery struct {
	value     []byte
	expiresAt time.Time
}

// TokenManager is an in-memory reference implementation of the token-family
// contract. Gateway hosts should persist equivalent records atomically in
// SessionStore and keep the pepper in KMS/HSM/SecretStore.
type TokenManager struct {
	mode                                 TokenMode
	pepper                               []byte
	accessTTL, familyTTL, idempotencyTTL time.Duration
	now                                  func() time.Time
	aead                                 cipher.AEAD
	mu                                   sync.Mutex
	access, refresh                      map[string]*tokenRecord
	families                             map[string]*familyRecord
	bindingRecovery                      map[string]encryptedRecovery
}

func NewTokenManager(config TokenManagerConfig) (*TokenManager, error) {
	if AccessPrefix(config.Mode) == "" || len(config.Pepper) < 32 || config.AccessTTL <= 0 || config.FamilyTTL <= 0 {
		return nil, errors.New("identity: valid mode, 32-byte pepper, and positive token TTLs are required")
	}
	if config.IdempotencyTTL <= 0 {
		config.IdempotencyTTL = time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	key := hmacDigest(config.Pepper, []byte("llmkit/idempotency-cache/v1"))
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &TokenManager{mode: config.Mode, pepper: append([]byte(nil), config.Pepper...), accessTTL: config.AccessTTL,
		familyTTL: config.FamilyTTL, idempotencyTTL: config.IdempotencyTTL, now: config.Now, aead: aead,
		access: make(map[string]*tokenRecord), refresh: make(map[string]*tokenRecord), families: make(map[string]*familyRecord), bindingRecovery: make(map[string]encryptedRecovery)}, nil
}

func (m *TokenManager) Issue(principal Principal) (TokenPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	familyID, err := randomText(16)
	if err != nil {
		return TokenPair{}, err
	}
	m.families[familyID] = &familyRecord{createdAt: now, expiresAt: now.Add(m.familyTTL), generation: 1}
	return m.issueLocked(principal, familyID, 1, now)
}

func (m *TokenManager) Authenticate(_ context.Context, token []byte) (Principal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, secret, ok := parseToken(string(token), AccessPrefix(m.mode))
	if !ok {
		return Principal{}, false
	}
	record := m.access[id]
	if record == nil || !m.validSecret(record, secret) || record.used || !m.now().Before(record.expiresAt) {
		return Principal{}, false
	}
	family := m.families[record.familyID]
	if family == nil || family.revoked || family.generation != record.generation || !m.now().Before(family.expiresAt) {
		return Principal{}, false
	}
	return clonePrincipal(record.principal), true
}

func (m *TokenManager) AuthenticateRecovery(_ context.Context, token []byte) (Principal, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, secret, ok := parseToken(string(token), AccessPrefix(m.mode))
	if !ok {
		return Principal{}, false
	}
	record := m.access[id]
	if record == nil || !record.used || !m.validSecret(record, secret) || !m.now().Before(record.cacheExpiresAt) {
		return Principal{}, false
	}
	principal := clonePrincipal(record.principal)
	principal.Scopes = map[string]struct{}{ScopeTokensRefresh: {}, ScopeUsersBind: {}}
	return principal, true
}

// Refresh rotates access and refresh tokens together. The refresh token is
// usable only while its associated access token and family are still valid.
// An exact idempotent retry returns the encrypted cached result; any other
// reuse revokes the whole family.
func (m *TokenManager) Refresh(refreshToken, idempotencyKey string) (TokenPair, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, secret, ok := parseToken(refreshToken, RefreshPrefix(m.mode))
	if !ok || idempotencyKey == "" {
		return TokenPair{}, ErrTokenInvalid
	}
	record := m.refresh[id]
	if record == nil || !m.validSecret(record, secret) {
		return TokenPair{}, ErrTokenInvalid
	}
	now := m.now().UTC()
	if record.used {
		if record.idempotencyKey == idempotencyKey && now.Before(record.cacheExpiresAt) {
			return m.decryptPair(record.cached)
		}
		m.revokeFamilyLocked(record.familyID)
		return TokenPair{}, ErrTokenReplay
	}
	family := m.families[record.familyID]
	if family == nil || family.revoked {
		return TokenPair{}, ErrTokenInvalid
	}
	if !now.Before(record.expiresAt) || !now.Before(family.expiresAt) {
		return TokenPair{}, ErrTokenExpired
	}
	record.used = true
	if access := m.accessByFamilyGenerationLocked(record.familyID, record.generation); access != nil {
		access.used = true
	}
	family.generation++
	pair, err := m.issueLocked(record.principal, record.familyID, family.generation, now)
	if err != nil {
		m.revokeFamilyLocked(record.familyID)
		return TokenPair{}, err
	}
	cached, err := m.encryptPair(pair)
	if err != nil {
		m.revokeFamilyLocked(record.familyID)
		return TokenPair{}, err
	}
	record.idempotencyKey, record.cached, record.cacheExpiresAt = idempotencyKey, cached, now.Add(m.idempotencyTTL)
	if access := m.accessByFamilyGenerationLocked(record.familyID, record.generation); access != nil {
		access.cacheExpiresAt = record.cacheExpiresAt
	}
	return pair, nil
}

func (m *TokenManager) RevokeFamily(familyID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revokeFamilyLocked(familyID)
}

// ReplaceClientPrincipal revokes every token family for a client before
// issuing the first family for a new binding version.
func (m *TokenManager) ReplaceClientPrincipal(principal Principal) (TokenPair, error) {
	if principal.ClientID == "" {
		return TokenPair{}, ErrTokenInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	for _, record := range m.access {
		if record.principal.ClientID == principal.ClientID {
			record.used = true
			record.cacheExpiresAt = now.Add(m.idempotencyTTL)
			m.revokeFamilyLocked(record.familyID)
		}
	}
	familyID, err := randomText(16)
	if err != nil {
		return TokenPair{}, err
	}
	m.families[familyID] = &familyRecord{createdAt: now, expiresAt: now.Add(m.familyTTL), generation: 1}
	return m.issueLocked(principal, familyID, 1, now)
}

func (m *TokenManager) RememberBindingResult(clientID string, expected uint64, idempotencyKey string, result BindingResult) error {
	if clientID == "" || idempotencyKey == "" {
		return ErrTokenInvalid
	}
	plain, err := json.Marshal(result)
	if err != nil {
		return err
	}
	defer clear(plain)
	m.mu.Lock()
	defer m.mu.Unlock()
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	key := m.bindingRecoveryKey(clientID, expected, idempotencyKey)
	m.bindingRecovery[key] = encryptedRecovery{value: m.aead.Seal(nonce, nonce, plain, nil), expiresAt: m.now().UTC().Add(m.idempotencyTTL)}
	return nil
}

func (m *TokenManager) RecoverBindingResult(clientID string, expected uint64, idempotencyKey string) (BindingResult, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.bindingRecoveryKey(clientID, expected, idempotencyKey)
	cached, ok := m.bindingRecovery[key]
	if !ok || !m.now().Before(cached.expiresAt) || len(cached.value) < m.aead.NonceSize() {
		delete(m.bindingRecovery, key)
		return BindingResult{}, false
	}
	plain, err := m.aead.Open(nil, cached.value[:m.aead.NonceSize()], cached.value[m.aead.NonceSize():], nil)
	if err != nil {
		return BindingResult{}, false
	}
	defer clear(plain)
	var result BindingResult
	if json.Unmarshal(plain, &result) != nil {
		return BindingResult{}, false
	}
	return result, true
}

func (m *TokenManager) bindingRecoveryKey(clientID string, expected uint64, idempotencyKey string) string {
	data := []byte(clientID + "\x00" + idempotencyKey + "\x00" + strconv.FormatUint(expected, 10))
	defer clear(data)
	return base64.RawURLEncoding.EncodeToString(hmacDigest(m.pepper, data))
}

func (m *TokenManager) issueLocked(principal Principal, familyID string, generation uint64, now time.Time) (TokenPair, error) {
	accessID, accessSecret, err := tokenMaterial()
	if err != nil {
		return TokenPair{}, err
	}
	refreshID, refreshSecret, err := tokenMaterial()
	if err != nil {
		return TokenPair{}, err
	}
	family := m.families[familyID]
	accessExpiry := now.Add(m.accessTTL)
	if accessExpiry.After(family.expiresAt) {
		accessExpiry = family.expiresAt
	}
	m.access[accessID] = &tokenRecord{id: accessID, secretMAC: m.mac(accessSecret), familyID: familyID, generation: generation, principal: clonePrincipal(principal), expiresAt: accessExpiry}
	// Refresh expires with the access token by confirmed contract.
	m.refresh[refreshID] = &tokenRecord{id: refreshID, secretMAC: m.mac(refreshSecret), familyID: familyID, generation: generation, principal: clonePrincipal(principal), expiresAt: accessExpiry}
	return TokenPair{AccessToken: AccessPrefix(m.mode) + accessID + "." + accessSecret, RefreshToken: RefreshPrefix(m.mode) + refreshID + "." + refreshSecret, AccessExpiresAt: accessExpiry, FamilyExpiresAt: family.expiresAt}, nil
}

func (m *TokenManager) validSecret(record *tokenRecord, secret string) bool {
	want, got := []byte(record.secretMAC), []byte(m.mac(secret))
	return len(want) == len(got) && subtle.ConstantTimeCompare(want, got) == 1
}

func (m *TokenManager) mac(secret string) string {
	return base64.RawURLEncoding.EncodeToString(hmacDigest(m.pepper, []byte(secret)))
}
func (m *TokenManager) revokeFamilyLocked(id string) {
	if f := m.families[id]; f != nil {
		f.revoked = true
	}
}
func (m *TokenManager) accessByFamilyGenerationLocked(id string, generation uint64) *tokenRecord {
	for _, record := range m.access {
		if record.familyID == id && record.generation == generation {
			return record
		}
	}
	return nil
}

func tokenMaterial() (string, string, error) {
	id, err := randomText(16)
	if err != nil {
		return "", "", err
	}
	secret, err := randomText(32)
	return id, secret, err
}
func randomText(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func parseToken(token, prefix string) (string, string, bool) {
	if prefix == "" || !strings.HasPrefix(token, prefix) {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(token, prefix), ".")
	returnValue := len(parts) == 2 && parts[0] != "" && parts[1] != ""
	if !returnValue {
		return "", "", false
	}
	return parts[0], parts[1], true
}
func hmacDigest(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write(data)
	return h.Sum(nil)
}

func (m *TokenManager) encryptPair(pair TokenPair) ([]byte, error) {
	plain := []byte(pair.AccessToken + "\n" + pair.RefreshToken + "\n" + pair.AccessExpiresAt.Format(time.RFC3339Nano) + "\n" + pair.FamilyExpiresAt.Format(time.RFC3339Nano))
	defer clear(plain)
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return m.aead.Seal(nonce, nonce, plain, nil), nil
}
func (m *TokenManager) decryptPair(data []byte) (TokenPair, error) {
	if len(data) < m.aead.NonceSize() {
		return TokenPair{}, ErrTokenInvalid
	}
	nonce := data[:m.aead.NonceSize()]
	plain, err := m.aead.Open(nil, nonce, data[m.aead.NonceSize():], nil)
	if err != nil {
		return TokenPair{}, ErrTokenInvalid
	}
	defer clear(plain)
	parts := strings.Split(string(plain), "\n")
	if len(parts) != 4 {
		return TokenPair{}, ErrTokenInvalid
	}
	a, err := time.Parse(time.RFC3339Nano, parts[2])
	if err != nil {
		return TokenPair{}, ErrTokenInvalid
	}
	f, err := time.Parse(time.RFC3339Nano, parts[3])
	if err != nil {
		return TokenPair{}, ErrTokenInvalid
	}
	return TokenPair{AccessToken: parts[0], RefreshToken: parts[1], AccessExpiresAt: a, FamilyExpiresAt: f}, nil
}

var _ Authenticator = (*TokenManager)(nil)
var _ RecoveryAuthenticator = (*TokenManager)(nil)
