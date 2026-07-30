package identity

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	ErrBindingUnauthorized = errors.New("identity: user binding is not authorized")
	ErrBindingConflict     = errors.New("identity: user binding version conflict")
)

// UserBinding is a time-varying, client-local identity. UserID values are
// never correlated across ClientIDs, even when their text is identical.
type UserBinding struct {
	ClientID       string    `json:"client_id"`
	UserID         string    `json:"user_id"`
	BindingVersion uint64    `json:"binding_version"`
	BoundAt        time.Time `json:"bound_at"`
}

type BindRequest struct {
	ClientID               string
	ClientInstanceID       string
	UserID                 string
	ExpectedBindingVersion uint64
	Proof                  []byte
}

// UserBindingAuthorizer lets a host apply mode-specific trust: launch-token
// trust in sidecar, client-token/assertion in local-service, or business API /
// OIDC assertion in gateway.
type UserBindingAuthorizer interface {
	AuthorizeBind(context.Context, BindRequest) error
}

type UserBindingAuthorizerFunc func(context.Context, BindRequest) error

func (f UserBindingAuthorizerFunc) AuthorizeBind(ctx context.Context, request BindRequest) error {
	return f(ctx, request)
}

type UserBindingStore interface {
	Current(context.Context, string) (UserBinding, bool, error)
	Bind(context.Context, BindRequest) (previous UserBinding, current UserBinding, err error)
	Watch(context.Context, string, uint64) (<-chan UserBinding, error)
}

// MemoryUserBindingStore is process-local. Gateway deployments must provide a
// strongly consistent external implementation; local-service may opt into one.
type MemoryUserBindingStore struct {
	authorizer UserBindingAuthorizer
	mu         sync.Mutex
	bindings   map[string]UserBinding
	watchers   map[string]map[chan UserBinding]struct{}
}

func NewMemoryUserBindingStore(authorizer UserBindingAuthorizer) *MemoryUserBindingStore {
	return &MemoryUserBindingStore{
		authorizer: authorizer,
		bindings:   make(map[string]UserBinding),
		watchers:   make(map[string]map[chan UserBinding]struct{}),
	}
}

func (s *MemoryUserBindingStore) Current(_ context.Context, clientID string) (UserBinding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	binding, ok := s.bindings[clientID]
	return binding, ok, nil
}

func (s *MemoryUserBindingStore) Bind(ctx context.Context, request BindRequest) (UserBinding, UserBinding, error) {
	if request.ClientID == "" || request.UserID == "" {
		return UserBinding{}, UserBinding{}, errors.New("identity: client_id and user_id are required")
	}
	if s.authorizer != nil {
		if err := s.authorizer.AuthorizeBind(ctx, request); err != nil {
			return UserBinding{}, UserBinding{}, ErrBindingUnauthorized
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.bindings[request.ClientID]
	if request.ExpectedBindingVersion != previous.BindingVersion {
		return UserBinding{}, UserBinding{}, ErrBindingConflict
	}
	current := UserBinding{
		ClientID: request.ClientID, UserID: request.UserID,
		BindingVersion: previous.BindingVersion + 1, BoundAt: time.Now().UTC(),
	}
	s.bindings[request.ClientID] = current
	for watcher := range s.watchers[request.ClientID] {
		select {
		case watcher <- current:
		default:
		}
	}
	return previous, current, nil
}

func (s *MemoryUserBindingStore) Watch(ctx context.Context, clientID string, after uint64) (<-chan UserBinding, error) {
	if clientID == "" {
		return nil, errors.New("identity: client_id is required")
	}
	updates := make(chan UserBinding, 1)
	s.mu.Lock()
	if current, ok := s.bindings[clientID]; ok && current.BindingVersion > after {
		updates <- current
	}
	if s.watchers[clientID] == nil {
		s.watchers[clientID] = make(map[chan UserBinding]struct{})
	}
	s.watchers[clientID][updates] = struct{}{}
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		delete(s.watchers[clientID], updates)
		close(updates)
		s.mu.Unlock()
	}()
	return updates, nil
}
