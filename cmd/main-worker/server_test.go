package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"delta-db/api/proto"
	"delta-db/internal/auth"
	"delta-db/pkg/crypto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createTestConfig() *Config {
	key, _ := crypto.GenerateKey(32)
	return &Config{
		GRPCAddr:       ":50051",
		RESTAddr:       ":8080",
		SharedFSPath:   "./testdata",
		MasterKey:      key,
		KeyID:          "test-key-1",
		WorkerTokenTTL: 1 * time.Hour,
		ClientTokenTTL: 24 * time.Hour,
	}
}

func TestNewMainWorkerServer(t *testing.T) {
	t.Run("creates server with valid config", func(t *testing.T) {
		config := createTestConfig()
		server, err := NewMainWorkerServer(config)

		assert.NoError(t, err)
		assert.NotNil(t, server)
		assert.Equal(t, config.KeyID, server.masterKeyID)
		assert.Equal(t, config.MasterKey, server.masterKey)
	})

	t.Run("generates master key if not provided", func(t *testing.T) {
		config := createTestConfig()
		config.MasterKey = nil
		
		server, err := NewMainWorkerServer(config)

		assert.NoError(t, err)
		assert.NotNil(t, server.masterKey)
		assert.Len(t, server.masterKey, 32)
	})

	t.Run("returns error for nil config", func(t *testing.T) {
		server, err := NewMainWorkerServer(nil)

		assert.Error(t, err)
		assert.Nil(t, server)
	})

	t.Run("returns error for invalid master key length", func(t *testing.T) {
		config := createTestConfig()
		config.MasterKey = []byte("short-key")
		
		server, err := NewMainWorkerServer(config)

		assert.Error(t, err)
		assert.Nil(t, server)
		assert.Contains(t, err.Error(), "must be 32 bytes")
	})

	t.Run("uses default key ID if not provided", func(t *testing.T) {
		config := createTestConfig()
		config.KeyID = ""
		
		server, err := NewMainWorkerServer(config)

		assert.NoError(t, err)
		assert.Equal(t, "main-key-v1", server.masterKeyID)
	})
}

