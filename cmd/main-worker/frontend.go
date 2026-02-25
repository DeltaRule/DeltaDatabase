package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"

	"delta-db/internal/auth"
)

//go:embed static
var staticFiles embed.FS

// loginRequest is the JSON body expected by POST /api/login.
type loginRequest struct {
	// Key is an API key or admin key used to authenticate.
	// When provided, a short-lived session token is issued.
	Key string `json:"key"`
	// ClientID is kept for backwards-compatibility.  When Key is absent and
	// ClientID is present, a session token is issued without key validation
	// (only when no admin key is configured — dev mode only).
	ClientID string `json:"client_id,omitempty"`
}

// loginResponse is the JSON body returned by POST /api/login.
type loginResponse struct {
	Token     string           `json:"token"`
	ClientID  string           `json:"client_id"`
	ExpiresAt time.Time        `json:"expires_at"`
	Permissions []auth.Permission `json:"permissions,omitempty"`
}

// handleLogin issues a short-lived client session token.
//
// Authentication priority:
//  1. If body.Key matches the admin key → issue token with all permissions.
//  2. If body.Key matches a valid API key → issue token with the key's permissions.
//  3. If no admin key is configured and body.ClientID is provided (dev mode) →
//     issue token with read+write for backwards compatibility.
func (s *MainWorkerServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad_json"}`, http.StatusBadRequest)
		return
	}

	var (
		clientID  string
		roles     []string
		perms     []auth.Permission
	)

	if req.Key != "" {
		// Attempt admin key authentication first.
		if s.adminKeyHash != "" && hashAdminKey(req.Key) == s.adminKeyHash {
			clientID = "admin"
			perms = auth.AllPermissions
			roles = []string{"admin"}
		} else {
			// Validate against the persistent API key store.
			apiKey, err := s.keyManager.ValidateKey(req.Key)
			if err != nil {
				http.Error(w, `{"error":"invalid or expired key"}`, http.StatusUnauthorized)
				return
			}
			clientID = apiKey.Name
			perms = apiKey.Permissions
			for _, p := range perms {
				roles = append(roles, string(p))
			}
		}
	} else if req.ClientID != "" && s.adminKeyHash == "" {
		// Dev / backwards-compatibility mode: no admin key configured.
		clientID = req.ClientID
		perms = []auth.Permission{auth.PermRead, auth.PermWrite}
		roles = []string{"read", "write"}
	} else {
		http.Error(w, `{"error":"key is required"}`, http.StatusBadRequest)
		return
	}

	ct, err := s.tokenManager.GenerateClientToken(clientID, roles)
	if err != nil {
		log.Printf("Failed to generate client token for %s: %v", clientID, err)
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(loginResponse{
		Token:       ct.Token,
		ClientID:    ct.ClientID,
		ExpiresAt:   ct.ExpiresAt,
		Permissions: perms,
	}); err != nil {
		log.Printf("Failed to encode login response: %v", err)
	}
}

// registerFrontendRoutes mounts the embedded static UI and the login endpoint
// on the provided ServeMux.
func (s *MainWorkerServer) registerFrontendRoutes(mux *http.ServeMux) {
	// Serve the single-page app at /
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Printf("Warning: could not prepare static file system: %v", err)
		return
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	// Login endpoint used by the SPA to obtain a Bearer token
	mux.HandleFunc("/api/login", s.handleLogin)
}
