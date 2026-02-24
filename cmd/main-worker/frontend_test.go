package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func TestHandleLogin(t *testing.T) {
	srv := createFrontendTestServer(t)

	t.Run("issues token for valid client_id", func(t *testing.T) {
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

	t.Run("rejects missing client_id", func(t *testing.T) {
		body, _ := json.Marshal(map[string]string{})
		req := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		srv.handleLogin(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("rejects wrong HTTP method", func(t *testing.T) {
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
