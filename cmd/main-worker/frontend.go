package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"
)

//go:embed static
var staticFiles embed.FS

// loginRequest is the JSON body expected by POST /api/login.
type loginRequest struct {
	ClientID string `json:"client_id"`
}

// loginResponse is the JSON body returned by POST /api/login.
type loginResponse struct {
	Token     string    `json:"token"`
	ClientID  string    `json:"client_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// handleLogin issues a client token given a client_id.
func (s *MainWorkerServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ClientID == "" {
		http.Error(w, `{"error":"client_id is required"}`, http.StatusBadRequest)
		return
	}

	ct, err := s.tokenManager.GenerateClientToken(req.ClientID, []string{"read", "write"})
	if err != nil {
		log.Printf("Failed to generate client token for %s: %v", req.ClientID, err)
		http.Error(w, `{"error":"failed to generate token"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(loginResponse{
		Token:     ct.Token,
		ClientID:  ct.ClientID,
		ExpiresAt: ct.ExpiresAt,
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
