package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// TokenManager handles token generation and validation for workers and clients.
type TokenManager struct {
	// Worker tokens: short-lived tokens for Processing Workers
	workerTokens map[string]*WorkerToken
	// Client tokens: tokens for REST API clients
	clientTokens map[string]*ClientToken
	mu           sync.RWMutex

	// Configuration
	workerTokenTTL time.Duration
	clientTokenTTL time.Duration
}

// WorkerToken represents an authenticated Processing Worker session.
type WorkerToken struct {
	Token     string
	WorkerID  string
	ExpiresAt time.Time
	KeyID     string
	Tags      map[string]string
}

// ClientToken represents an authenticated client session.
type ClientToken struct {
	Token     string
	ClientID  string
	ExpiresAt time.Time
	Roles     []string
}

// TokenType represents the type of token.
type TokenType int

const (
	TokenTypeWorker TokenType = iota
	TokenTypeClient
)

// NewTokenManager creates a new token manager with specified TTLs.
func NewTokenManager(workerTokenTTL, clientTokenTTL time.Duration) *TokenManager {
	if workerTokenTTL == 0 {
		workerTokenTTL = 1 * time.Hour // Default: 1 hour
	}
	if clientTokenTTL == 0 {
		clientTokenTTL = 24 * time.Hour // Default: 24 hours
	}

	tm := &TokenManager{
		workerTokens:   make(map[string]*WorkerToken),
		clientTokens:   make(map[string]*ClientToken),
		workerTokenTTL: workerTokenTTL,
		clientTokenTTL: clientTokenTTL,
	}

	// Start cleanup goroutine
	go tm.cleanupExpiredTokens()

	return tm
}

// GenerateWorkerToken creates a new token for a Processing Worker.
func (tm *TokenManager) GenerateWorkerToken(workerID, keyID string, tags map[string]string) (*WorkerToken, error) {
	if workerID == "" {
		return nil, fmt.Errorf("worker ID cannot be empty")
	}

	token, err := generateSecureToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	wt := &WorkerToken{
		Token:     token,
		WorkerID:  workerID,
		ExpiresAt: time.Now().Add(tm.workerTokenTTL),
		KeyID:     keyID,
		Tags:      tags,
	}

	tm.mu.Lock()
	tm.workerTokens[token] = wt
	tm.mu.Unlock()

	return wt, nil
}

// GenerateClientToken creates a new token for a REST API client.
func (tm *TokenManager) GenerateClientToken(clientID string, roles []string) (*ClientToken, error) {
	if clientID == "" {
		return nil, fmt.Errorf("client ID cannot be empty")
	}

	token, err := generateSecureToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	ct := &ClientToken{
		Token:     token,
		ClientID:  clientID,
		ExpiresAt: time.Now().Add(tm.clientTokenTTL),
		Roles:     roles,
	}

	tm.mu.Lock()
	tm.clientTokens[token] = ct
	tm.mu.Unlock()

	return ct, nil
}

// ValidateWorkerToken verifies a worker token and returns the associated worker info.
func (tm *TokenManager) ValidateWorkerToken(token string) (*WorkerToken, error) {
	if token == "" {
		return nil, fmt.Errorf("token cannot be empty")
	}

	tm.mu.RLock()
	wt, exists := tm.workerTokens[token]
	tm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("invalid token")
	}

	if time.Now().After(wt.ExpiresAt) {
		tm.mu.Lock()
		delete(tm.workerTokens, token)
		tm.mu.Unlock()
		return nil, fmt.Errorf("token expired")
	}

	return wt, nil
}

// ValidateClientToken verifies a client token and returns the associated client info.
func (tm *TokenManager) ValidateClientToken(token string) (*ClientToken, error) {
	if token == "" {
		return nil, fmt.Errorf("token cannot be empty")
	}

	tm.mu.RLock()
	ct, exists := tm.clientTokens[token]
	tm.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("invalid token")
	}

	if time.Now().After(ct.ExpiresAt) {
		tm.mu.Lock()
		delete(tm.clientTokens, token)
		tm.mu.Unlock()
		return nil, fmt.Errorf("token expired")
	}

	return ct, nil
}

// RevokeWorkerToken invalidates a worker token.
func (tm *TokenManager) RevokeWorkerToken(token string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, exists := tm.workerTokens[token]; !exists {
		return fmt.Errorf("token not found")
	}

	delete(tm.workerTokens, token)
	return nil
}

// RevokeClientToken invalidates a client token.
func (tm *TokenManager) RevokeClientToken(token string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if _, exists := tm.clientTokens[token]; !exists {
		return fmt.Errorf("token not found")
	}

	delete(tm.clientTokens, token)
	return nil
}

