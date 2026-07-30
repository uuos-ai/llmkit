// Package blobstore provides bounded temporary content storage for sidecar and
// local-service. Gateway hosts should supply an external managed.BlobStore.
package blobstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/uuos-ai/llmkit/identity"
	"github.com/uuos-ai/llmkit/managed"
)

type Config struct {
	MaxBlobBytes int64
	TTL          time.Duration
	Now          func() time.Time
}
type entry struct {
	owner    string
	metadata managed.BlobMetadata
	data     []byte
}
type Memory struct {
	config Config
	mu     sync.Mutex
	blobs  map[string]entry
}

func NewMemory(config Config) (*Memory, error) {
	if config.MaxBlobBytes <= 0 {
		config.MaxBlobBytes = 16 << 20
	}
	if config.TTL <= 0 {
		config.TTL = 10 * time.Minute
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Memory{config: config, blobs: make(map[string]entry)}, nil
}

func (s *Memory) Put(_ context.Context, principal identity.Principal, metadata managed.BlobMetadata, reader io.Reader) (managed.BlobMetadata, error) {
	if principal.ClientID == "" || principal.UserID == "" || principal.BindingVersion == 0 || reader == nil {
		return managed.BlobMetadata{}, errors.New("blobstore: bound principal and content are required")
	}
	data, err := io.ReadAll(io.LimitReader(reader, s.config.MaxBlobBytes+1))
	if err != nil || int64(len(data)) > s.config.MaxBlobBytes {
		clear(data)
		return managed.BlobMetadata{}, errors.New("blobstore: content exceeds limit")
	}
	digest := sha256.Sum256(data)
	checksum := "sha256:" + hex.EncodeToString(digest[:])
	if metadata.Checksum != "" && metadata.Checksum != checksum {
		clear(data)
		return managed.BlobMetadata{}, errors.New("blobstore: checksum mismatch")
	}
	idBytes := make([]byte, 18)
	if _, err := rand.Read(idBytes); err != nil {
		clear(data)
		return managed.BlobMetadata{}, errors.New("blobstore: reference generation failed")
	}
	metadata.Ref = "blob_" + base64.RawURLEncoding.EncodeToString(idBytes)
	metadata.SizeBytes = int64(len(data))
	metadata.Checksum = checksum
	metadata.ExpiresAt = s.config.Now().UTC().Add(s.config.TTL)
	s.mu.Lock()
	s.cleanupLocked()
	s.blobs[metadata.Ref] = entry{owner: owner(principal), metadata: metadata, data: data}
	s.mu.Unlock()
	return metadata, nil
}

func (s *Memory) Open(_ context.Context, principal identity.Principal, ref string) (io.ReadCloser, managed.BlobMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanupLocked()
	item, ok := s.blobs[ref]
	if !ok || item.owner != owner(principal) {
		return nil, managed.BlobMetadata{}, errors.New("blobstore: blob is unavailable")
	}
	data := append([]byte(nil), item.data...)
	return io.NopCloser(bytes.NewReader(data)), item.metadata, nil
}

func (s *Memory) Delete(_ context.Context, principal identity.Principal, ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.blobs[ref]
	if !ok || item.owner != owner(principal) {
		return errors.New("blobstore: blob is unavailable")
	}
	clear(item.data)
	delete(s.blobs, ref)
	return nil
}

func (s *Memory) cleanupLocked() {
	now := s.config.Now()
	for ref, item := range s.blobs {
		if !now.Before(item.metadata.ExpiresAt) {
			clear(item.data)
			delete(s.blobs, ref)
		}
	}
}
func owner(p identity.Principal) string {
	return p.ClientID + "\x00" + p.UserID + "\x00" + strconv.FormatUint(p.BindingVersion, 10)
}

var _ managed.BlobStore = (*Memory)(nil)
