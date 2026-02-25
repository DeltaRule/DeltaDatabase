package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Permission defines what an API key is allowed to do.
type Permission string

const (
	// PermRead allows reading entity data.
	PermRead Permission = "read"
	// PermWrite allows writing entity data.
	PermWrite Permission = "write"
	// PermAdmin grants full access including key management.
	PermAdmin Permission = "admin"
)

// AllPermissions is a convenience slice containing all permissions.
var AllPermissions = []Permission{PermRead, PermWrite, PermAdmin}

// APIKey represents a named API key with RBAC permissions.
type APIKey struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	KeyHash     string       `json:"key_hash"` // SHA-256 of the raw secret
	Permissions []Permission `json:"permissions"`
	ExpiresAt   *time.Time   `json:"expires_at,omitempty"` // nil = never expires
	CreatedAt   time.Time    `json:"created_at"`
	Enabled     bool         `json:"enabled"`
}

// HasPermission reports whether the key has been granted p.
// A key with PermAdmin implicitly satisfies any permission check.
func (k *APIKey) HasPermission(p Permission) bool {
	for _, granted := range k.Permissions {
		if granted == PermAdmin || granted == p {
			return true
		}
	}
	return false
}

// IsExpired reports whether the key has an expiry time that has passed.
func (k *APIKey) IsExpired() bool {
	if k.ExpiresAt == nil {
		return false
	}
	return time.Now().After(*k.ExpiresAt)
}

// KeyManager manages API keys with RBAC and optional filesystem persistence.
type KeyManager struct {
	mu        sync.RWMutex
	keys      map[string]*APIKey // keyHash → APIKey
	idIndex   map[string]string  // id → keyHash
	storePath string             // JSON file path; empty = in-memory only
}

// NewKeyManager creates a new KeyManager. If storePath is non-empty the
// manager loads existing keys from that file (if it exists) and writes
// updates to it.
func NewKeyManager(storePath string) (*KeyManager, error) {
	km := &KeyManager{
		keys:      make(map[string]*APIKey),
		idIndex:   make(map[string]string),
		storePath: storePath,
	}
	if storePath != "" {
		if err := km.load(); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to load key store: %w", err)
		}
	}
	return km, nil
}

// CreateKey generates a new API key with the supplied attributes.
// It returns the raw secret (shown only once) and the stored APIKey record.
func (km *KeyManager) CreateKey(name string, permissions []Permission, expiresAt *time.Time) (secret string, key *APIKey, err error) {
	if name == "" {
		return "", nil, fmt.Errorf("key name cannot be empty")
	}
	if len(permissions) == 0 {
		return "", nil, fmt.Errorf("at least one permission is required")
	}

	// Generate a 32-byte cryptographically secure random secret.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("failed to generate key secret: %w", err)
	}
	secret = "dk_" + hex.EncodeToString(raw) // "dk_" = DeltaDatabase Key prefix

	// Hash for storage — the raw secret is never persisted.
	sum := sha256.Sum256([]byte(secret))
	keyHash := hex.EncodeToString(sum[:])

	// Unique ID (8 random bytes displayed as hex).
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, fmt.Errorf("failed to generate key ID: %w", err)
	}
	id := hex.EncodeToString(idBytes)

	apiKey := &APIKey{
		ID:          id,
		Name:        name,
		KeyHash:     keyHash,
		Permissions: permissions,
		ExpiresAt:   expiresAt,
		CreatedAt:   time.Now().UTC(),
		Enabled:     true,
	}

	km.mu.Lock()
	km.keys[keyHash] = apiKey
	km.idIndex[id] = keyHash
	km.mu.Unlock()

	km.save() // non-fatal; logged internally
	return secret, apiKey, nil
}

// ValidateKey checks the raw secret and returns the corresponding APIKey
// when the key is valid, enabled, and not expired.
func (km *KeyManager) ValidateKey(secret string) (*APIKey, error) {
	if secret == "" {
		return nil, fmt.Errorf("key secret cannot be empty")
	}
	sum := sha256.Sum256([]byte(secret))
	keyHash := hex.EncodeToString(sum[:])

	km.mu.RLock()
	key, exists := km.keys[keyHash]
	km.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("invalid key")
	}
	if !key.Enabled {
		return nil, fmt.Errorf("key is disabled")
	}
	if key.IsExpired() {
		return nil, fmt.Errorf("key has expired")
	}
	return key, nil
}

// RevokeKey disables the key with the given ID.
func (km *KeyManager) RevokeKey(id string) error {
	km.mu.Lock()
	defer km.mu.Unlock()

	keyHash, exists := km.idIndex[id]
	if !exists {
		return fmt.Errorf("key not found: %s", id)
	}
	km.keys[keyHash].Enabled = false
	go km.save()
	return nil
}

// DeleteKey permanently removes the key with the given ID.
func (km *KeyManager) DeleteKey(id string) error {
	km.mu.Lock()
	defer km.mu.Unlock()

	keyHash, exists := km.idIndex[id]
	if !exists {
		return fmt.Errorf("key not found: %s", id)
	}
	delete(km.keys, keyHash)
	delete(km.idIndex, id)
	go km.save()
	return nil
}

// GetKey returns the APIKey for the given ID.
func (km *KeyManager) GetKey(id string) (*APIKey, error) {
	km.mu.RLock()
	defer km.mu.RUnlock()

	keyHash, exists := km.idIndex[id]
	if !exists {
		return nil, fmt.Errorf("key not found: %s", id)
	}
	return km.keys[keyHash], nil
}

// ListKeys returns all stored API keys (without secrets).
func (km *KeyManager) ListKeys() []*APIKey {
	km.mu.RLock()
	defer km.mu.RUnlock()

	out := make([]*APIKey, 0, len(km.keys))
	for _, k := range km.keys {
		out = append(out, k)
	}
	return out
}

// Count returns the number of stored API keys.
func (km *KeyManager) Count() int {
	km.mu.RLock()
	defer km.mu.RUnlock()
	return len(km.keys)
}

// load reads the JSON key store from disk.
func (km *KeyManager) load() error {
	data, err := os.ReadFile(km.storePath)
	if err != nil {
		return err
	}
	var keys []*APIKey
	if err := json.Unmarshal(data, &keys); err != nil {
		return fmt.Errorf("failed to parse key store: %w", err)
	}
	for _, k := range keys {
		km.keys[k.KeyHash] = k
		km.idIndex[k.ID] = k.KeyHash
	}
	return nil
}

// save writes the current key store to disk atomically.
// Errors are silently discarded (logging would create a dependency cycle).
func (km *KeyManager) save() {
	if km.storePath == "" {
		return
	}

	km.mu.RLock()
	keys := make([]*APIKey, 0, len(km.keys))
	for _, k := range km.keys {
		keys = append(keys, k)
	}
	km.mu.RUnlock()

	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return
	}

	dir := filepath.Dir(km.storePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	tmp := km.storePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	os.Rename(tmp, km.storePath) //nolint:errcheck
}
