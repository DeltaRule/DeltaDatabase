package main

import (
	"context"
	"testing"
	"time"

	"delta-db/api/proto"
	"delta-db/pkg/crypto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	t.Run("returns unimplemented with valid token", func(t *testing.T) {
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

		// Should return unimplemented error for now
		assert.Error(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "not_implemented", resp.Status)
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
