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
			SchemaId:     "testdb",
			EntityKey:    "test-entity",
			Operation:    "GET",
		}

		resp, err := server.Process(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})

	t.Run("returns error for invalid token", func(t *testing.T) {
		req := &proto.ProcessRequest{
			SchemaId:     "testdb",
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
			SchemaId:     "testdb",
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

func TestHandleSchemaDelete(t *testing.T) {
	config := createTestConfigWithTempDir(t)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)
	require.NotNil(t, server.validator)

	ct, err := server.tokenManager.GenerateClientToken("test-client", []string{"read", "write"})
	require.NoError(t, err)
	authHeader := "Bearer " + ct.Token

	schemaJSON := `{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}`

	// Pre-populate a schema to delete in these tests.
	putReq := httptest.NewRequest(http.MethodPut, "/schema/todelete.v1", strings.NewReader(schemaJSON))
	putReq.Header.Set("Authorization", authHeader)
	putW := httptest.NewRecorder()
	server.handleSchema(putW, putReq)
	require.Equal(t, http.StatusOK, putW.Code)

	t.Run("DELETE requires Authorization header", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/schema/todelete.v1", nil)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("DELETE with write token removes schema", func(t *testing.T) {
		// Re-create schema so we can delete it.
		putReq2 := httptest.NewRequest(http.MethodPut, "/schema/todelete.v1", strings.NewReader(schemaJSON))
		putReq2.Header.Set("Authorization", authHeader)
		putW2 := httptest.NewRecorder()
		server.handleSchema(putW2, putReq2)
		require.Equal(t, http.StatusOK, putW2.Code)

		req := httptest.NewRequest(http.MethodDelete, "/schema/todelete.v1", nil)
		req.Header.Set("Authorization", authHeader)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var resp map[string]string
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "ok", resp["status"])
	})

	t.Run("GET returns 404 after DELETE", func(t *testing.T) {
		// Ensure schema is gone.
		req := httptest.NewRequest(http.MethodGet, "/schema/todelete.v1", nil)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("DELETE non-existent schema returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/schema/ghost.v99", nil)
		req.Header.Set("Authorization", authHeader)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("DELETE with read-only token is forbidden", func(t *testing.T) {
		readCT, err := server.tokenManager.GenerateClientToken("reader", []string{"read"})
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodDelete, "/schema/todelete.v1", nil)
		req.Header.Set("Authorization", "Bearer "+readCT.Token)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("DELETE rejects path traversal schema id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/schema/../etc/passwd", nil)
		req.Header.Set("Authorization", authHeader)
		w := httptest.NewRecorder()
		server.handleSchema(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("admin schemas list excludes deleted schema", func(t *testing.T) {
		// Put a schema, then delete it, then verify it is absent.
		putReq3 := httptest.NewRequest(http.MethodPut, "/schema/listed.v1", strings.NewReader(schemaJSON))
		putReq3.Header.Set("Authorization", authHeader)
		putW3 := httptest.NewRecorder()
		server.handleSchema(putW3, putReq3)
		require.Equal(t, http.StatusOK, putW3.Code)

		delReq := httptest.NewRequest(http.MethodDelete, "/schema/listed.v1", nil)
		delReq.Header.Set("Authorization", authHeader)
		delW := httptest.NewRecorder()
		server.handleSchema(delW, delReq)
		require.Equal(t, http.StatusOK, delW.Code)

		listReq := httptest.NewRequest(http.MethodGet, "/admin/schemas", nil)
		listW := httptest.NewRecorder()
		server.handleAdminSchemas(listW, listReq)
		require.Equal(t, http.StatusOK, listW.Code)
		var schemas []string
		require.NoError(t, json.Unmarshal(listW.Body.Bytes(), &schemas))
		assert.NotContains(t, schemas, "listed.v1")
	})
}

func TestProcessSchemaOperations(t *testing.T) {
	config := createTestConfigWithTempDir(t)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)
	require.NotNil(t, server.validator)

	// Get a valid worker token for Process calls.
	_, pubKey, err := crypto.GenerateRSAKeyPair(2048)
	require.NoError(t, err)
	pubPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
	require.NoError(t, err)
	subResp, err := server.Subscribe(context.Background(), &proto.SubscribeRequest{
		WorkerId: "test-worker",
		Pubkey:   pubPEM,
	})
	require.NoError(t, err)
	workerToken := subResp.Token

	schemaJSON := []byte(`{"type":"object","properties":{"value":{"type":"number"}},"required":["value"]}`)

	t.Run("SCHEMA_PUT saves schema", func(t *testing.T) {
		resp, err := server.Process(context.Background(), &proto.ProcessRequest{
			Operation: "SCHEMA_PUT",
			SchemaId:  "grpc.v1",
			Payload:   schemaJSON,
			Token:     workerToken,
		})
		require.NoError(t, err)
		assert.Equal(t, "OK", resp.Status)
	})

	t.Run("SCHEMA_GET retrieves saved schema", func(t *testing.T) {
		resp, err := server.Process(context.Background(), &proto.ProcessRequest{
			Operation: "SCHEMA_GET",
			SchemaId:  "grpc.v1",
			Token:     workerToken,
		})
		require.NoError(t, err)
		assert.Equal(t, "OK", resp.Status)
		assert.NotEmpty(t, resp.Result)
	})

	t.Run("SCHEMA_DELETE removes schema", func(t *testing.T) {
		resp, err := server.Process(context.Background(), &proto.ProcessRequest{
			Operation: "SCHEMA_DELETE",
			SchemaId:  "grpc.v1",
			Token:     workerToken,
		})
		require.NoError(t, err)
		assert.Equal(t, "OK", resp.Status)
	})

	t.Run("SCHEMA_GET returns NotFound after delete", func(t *testing.T) {
		_, err := server.Process(context.Background(), &proto.ProcessRequest{
			Operation: "SCHEMA_GET",
			SchemaId:  "grpc.v1",
			Token:     workerToken,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotFound")
	})

	t.Run("SCHEMA_DELETE non-existent schema returns NotFound", func(t *testing.T) {
		_, err := server.Process(context.Background(), &proto.ProcessRequest{
			Operation: "SCHEMA_DELETE",
			SchemaId:  "ghost.v99",
			Token:     workerToken,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NotFound")
	})

	t.Run("unknown operation returns InvalidArgument", func(t *testing.T) {
		_, err := server.Process(context.Background(), &proto.ProcessRequest{
			Operation: "BOGUS",
			SchemaId:  "any",
			Token:     workerToken,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "InvalidArgument")
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

// TestHandleSchemas tests the GET /api/schemas endpoint.
func TestHandleSchemas(t *testing.T) {
config := createTestConfigWithTempDir(t)
server, err := NewMainWorkerServer(config)
require.NoError(t, err)

ct, err := server.tokenManager.GenerateClientToken("test-client", []string{"read"})
require.NoError(t, err)
authHeader := "Bearer " + ct.Token

t.Run("returns empty list when no entities cached", func(t *testing.T) {
req := httptest.NewRequest(http.MethodGet, "/api/schemas", nil)
req.Header.Set("Authorization", authHeader)
w := httptest.NewRecorder()

server.handleSchemas(w, req)

assert.Equal(t, http.StatusOK, w.Code)
var schemas []string
require.NoError(t, json.NewDecoder(w.Body).Decode(&schemas))
assert.Empty(t, schemas)
})

t.Run("returns schema IDs after entities are cached", func(t *testing.T) {
// Seed two schemas via the entity store.
server.entityStore.Set("alpha/k1", []byte(`{}`), "1")
server.entityStore.Set("alpha/k2", []byte(`{}`), "1")
server.entityStore.Set("beta/k1", []byte(`{}`), "1")

req := httptest.NewRequest(http.MethodGet, "/api/schemas", nil)
req.Header.Set("Authorization", authHeader)
w := httptest.NewRecorder()

server.handleSchemas(w, req)

assert.Equal(t, http.StatusOK, w.Code)
var schemas []string
require.NoError(t, json.NewDecoder(w.Body).Decode(&schemas))
assert.Contains(t, schemas, "alpha")
assert.Contains(t, schemas, "beta")
})

t.Run("returns sorted list", func(t *testing.T) {
server.entityStore.Set("zz/k1", []byte(`{}`), "1")
server.entityStore.Set("aa/k1", []byte(`{}`), "1")

req := httptest.NewRequest(http.MethodGet, "/api/schemas", nil)
req.Header.Set("Authorization", authHeader)
w := httptest.NewRecorder()

server.handleSchemas(w, req)

assert.Equal(t, http.StatusOK, w.Code)
var schemas []string
require.NoError(t, json.NewDecoder(w.Body).Decode(&schemas))
// Verify sorted (first element <= last element for any list size ≥ 2).
if len(schemas) >= 2 {
for i := 1; i < len(schemas); i++ {
assert.LessOrEqual(t, schemas[i-1], schemas[i], "schemas should be sorted")
}
}
})

t.Run("rejects unauthenticated request", func(t *testing.T) {
req := httptest.NewRequest(http.MethodGet, "/api/schemas", nil)
w := httptest.NewRecorder()

server.handleSchemas(w, req)

assert.Equal(t, http.StatusUnauthorized, w.Code)
})

t.Run("rejects POST method", func(t *testing.T) {
req := httptest.NewRequest(http.MethodPost, "/api/schemas", nil)
req.Header.Set("Authorization", authHeader)
w := httptest.NewRecorder()

server.handleSchemas(w, req)

assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
})
}

// TestHandleMe tests the GET /api/me endpoint.
func TestHandleMe(t *testing.T) {
const adminKey = "test-admin-me"
config := createTestConfigWithAdminKey(t, adminKey)
server, err := NewMainWorkerServer(config)
require.NoError(t, err)

t.Run("returns admin identity for admin key", func(t *testing.T) {
req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
req.Header.Set("Authorization", "Bearer "+adminKey)
w := httptest.NewRecorder()

server.handleMe(w, req)

assert.Equal(t, http.StatusOK, w.Code)
var resp meResponse
require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
assert.Equal(t, "admin", resp.ClientID)
assert.True(t, resp.IsAdmin)
assert.Contains(t, resp.Permissions, auth.PermAdmin)
})

t.Run("returns client identity for session token", func(t *testing.T) {
ct, err := server.tokenManager.GenerateClientToken("myapp", []string{"read", "write"})
require.NoError(t, err)

req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
req.Header.Set("Authorization", "Bearer "+ct.Token)
w := httptest.NewRecorder()

server.handleMe(w, req)

assert.Equal(t, http.StatusOK, w.Code)
var resp meResponse
require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
assert.Equal(t, "myapp", resp.ClientID)
assert.False(t, resp.IsAdmin)
})

t.Run("returns 401 without token", func(t *testing.T) {
req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
w := httptest.NewRecorder()

server.handleMe(w, req)

assert.Equal(t, http.StatusUnauthorized, w.Code)
})

t.Run("returns 401 for invalid token", func(t *testing.T) {
req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
req.Header.Set("Authorization", "Bearer invalid-token")
w := httptest.NewRecorder()

server.handleMe(w, req)

assert.Equal(t, http.StatusUnauthorized, w.Code)
})

t.Run("rejects POST method", func(t *testing.T) {
req := httptest.NewRequest(http.MethodPost, "/api/me", nil)
req.Header.Set("Authorization", "Bearer "+adminKey)
w := httptest.NewRecorder()

server.handleMe(w, req)

assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
})

t.Run("returns api key identity", func(t *testing.T) {
secret, _, err := server.keyManager.CreateKey("ci-bot", []auth.Permission{auth.PermRead}, nil)
require.NoError(t, err)

req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
req.Header.Set("Authorization", "Bearer "+secret)
w := httptest.NewRecorder()

server.handleMe(w, req)

assert.Equal(t, http.StatusOK, w.Code)
var resp meResponse
require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
assert.Equal(t, "ci-bot", resp.ClientID)
assert.False(t, resp.IsAdmin)
assert.Contains(t, resp.Permissions, auth.PermRead)
})
}

// TestHandleAdminWorkers tests the GET /admin/workers endpoint and verifies
// that workers appear as connected after they subscribe.
func TestHandleAdminWorkers(t *testing.T) {
	const adminKey = "test-admin-workers-key"
	config := createTestConfigWithAdminKey(t, adminKey)
	server, err := NewMainWorkerServer(config)
	require.NoError(t, err)

	t.Run("returns empty list when no workers have subscribed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/workers", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAdminWorkers(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
		var workers []map[string]interface{}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&workers))
		assert.Empty(t, workers)
	})

	t.Run("shows worker as connected after subscribe", func(t *testing.T) {
		// Simulate a processing worker subscribing via the gRPC Subscribe RPC.
		// The Main Worker must vend a session token and a master key wrapped
		// with the worker's public key before the worker can be considered
		// connected.
		privKey, pubKey, err := crypto.GenerateRSAKeyPair(2048)
		require.NoError(t, err)
		pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
		require.NoError(t, err)

		subReq := &proto.SubscribeRequest{
			WorkerId: "proc-worker-1",
			Pubkey:   pubKeyPEM,
			Tags:     map[string]string{"env": "test"},
		}
		subResp, err := server.Subscribe(context.Background(), subReq)
		require.NoError(t, err)

		// Verify the subscription response carries the required key material:
		// a short-lived worker token and the master key wrapped to the worker's
		// RSA public key.  Without these the worker cannot decrypt entities.
		assert.NotEmpty(t, subResp.Token, "subscribe response must contain a worker session token")
		assert.NotEmpty(t, subResp.WrappedKey, "subscribe response must contain the wrapped encryption key")
		assert.NotEmpty(t, subResp.KeyId, "subscribe response must identify the encryption key")

		// Confirm the wrapped key can actually be unwrapped with the worker's
		// private key — proving the Main Worker encrypted it correctly.
		unwrapped, err := crypto.UnwrapKey(privKey, subResp.WrappedKey)
		require.NoError(t, err)
		assert.Equal(t, server.masterKey, unwrapped,
			"unwrapped key must match the Main Worker's master key")

		// The admin/workers endpoint must now list the subscribed worker.
		req := httptest.NewRequest(http.MethodGet, "/admin/workers", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAdminWorkers(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var workers []map[string]interface{}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&workers))
		require.Len(t, workers, 1, "expected exactly one connected worker")

		worker := workers[0]
		assert.Equal(t, "proc-worker-1", worker["worker_id"])
		assert.Equal(t, "Available", worker["status"],
			"worker should be shown as Available (connected)")
	})

	t.Run("shows multiple workers as connected after each subscribes", func(t *testing.T) {
		freshConfig := createTestConfigWithAdminKey(t, adminKey)
		freshServer, err := NewMainWorkerServer(freshConfig)
		require.NoError(t, err)

		workerIDs := []string{"proc-worker-a", "proc-worker-b", "proc-worker-c"}
		for _, id := range workerIDs {
			privKey, pubKey, err := crypto.GenerateRSAKeyPair(2048)
			require.NoError(t, err)
			pubKeyPEM, err := crypto.MarshalPublicKeyToPEM(pubKey)
			require.NoError(t, err)

			subResp, err := freshServer.Subscribe(context.Background(), &proto.SubscribeRequest{
				WorkerId: id,
				Pubkey:   pubKeyPEM,
			})
			require.NoError(t, err)

			// Each worker must receive a token and the wrapped master key so it
			// can decrypt entity data.
			assert.NotEmpty(t, subResp.Token)
			assert.NotEmpty(t, subResp.WrappedKey)

			// Each worker must be able to unwrap the key it received.
			_, err = crypto.UnwrapKey(privKey, subResp.WrappedKey)
			require.NoError(t, err, "worker %s must be able to unwrap its key", id)
		}

		req := httptest.NewRequest(http.MethodGet, "/admin/workers", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		freshServer.handleAdminWorkers(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		var workers []map[string]interface{}
		require.NoError(t, json.NewDecoder(w.Body).Decode(&workers))
		assert.Len(t, workers, len(workerIDs), "all subscribed workers should be shown as connected")
		for _, wk := range workers {
			assert.Equal(t, "Available", wk["status"])
		}
	})

	t.Run("rejects unauthenticated request with 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/workers", nil)
		w := httptest.NewRecorder()
		server.handleAdminWorkers(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("rejects non-admin token with 403", func(t *testing.T) {
		secret, _, err := server.keyManager.CreateKey("readonly", []auth.Permission{auth.PermRead}, nil)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/admin/workers", nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		w := httptest.NewRecorder()
		server.handleAdminWorkers(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("rejects non-GET method with 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/workers", nil)
		req.Header.Set("Authorization", adminBearer(adminKey))
		w := httptest.NewRecorder()
		server.handleAdminWorkers(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}