// GetWorkerTokenCount returns the number of active worker tokens.
func (tm *TokenManager) GetWorkerTokenCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return len(tm.workerTokens)
}

// GetClientTokenCount returns the number of active client tokens.
func (tm *TokenManager) GetClientTokenCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return len(tm.clientTokens)
}

// cleanupExpiredTokens periodically removes expired tokens.
func (tm *TokenManager) cleanupExpiredTokens() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()

		tm.mu.Lock()

		// Cleanup expired worker tokens
		for token, wt := range tm.workerTokens {
			if now.After(wt.ExpiresAt) {
				delete(tm.workerTokens, token)
			}
		}

		// Cleanup expired client tokens
		for token, ct := range tm.clientTokens {
			if now.After(ct.ExpiresAt) {
				delete(tm.clientTokens, token)
			}
		}

		tm.mu.Unlock()
	}
}

// generateSecureToken creates a cryptographically secure random token.
func generateSecureToken() (string, error) {
	// Generate 32 random bytes
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	// Return base64 encoded token
	return base64.URLEncoding.EncodeToString(b), nil
}

// WorkerAuthenticator handles worker authentication logic.
type WorkerAuthenticator struct {
	// Known worker IDs and their credentials
	workers map[string]*WorkerCredentials
	mu      sync.RWMutex
}

// WorkerCredentials stores authentication info for a worker.
type WorkerCredentials struct {
	WorkerID     string
	PasswordHash string // SHA256 hash of password
	Enabled      bool
	Tags         map[string]string
}

// NewWorkerAuthenticator creates a new worker authenticator.
func NewWorkerAuthenticator() *WorkerAuthenticator {
	return &WorkerAuthenticator{
		workers: make(map[string]*WorkerCredentials),
	}
}

// RegisterWorker adds a new worker with credentials.
func (wa *WorkerAuthenticator) RegisterWorker(workerID, password string, tags map[string]string) error {
	if workerID == "" {
		return fmt.Errorf("worker ID cannot be empty")
	}
	if password == "" {
		return fmt.Errorf("password cannot be empty")
	}

	passwordHash := hashPassword(password)

	wa.mu.Lock()
	defer wa.mu.Unlock()

	if _, exists := wa.workers[workerID]; exists {
		return fmt.Errorf("worker already registered: %s", workerID)
	}

	wa.workers[workerID] = &WorkerCredentials{
		WorkerID:     workerID,
		PasswordHash: passwordHash,
		Enabled:      true,
		Tags:         tags,
	}

	return nil
}

// AuthenticateWorker verifies worker credentials.
func (wa *WorkerAuthenticator) AuthenticateWorker(workerID, password string) (*WorkerCredentials, error) {
	if workerID == "" {
		return nil, fmt.Errorf("worker ID cannot be empty")
	}

	wa.mu.RLock()
	creds, exists := wa.workers[workerID]
	wa.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("worker not found: %s", workerID)
	}

	if !creds.Enabled {
		return nil, fmt.Errorf("worker disabled: %s", workerID)
	}

	if creds.PasswordHash != hashPassword(password) {
		return nil, fmt.Errorf("invalid credentials")
	}

	return creds, nil
}

// DisableWorker disables a worker.
func (wa *WorkerAuthenticator) DisableWorker(workerID string) error {
	wa.mu.Lock()
	defer wa.mu.Unlock()

	creds, exists := wa.workers[workerID]
	if !exists {
		return fmt.Errorf("worker not found: %s", workerID)
	}

	creds.Enabled = false
	return nil
}

// EnableWorker enables a worker.
func (wa *WorkerAuthenticator) EnableWorker(workerID string) error {
	wa.mu.Lock()
	defer wa.mu.Unlock()

	creds, exists := wa.workers[workerID]
	if !exists {
		return fmt.Errorf("worker not found: %s", workerID)
	}

	creds.Enabled = true
	return nil
}

// GetWorkerCredentials returns credentials for a worker.
func (wa *WorkerAuthenticator) GetWorkerCredentials(workerID string) (*WorkerCredentials, error) {
	wa.mu.RLock()
	defer wa.mu.RUnlock()

	creds, exists := wa.workers[workerID]
	if !exists {
		return nil, fmt.Errorf("worker not found: %s", workerID)
	}

	return creds, nil
}

// ListWorkers returns all registered worker IDs.
func (wa *WorkerAuthenticator) ListWorkers() []string {
	wa.mu.RLock()
	defer wa.mu.RUnlock()

	workers := make([]string, 0, len(wa.workers))
	for id := range wa.workers {
		workers = append(workers, id)
	}
	return workers
}

// hashPassword creates a SHA256 hash of the password.
func hashPassword(password string) string {
	hash := sha256.Sum256([]byte(password))
	return hex.EncodeToString(hash[:])
}
