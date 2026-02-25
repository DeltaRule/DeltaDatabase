package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"delta-db/internal/auth"
	"delta-db/pkg/crypto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createFrontendTestServer(t *testing.T) *MainWorkerServer {
	t.Helper()
	key, _ := crypto.GenerateKey(32)
	srv, err := NewMainWorkerServer(&Config{
		GRPCAddr:       ":0",
		RESTAddr:       ":0",
		SharedFSPath:   "./testdata",
		MasterKey:      key,
		KeyID:          "test-key-1",
		WorkerTokenTTL: 1 * time.Hour,
		ClientTokenTTL: 24 * time.Hour,
	})
	require.NoError(t, err)
	return srv
}

func createFrontendTestServerWithAdminKey(t *testing.T, adminKey string) *MainWorkerServer {
	t.Helper()
	key, _ := crypto.GenerateKey(32)
	srv, err := NewMainWorkerServer(&Config{
		GRPCAddr:       ":0",
		RESTAddr:       ":0",
		SharedFSPath:   t.TempDir(),
		MasterKey:      key,
		KeyID:          "test-key-1",
		WorkerTokenTTL: 1 * time.Hour,
		ClientTokenTTL: 24 * time.Hour,
		AdminKey:       adminKey,
	})
	require.NoError(t, err)
	return srv
}

func TestHandleLogin(t *testing.T) {
	t.Run("dev mode: issues token for valid client_id when no admin key", func(t *testing.T) {
		srv := createFrontendTestServer(t) // no admin key → dev mode
		body, _ := json.Marshal(map[string]string{"client_id": "testclient"})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp loginResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, "testclient", resp.ClientID)
		assert.True(t, resp.ExpiresAt.After(time.Now()))
	})

	t.Run("admin key mode: issues token with admin permissions", func(t *testing.T) {
		srv := createFrontendTestServerWithAdminKey(t, "super-secret-admin")
		body, _ := json.Marshal(map[string]string{"key": "super-secret-admin"})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp loginResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, "admin", resp.ClientID)
		assert.Contains(t, resp.Permissions, auth.PermAdmin)
	})

	t.Run("API key: issues token with key permissions", func(t *testing.T) {
		srv := createFrontendTestServerWithAdminKey(t, "admin-key")
		// Create an API key with read permission.
		secret, _, err := srv.keyManager.CreateKey("reader", []auth.Permission{auth.PermRead}, nil)
		require.NoError(t, err)

		body, _ := json.Marshal(map[string]string{"key": secret})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp loginResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.Token)
		assert.Equal(t, "reader", resp.ClientID)
		assert.Contains(t, resp.Permissions, auth.PermRead)
		assert.NotContains(t, resp.Permissions, auth.PermAdmin)
	})

	t.Run("admin key mode: rejects invalid key", func(t *testing.T) {
		srv := createFrontendTestServerWithAdminKey(t, "super-secret-admin")
		body, _ := json.Marshal(map[string]string{"key": "wrong-key"})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("admin key mode: rejects missing key", func(t *testing.T) {
		srv := createFrontendTestServerWithAdminKey(t, "super-secret-admin")
		body, _ := json.Marshal(map[string]string{})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("dev mode: rejects missing client_id", func(t *testing.T) {
		srv := createFrontendTestServer(t)
		body, _ := json.Marshal(map[string]string{})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects wrong HTTP method", func(t *testing.T) {
		srv := createFrontendTestServer(t)
		req := httptest.NewRequest(http.MethodGet, "/api/login", nil)
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestServeFrontend(t *testing.T) {
	srv := createFrontendTestServer(t)
	mux := http.NewServeMux()
	srv.registerFrontendRoutes(mux)

	t.Run("serves index.html at root", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "DeltaDatabase")
	})

	t.Run("login endpoint registered", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{"client_id": "admin"})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})
}