func TestSubscribe(t *testing.T) {
	config := createTestConfig()
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("successfully subscribes worker with valid request", func(t *testing.T) {
		// Generate worker key pair
		privKey, pubKey, err := crypto.GenerateRSAKeyPair(2048)
		require.NoError(t, err)

		pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
		require.NoError(t, err)

		req := &proto.SubscribeRequest{
			WorkerId: "worker-1",
			Pubkey:   pubKeyPEM,
			Tags:     map[string]string{"env": "test"},
		}

		resp, err := server.Subscribe(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.NotEmpty(t, resp.Token)
		assert.NotEmpty(t, resp.WrappedKey)
		assert.Equal(t, config.KeyID, resp.KeyId)

		// Verify we can unwrap the key
		unwrappedKey, err := crypto.UnwrapKey(privKey, resp.WrappedKey)
		assert.NoError(t, err)
		assert.Equal(t, server.masterKey, unwrappedKey)

		// Verify token is valid
		token, err := server.tokenManager.ValidateWorkerToken(resp.Token)
		assert.NoError(t, err)
		assert.Equal(t, "worker-1", token.WorkerID)
		assert.Equal(t, config.KeyID, token.KeyID)

		// Verify worker is marked Available in the registry.
		info, exists := server.registry.GetWorker("worker-1")
		assert.True(t, exists)
		assert.Equal(t, "Available", string(info.Status))
	})

	t.Run("returns error for empty worker ID", func(t *testing.T) {
		_, pubKey, err := crypto.GenerateRSAKeyPair(2048)
		require.NoError(t, err)

		pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
		require.NoError(t, err)

		req := &proto.SubscribeRequest{
			WorkerId: "",
			Pubkey:   pubKeyPEM,
		}

		resp, err := server.Subscribe(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("returns error for empty public key", func(t *testing.T) {
		req := &proto.SubscribeRequest{
			WorkerId: "worker-2",
			Pubkey:   nil,
		}

		resp, err := server.Subscribe(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("returns error for invalid public key format", func(t *testing.T) {
		req := &proto.SubscribeRequest{
			WorkerId: "worker-3",
			Pubkey:   []byte("invalid-key"),
		}

		resp, err := server.Subscribe(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("handles multiple subscriptions from same worker", func(t *testing.T) {
		_, pubKey, err := crypto.GenerateRSAKeyPair(2048)
		require.NoError(t, err)

		pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
		require.NoError(t, err)

		req := &proto.SubscribeRequest{
			WorkerId: "worker-multi",
			Pubkey:   pubKeyPEM,
		}

		// First subscription
		resp1, err := server.Subscribe(context.Background(), req)
		require.NoError(t, err)

		// Second subscription (should succeed and return new token)
		resp2, err := server.Subscribe(context.Background(), req)
		require.NoError(t, err)

		// Tokens should be different
		assert.NotEqual(t, resp1.Token, resp2.Token)
		
		// Both tokens should be valid
		_, err = server.tokenManager.ValidateWorkerToken(resp1.Token)
		assert.NoError(t, err)
		_, err = server.tokenManager.ValidateWorkerToken(resp2.Token)
		assert.NoError(t, err)
	})

	t.Run("preserves worker tags", func(t *testing.T) {
		_, pubKey, err := crypto.GenerateRSAKeyPair(2048)
		require.NoError(t, err)

		pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
		require.NoError(t, err)

		tags := map[string]string{
			"region": "us-west",
			"env":    "production",
			"role":   "processor",
		}

		req := &proto.SubscribeRequest{
			WorkerId: "worker-tagged",
			Pubkey:   pubKeyPEM,
			Tags:     tags,
		}

		resp, err := server.Subscribe(context.Background(), req)
		require.NoError(t, err)

		// Verify tags are stored with token
		token, err := server.tokenManager.ValidateWorkerToken(resp.Token)
		require.NoError(t, err)
		assert.Equal(t, tags, token.Tags)
	})
}

func TestProcess(t *testing.T) {
	config := createTestConfig()
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("returns error for empty token", func(t *testing.T) {
		req := &proto.ProcessRequest{
			DatabaseName: "testdb",
			EntityKey:    "test-entity",
			Operation:    "GET",
		}

		resp, err := server.Process(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("returns error for invalid token", func(t *testing.T) {
		req := &proto.ProcessRequest{
			DatabaseName: "testdb",
			EntityKey:    "test-entity",
			Operation:    "GET",
			Token:        "invalid-token",
		}

		resp, err := server.Process(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("returns not_found for missing entity with valid token", func(t *testing.T) {
		// Generate a valid token first
		token, err := server.tokenManager.GenerateWorkerToken("worker-1", "key-1", nil)
		require.NoError(t, err)

		req := &proto.ProcessRequest{
			DatabaseName: "testdb",
			EntityKey:    "test-entity",
			Operation:    "GET",
			Token:        token.Token,
		}

		resp, err := server.Process(context.Background(), req)

		// No proc-worker registered and entity not in entity store → NotFound.
		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})
}

func TestRegisterWorker(t *testing.T) {
	config := createTestConfig()
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("registers new worker", func(t *testing.T) {
		tags := map[string]string{"env": "test"}
		err := server.RegisterWorker("worker-manual", "password123", tags)

		assert.NoError(t, err)
		assert.True(t, server.isWorkerRegistered("worker-manual"))
	})

	t.Run("returns error for duplicate worker", func(t *testing.T) {
		err := server.RegisterWorker("worker-dup", "pass", nil)
		require.NoError(t, err)

		err = server.RegisterWorker("worker-dup", "pass", nil)
		assert.Error(t, err)
	})
}

func TestIsWorkerRegistered(t *testing.T) {
	config := createTestConfig()
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("returns false for unregistered worker", func(t *testing.T) {
		assert.False(t, server.isWorkerRegistered("non-existent"))
	})

	t.Run("returns true for registered worker", func(t *testing.T) {
		server.RegisterWorker("worker-check", "pass", nil)
		assert.True(t, server.isWorkerRegistered("worker-check"))
	})
}

func TestGetStats(t *testing.T) {
	config := createTestConfig()
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("returns initial stats", func(t *testing.T) {
		stats := server.GetStats()

		assert.Equal(t, 0, stats["active_worker_tokens"])
		assert.Equal(t, 0, stats["active_client_tokens"])
		assert.Equal(t, 0, stats["registered_workers"])
		assert.Equal(t, config.KeyID, stats["master_key_id"])
	})

	t.Run("reflects changes in stats", func(t *testing.T) {
		// Generate some tokens
		server.tokenManager.GenerateWorkerToken("w1", "key", nil)
		server.tokenManager.GenerateClientToken("c1", nil)
		server.RegisterWorker("worker-stat", "pass", nil)

		stats := server.GetStats()

		assert.Equal(t, 1, stats["active_worker_tokens"])
		assert.Equal(t, 1, stats["active_client_tokens"])
		assert.Equal(t, 1, stats["registered_workers"])
	})
}

func TestGenerateTestKeyPair(t *testing.T) {
	t.Run("generates valid key pair", func(t *testing.T) {
		privKey, pubKey, err := GenerateTestKeyPair()

		assert.NoError(t, err)
		assert.NotNil(t, privKey)
		assert.NotNil(t, pubKey)
		assert.Equal(t, 2048, privKey.N.BitLen())
	})
}

// createTestConfigWithTempDir creates a test config that uses t.TempDir() so
// that the schema validator can be fully initialized without side-effects.
func createTestConfigWithTempDir(t *testing.T) *Config {
	t.Helper()
	key, _ := crypto.GenerateKey(32)
	return &Config{
		GRPCAddr:       ":50051",
		RESTAddr:       ":8080",
		SharedFSPath:   t.TempDir(),
		MasterKey:      key,
		KeyID:          "test-key-1",
		WorkerTokenTTL: 1 * time.Hour,
		ClientTokenTTL: 24 * time.Hour,
	}
}

func TestHandleAdminSchemas(t *testing.T) {
	config := createTestConfigWithTempDir(t)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)
	require.NotNil(t, server.validator, "validator should be initialized when SharedFSPath is set")

	t.Run("returns empty list when no schemas exist", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/schemas", nil)
		w := httptest.NewRecorder()
		server.handleAdminSchemas(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var schemas []string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &schemas))
		assert.Empty(t, schemas)
	})

	t.Run("rejects non-GET methods", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/schemas", nil)
		w := httptest.NewRecorder()
		server.handleAdminSchemas(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestHandleSchema(t *testing.T) {
	config := createTestConfigWithTempDir(t)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)
	require.NotNil(t, server.validator)

	// Generate a real client token that passes ValidateClientToken.
	ct, err := server.tokenManager.GenerateClientToken("test-client", []string{"read", "write"})
	require.NoError(t, err)
	authHeader := "Bearer " + ct.Token

	schemaJSON := `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`

	t.Run("PUT requires Authorization header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/schema/myschema.v1",
			strings.NewReader(schemaJSON))
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("PUT with valid client token saves schema and returns ok", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/schema/myschema.v1",
			strings.NewReader(schemaJSON))
		req.Header.Set("Authorization", authHeader)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "ok", resp["status"])
	})

	t.Run("GET returns saved schema", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/schema/myschema.v1", nil)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		// Body must be valid JSON matching what was saved.
		var got interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	})

	t.Run("GET returns 404 for missing schema", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/schema/does-not-exist.v1", nil)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("PUT rejects invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/schema/bad.v1",
			strings.NewReader("{not-json}"))
		req.Header.Set("Authorization", authHeader)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("PUT returns 400 for missing schema id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/schema/",
			strings.NewReader(schemaJSON))
		req.Header.Set("Authorization", authHeader)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("admin schemas lists saved schema", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/schemas", nil)
		w := httptest.NewRecorder()
		server.handleAdminSchemas(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var schemas []string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &schemas))
		assert.Contains(t, schemas, "myschema.v1")
	})
}

func TestConcurrentSubscriptions(t *testing.T) {
	config := createTestConfig()
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("handles concurrent subscriptions", func(t *testing.T) {
		numWorkers := 10
		results := make(chan error, numWorkers)

		for i := 0; i < numWorkers; i++ {
			go func(id int) {
				privKey, pubKey, err := crypto.GenerateRSAKeyPair(2048)
				if err != nil {
					results <- err
					return
				}

				pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
				if err != nil {
					results <- err
					return
				}

				req := &proto.SubscribeRequest{
					WorkerId: string(rune('a' + id)),
					Pubkey:   pubKeyPEM,
				}

				resp, err := server.Subscribe(context.Background(), req)
				if err != nil {
					results <- err
					return
				}

				// Verify key unwrapping
				_, err = crypto.UnwrapKey(privKey, resp.WrappedKey)
				results <- err
			}(i)
		}

		// Collect results
		for i := 0; i < numWorkers; i++ {
			err := <-results
			assert.NoError(t, err)
		}
	})
}

// ── Helper ──────────────────────────────────────────────────────────────────

// createTestConfigWithAdminKey creates a test config with a pre-set admin key.
func createTestConfigWithAdminKey(t *testing.T, adminKey string) *Config {
	t.Helper()
	key, _ := crypto.GenerateKey(32)
	return &Config{
		GRPCAddr:       ":0",
		RESTAddr:       ":0",
		SharedFSPath:   t.TempDir(),
		MasterKey:      key,
		KeyID:          "test-key-1",
		WorkerTokenTTL: 1 * time.Hour,
		ClientTokenTTL: 24 * time.Hour,
		AdminKey:       adminKey,
	}
}

// adminBearer returns an "Authorization: Bearer <adminKey>" header value.
func adminBearer(adminKey string) string { return "Bearer " + adminKey }

// ── TestHandleAPIKeys ────────────────────────────────────────────────────────

func TestHandleAPIKeys(t *testing.T) {
	const adminKey = "test-admin-key"
	config := createTestConfigWithAdminKey(t, adminKey)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("GET lists empty keys initially", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var keys []map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &keys))
		assert.Empty(t, keys)
	})

	t.Run("GET returns 200 empty array without auth when no keys exist", func(t *testing.T) {
		// Fresh server with zero keys.  Even without an Authorization header the
		// endpoint must return 200 [] so the browser UI shows "No API keys found."
		// rather than "Failed to load keys: 401/403".
		fresh, err := NewMainWorkerServer(createTestConfigWithAdminKey(t, adminKey))
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		w := httptest.NewRecorder()
		fresh.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		assert.JSONEq(t, "[]", strings.TrimSpace(w.Body.String()))
	})

	t.Run("GET requires admin permission", func(t *testing.T) {
		// Create a read-only key.
		secret, _, _ := server.keyManager.CreateKey("readonly", []auth.Permission{auth.PermRead}, nil)
		req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("GET unauthorized without token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("POST creates key and returns secret", func(t *testing.T) {
		body, _ := json.Marshal(createKeyRequest{
			Name:        "my-api-key",
			Permissions: []auth.Permission{auth.PermRead, auth.PermWrite},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
		req.Header.Set("Authorization", adminBearer(adminKey))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp createKeyResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.NotEmpty(t, resp.Secret)
		assert.True(t, len(resp.Secret) > 5 && resp.Secret[:3] == "dk_")
		assert.Equal(t, "my-api-key", resp.Name)
		assert.Nil(t, resp.ExpiresAt)
	})

	t.Run("POST creates key with expiry", func(t *testing.T) {
		body, _ := json.Marshal(createKeyRequest{
			Name:        "expiring-key",
			Permissions: []auth.Permission{auth.PermRead},
			ExpiresIn:   "24h",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp createKeyResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		require.NotNil(t, resp.ExpiresAt)
		assert.True(t, resp.ExpiresAt.After(time.Now()))
	})

	t.Run("POST creates key with days expiry", func(t *testing.T) {
		body, _ := json.Marshal(createKeyRequest{
			Name:        "week-key",
			Permissions: []auth.Permission{auth.PermRead},
			ExpiresIn:   "7d",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
		var resp createKeyResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		require.NotNil(t, resp.ExpiresAt)
		// Should expire roughly 7 days from now.
		assert.True(t, resp.ExpiresAt.After(time.Now().Add(6*24*time.Hour)))
	})

	t.Run("POST rejects missing name", func(t *testing.T) {
		body, _ := json.Marshal(createKeyRequest{Permissions: []auth.Permission{auth.PermRead}})
		req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("POST rejects empty permissions", func(t *testing.T) {
		body, _ := json.Marshal(createKeyRequest{Name: "no-perms"})
		req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("POST rejects invalid expires_in", func(t *testing.T) {
		body, _ := json.Marshal(createKeyRequest{
			Name:        "bad-expiry",
			Permissions: []auth.Permission{auth.PermRead},
			ExpiresIn:   "notaduration",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("GET lists created keys", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var keys []map[string]interface{}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &keys))
		assert.NotEmpty(t, keys)
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "/api/keys", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

// ── TestHandleAPIKeyByID ─────────────────────────────────────────────────────

func TestHandleAPIKeyByID(t *testing.T) {
	const adminKey = "test-admin-key"
	config := createTestConfigWithAdminKey(t, adminKey)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	// Pre-create a key.
	_, createdKey, err := server.keyManager.CreateKey("deletable", []auth.Permission{auth.PermRead}, nil)
	require.NoError(t, err)

	t.Run("DELETE removes key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/keys/"+createdKey.ID, nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeyByID(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, 0, server.keyManager.Count())
	})

	t.Run("DELETE returns 404 for missing key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/keys/nonexistent", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeyByID(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("DELETE requires admin permission", func(t *testing.T) {
		// Create a key to try to delete.
		_, k, _ := server.keyManager.CreateKey("victim", []auth.Permission{auth.PermRead}, nil)
		// Try to delete with a read-only key.
		roSecret, _, _ := server.keyManager.CreateKey("ro", []auth.Permission{auth.PermRead}, nil)

		req := httptest.NewRequest(http.MethodDelete, "/api/keys/"+k.ID, nil)
		req.Header.Set("Authorization", "Bearer "+roSecret)
		w := httptest.NewRecorder()
		server.handleAPIKeyByID(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("method not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/keys/someid", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAPIKeyByID(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

// ── TestAdminKeyAuth ─────────────────────────────────────────────────────────

// TestAdminKeyAuth verifies that the admin key and API keys work directly as
// Bearer tokens — exactly like a Postgres password or MinIO access key.
// No call to POST /api/login is required.
func TestAdminKeyAuth(t *testing.T) {
	const adminKey = "my-super-secret"
	config := createTestConfigWithAdminKey(t, adminKey)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("admin key grants full access to entity PUT without login", func(t *testing.T) {
		// Use the raw admin key directly — no session token, no /api/login.
		body := bytes.NewReader([]byte(`{"testkey": "testvalue"}`))
		req := httptest.NewRequest(http.MethodPut, "/entity/mydb", body)
		req.Header.Set("Authorization", "Bearer "+adminKey)
		w := httptest.NewRecorder()
		server.handleEntity(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("admin key grants full access to entity GET without login", func(t *testing.T) {
		// Seed a value first.
		putBody := bytes.NewReader([]byte(`{"directget": "hi"}`))
		putReq := httptest.NewRequest(http.MethodPut, "/entity/mydb", putBody)
		putReq.Header.Set("Authorization", "Bearer "+adminKey)
		server.handleEntity(httptest.NewRecorder(), putReq)

		// Now GET it — still with the raw admin key, no session.
		getReq := httptest.NewRequest(http.MethodGet, "/entity/mydb?key=directget", nil)
		getReq.Header.Set("Authorization", "Bearer "+adminKey)
		getW := httptest.NewRecorder()
		server.handleEntity(getW, getReq)

		assert.Equal(t, http.StatusOK, getW.Code)
	})

	t.Run("wrong admin key is rejected", func(t *testing.T) {
		body := bytes.NewReader([]byte(`{"k": "v"}`))
		req := httptest.NewRequest(http.MethodPut, "/entity/mydb", body)
		req.Header.Set("Authorization", "Bearer wrongkey")
		w := httptest.NewRecorder()
		server.handleEntity(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("read-only API key is rejected for entity PUT", func(t *testing.T) {
		secret, _, _ := server.keyManager.CreateKey("ro", []auth.Permission{auth.PermRead}, nil)
		body := bytes.NewReader([]byte(`{"k": "v"}`))
		req := httptest.NewRequest(http.MethodPut, "/entity/mydb", body)
		req.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		server.handleEntity(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("write API key works directly for entity PUT without login", func(t *testing.T) {
		// Create an API key with write permission.
		secret, _, _ := server.keyManager.CreateKey("rw", []auth.Permission{auth.PermRead, auth.PermWrite}, nil)
		// Use the raw API key secret directly — no session token.
		body := bytes.NewReader([]byte(`{"k": "v"}`))
		req := httptest.NewRequest(http.MethodPut, "/entity/mydb", body)
		req.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		server.handleEntity(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("admin key manages API keys directly without login", func(t *testing.T) {
		// Create a key using the raw admin key — no session token.
		body, _ := json.Marshal(createKeyRequest{
			Name:        "direct-create",
			Permissions: []auth.Permission{auth.PermRead},
		})
		req := httptest.NewRequest(http.MethodPost, "/api/keys", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+adminKey)
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
	})
}

// TestSessionTokenInheritsPermissions verifies that session tokens issued by
// POST /api/login carry the correct permissions of the authenticating credential.
// Previously, session tokens were always restricted to read+write regardless of
// the login credential — which broke admin access in the Management UI.
func TestSessionTokenInheritsPermissions(t *testing.T) {
	const adminKey = "test-admin-key-inherit"
	config := createTestConfigWithAdminKey(t, adminKey)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	// Obtain a session token via POST /api/login with the admin key.
	loginBody, _ := json.Marshal(map[string]string{"key": adminKey})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	loginW := httptest.NewRecorder()
	server.handleLogin(loginW, loginReq)
	require.Equal(t, http.StatusOK, loginW.Code)

	var loginResp loginResponse
	require.NoError(t, json.NewDecoder(loginW.Body).Decode(&loginResp))
	sessionToken := loginResp.Token
	require.NotEmpty(t, sessionToken)

	t.Run("session token from admin login can list API keys (admin endpoint)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		req.Header.Set("Authorization", "Bearer "+sessionToken)
		w := httptest.NewRecorder()
		server.handleAPIKeys(w, req)
		// Should be 200 (admin permission granted via session token), not 403.
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("session token from admin login can list workers (admin endpoint)", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/workers", nil)
		req.Header.Set("Authorization", "Bearer "+sessionToken)
		w := httptest.NewRecorder()
		server.handleAdminWorkers(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("session token from read-only API key is rejected for admin endpoint", func(t *testing.T) {
		// Create a read-only API key.
		secret, _, _ := server.keyManager.CreateKey("ro-key", []auth.Permission{auth.PermRead}, nil)

		// Log in with it.
		body2, _ := json.Marshal(map[string]string{"key": secret})
		req2 := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body2))
		req2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		server.handleLogin(w2, req2)
		require.Equal(t, http.StatusOK, w2.Code)

		var resp2 loginResponse
		require.NoError(t, json.NewDecoder(w2.Body).Decode(&resp2))

		// Session token from a read-only key must be rejected for admin endpoints.
		req3 := httptest.NewRequest(http.MethodGet, "/api/keys", nil)
		req3.Header.Set("Authorization", "Bearer "+resp2.Token)
		w3 := httptest.NewRecorder()
		server.handleAPIKeys(w3, req3)
		assert.Equal(t, http.StatusForbidden, w3.Code)
	})
}
