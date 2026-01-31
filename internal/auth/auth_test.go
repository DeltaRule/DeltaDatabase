package auth

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTokenManager(t *testing.T) {
	t.Run("creates token manager with custom TTLs", func(t *testing.T) {
		tm := NewTokenManager(30*time.Minute, 12*time.Hour)

		assert.NotNil(t, tm)
		assert.Equal(t, 30*time.Minute, tm.workerTokenTTL)
		assert.Equal(t, 12*time.Hour, tm.clientTokenTTL)
	})

	t.Run("uses default TTLs when zero", func(t *testing.T) {
		tm := NewTokenManager(0, 0)

		assert.NotNil(t, tm)
		assert.Equal(t, 1*time.Hour, tm.workerTokenTTL)
		assert.Equal(t, 24*time.Hour, tm.clientTokenTTL)
	})
}

func TestGenerateWorkerToken(t *testing.T) {
	tm := NewTokenManager(1*time.Hour, 24*time.Hour)

	t.Run("generates valid worker token", func(t *testing.T) {
		tags := map[string]string{"env": "prod", "region": "us-west"}
		token, err := tm.GenerateWorkerToken("worker-1", "key-123", tags)

		assert.NoError(t, err)
		assert.NotNil(t, token)
		assert.NotEmpty(t, token.Token)
		assert.Equal(t, "worker-1", token.WorkerID)
		assert.Equal(t, "key-123", token.KeyID)
		assert.Equal(t, tags, token.Tags)
		assert.True(t, token.ExpiresAt.After(time.Now()))
	})

	t.Run("returns error for empty worker ID", func(t *testing.T) {
		token, err := tm.GenerateWorkerToken("", "key-123", nil)

		assert.Error(t, err)
		assert.Nil(t, token)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		token1, err := tm.GenerateWorkerToken("worker-1", "key-123", nil)
		require.NoError(t, err)

		token2, err := tm.GenerateWorkerToken("worker-2", "key-456", nil)
		require.NoError(t, err)

		assert.NotEqual(t, token1.Token, token2.Token)
	})
}

func TestGenerateClientToken(t *testing.T) {
	tm := NewTokenManager(1*time.Hour, 24*time.Hour)

	t.Run("generates valid client token", func(t *testing.T) {
		roles := []string{"admin", "user"}
		token, err := tm.GenerateClientToken("client-1", roles)

		assert.NoError(t, err)
		assert.NotNil(t, token)
		assert.NotEmpty(t, token.Token)
		assert.Equal(t, "client-1", token.ClientID)
		assert.Equal(t, roles, token.Roles)
		assert.True(t, token.ExpiresAt.After(time.Now()))
	})

	t.Run("returns error for empty client ID", func(t *testing.T) {
		token, err := tm.GenerateClientToken("", []string{"user"})

		assert.Error(t, err)
		assert.Nil(t, token)
	})
}

func TestValidateWorkerToken(t *testing.T) {
	tm := NewTokenManager(100*time.Millisecond, 24*time.Hour)

	t.Run("validates existing token", func(t *testing.T) {
		token, err := tm.GenerateWorkerToken("worker-1", "key-123", nil)
		require.NoError(t, err)

		validated, err := tm.ValidateWorkerToken(token.Token)

		assert.NoError(t, err)
		assert.NotNil(t, validated)
		assert.Equal(t, "worker-1", validated.WorkerID)
		assert.Equal(t, "key-123", validated.KeyID)
	})

	t.Run("returns error for invalid token", func(t *testing.T) {
		validated, err := tm.ValidateWorkerToken("invalid-token")

		assert.Error(t, err)
		assert.Nil(t, validated)
		assert.Contains(t, err.Error(), "invalid token")
	})

	t.Run("returns error for empty token", func(t *testing.T) {
		validated, err := tm.ValidateWorkerToken("")

		assert.Error(t, err)
		assert.Nil(t, validated)
	})

	t.Run("returns error for expired token", func(t *testing.T) {
		token, err := tm.GenerateWorkerToken("worker-1", "key-123", nil)
		require.NoError(t, err)

		// Wait for token to expire
		time.Sleep(150 * time.Millisecond)

		validated, err := tm.ValidateWorkerToken(token.Token)

		assert.Error(t, err)
		assert.Nil(t, validated)
		assert.Contains(t, err.Error(), "expired")
	})
}

func TestValidateClientToken(t *testing.T) {
	tm := NewTokenManager(1*time.Hour, 100*time.Millisecond)

	t.Run("validates existing token", func(t *testing.T) {
		token, err := tm.GenerateClientToken("client-1", []string{"user"})
		require.NoError(t, err)

		validated, err := tm.ValidateClientToken(token.Token)

		assert.NoError(t, err)
		assert.NotNil(t, validated)
		assert.Equal(t, "client-1", validated.ClientID)
	})

	t.Run("returns error for expired token", func(t *testing.T) {
		token, err := tm.GenerateClientToken("client-1", []string{"user"})
		require.NoError(t, err)

		time.Sleep(150 * time.Millisecond)

		validated, err := tm.ValidateClientToken(token.Token)

		assert.Error(t, err)
		assert.Nil(t, validated)
	})
}

func TestRevokeTokens(t *testing.T) {
	tm := NewTokenManager(1*time.Hour, 24*time.Hour)

	t.Run("revokes worker token", func(t *testing.T) {
		token, err := tm.GenerateWorkerToken("worker-1", "key-123", nil)
		require.NoError(t, err)

		err = tm.RevokeWorkerToken(token.Token)
		assert.NoError(t, err)

		// Token should no longer be valid
		validated, err := tm.ValidateWorkerToken(token.Token)
		assert.Error(t, err)
		assert.Nil(t, validated)
	})

	t.Run("returns error for non-existent worker token", func(t *testing.T) {
		err := tm.RevokeWorkerToken("non-existent")
		assert.Error(t, err)
	})

	t.Run("revokes client token", func(t *testing.T) {
		token, err := tm.GenerateClientToken("client-1", []string{"user"})
		require.NoError(t, err)

		err = tm.RevokeClientToken(token.Token)
		assert.NoError(t, err)

		validated, err := tm.ValidateClientToken(token.Token)
		assert.Error(t, err)
		assert.Nil(t, validated)
	})
}

func TestTokenCounts(t *testing.T) {
	tm := NewTokenManager(1*time.Hour, 24*time.Hour)

	t.Run("tracks worker token count", func(t *testing.T) {
		assert.Equal(t, 0, tm.GetWorkerTokenCount())

		_, err := tm.GenerateWorkerToken("worker-1", "key-123", nil)
		require.NoError(t, err)
		assert.Equal(t, 1, tm.GetWorkerTokenCount())

		_, err = tm.GenerateWorkerToken("worker-2", "key-456", nil)
		require.NoError(t, err)
		assert.Equal(t, 2, tm.GetWorkerTokenCount())
	})

	t.Run("tracks client token count", func(t *testing.T) {
		assert.Equal(t, 0, tm.GetClientTokenCount())

		_, err := tm.GenerateClientToken("client-1", []string{"user"})
		require.NoError(t, err)
		assert.Equal(t, 1, tm.GetClientTokenCount())
	})
}

func TestConcurrentTokenOperations(t *testing.T) {
	tm := NewTokenManager(1*time.Hour, 24*time.Hour)

	t.Run("handles concurrent worker token generation", func(t *testing.T) {
		var wg sync.WaitGroup
		errors := make(chan error, 100)

		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				_, err := tm.GenerateWorkerToken(string(rune(id)), "key", nil)
				if err != nil {
					errors <- err
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			assert.NoError(t, err)
		}
	})

	t.Run("handles concurrent validation", func(t *testing.T) {
		token, err := tm.GenerateWorkerToken("worker-1", "key-123", nil)
		require.NoError(t, err)

		var wg sync.WaitGroup
		errors := make(chan error, 100)

		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := tm.ValidateWorkerToken(token.Token)
				if err != nil {
					errors <- err
				}
			}()
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			assert.NoError(t, err)
		}
	})
}

func TestNewWorkerAuthenticator(t *testing.T) {
	wa := NewWorkerAuthenticator()

	assert.NotNil(t, wa)
	assert.Empty(t, wa.ListWorkers())
}

func TestRegisterWorker(t *testing.T) {
	wa := NewWorkerAuthenticator()

	t.Run("registers new worker", func(t *testing.T) {
		tags := map[string]string{"env": "prod"}
		err := wa.RegisterWorker("worker-1", "password123", tags)

		assert.NoError(t, err)
		assert.Contains(t, wa.ListWorkers(), "worker-1")
	})

	t.Run("returns error for empty worker ID", func(t *testing.T) {
		err := wa.RegisterWorker("", "password", nil)
		assert.Error(t, err)
	})

	t.Run("returns error for empty password", func(t *testing.T) {
		err := wa.RegisterWorker("worker-2", "", nil)
		assert.Error(t, err)
	})

	t.Run("returns error for duplicate worker", func(t *testing.T) {
		wa2 := NewWorkerAuthenticator()
		err := wa2.RegisterWorker("worker-dup", "password", nil)
		require.NoError(t, err)

		err = wa2.RegisterWorker("worker-dup", "password", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "already registered")
	})
}

func TestAuthenticateWorker(t *testing.T) {
	wa := NewWorkerAuthenticator()

	err := wa.RegisterWorker("worker-1", "password123", map[string]string{"env": "prod"})
	require.NoError(t, err)

	t.Run("authenticates with correct credentials", func(t *testing.T) {
		creds, err := wa.AuthenticateWorker("worker-1", "password123")

		assert.NoError(t, err)
		assert.NotNil(t, creds)
		assert.Equal(t, "worker-1", creds.WorkerID)
		assert.True(t, creds.Enabled)
		assert.Equal(t, "prod", creds.Tags["env"])
	})

	t.Run("returns error for wrong password", func(t *testing.T) {
		creds, err := wa.AuthenticateWorker("worker-1", "wrongpassword")

		assert.Error(t, err)
		assert.Nil(t, creds)
		assert.Contains(t, err.Error(), "invalid credentials")
	})

	t.Run("returns error for non-existent worker", func(t *testing.T) {
		creds, err := wa.AuthenticateWorker("non-existent", "password")

		assert.Error(t, err)
		assert.Nil(t, creds)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("returns error for empty worker ID", func(t *testing.T) {
		creds, err := wa.AuthenticateWorker("", "password")

		assert.Error(t, err)
		assert.Nil(t, creds)
	})

	t.Run("returns error for disabled worker", func(t *testing.T) {
		err := wa.DisableWorker("worker-1")
		require.NoError(t, err)

		creds, err := wa.AuthenticateWorker("worker-1", "password123")

		assert.Error(t, err)
		assert.Nil(t, creds)
		assert.Contains(t, err.Error(), "disabled")
	})
}

func TestDisableEnableWorker(t *testing.T) {
	wa := NewWorkerAuthenticator()

	err := wa.RegisterWorker("worker-1", "password", nil)
	require.NoError(t, err)

	t.Run("disables worker", func(t *testing.T) {
		err := wa.DisableWorker("worker-1")
		assert.NoError(t, err)

		creds, err := wa.GetWorkerCredentials("worker-1")
		require.NoError(t, err)
		assert.False(t, creds.Enabled)
	})

	t.Run("enables worker", func(t *testing.T) {
		err := wa.EnableWorker("worker-1")
		assert.NoError(t, err)

		creds, err := wa.GetWorkerCredentials("worker-1")
		require.NoError(t, err)
		assert.True(t, creds.Enabled)
	})

	t.Run("returns error for non-existent worker", func(t *testing.T) {
		err := wa.DisableWorker("non-existent")
		assert.Error(t, err)

		err = wa.EnableWorker("non-existent")
		assert.Error(t, err)
	})
}

func TestGetWorkerCredentials(t *testing.T) {
	wa := NewWorkerAuthenticator()

	tags := map[string]string{"region": "us-west"}
	err := wa.RegisterWorker("worker-1", "password", tags)
	require.NoError(t, err)

	t.Run("returns worker credentials", func(t *testing.T) {
		creds, err := wa.GetWorkerCredentials("worker-1")

		assert.NoError(t, err)
		assert.NotNil(t, creds)
		assert.Equal(t, "worker-1", creds.WorkerID)
		assert.Equal(t, tags, creds.Tags)
	})

	t.Run("returns error for non-existent worker", func(t *testing.T) {
		creds, err := wa.GetWorkerCredentials("non-existent")

		assert.Error(t, err)
		assert.Nil(t, creds)
	})
}

func TestListWorkers(t *testing.T) {
	wa := NewWorkerAuthenticator()

	t.Run("returns empty list initially", func(t *testing.T) {
		workers := wa.ListWorkers()
		assert.Empty(t, workers)
	})

	t.Run("lists all registered workers", func(t *testing.T) {
		wa.RegisterWorker("worker-1", "pass1", nil)
		wa.RegisterWorker("worker-2", "pass2", nil)
		wa.RegisterWorker("worker-3", "pass3", nil)

		workers := wa.ListWorkers()
		assert.Len(t, workers, 3)
		assert.Contains(t, workers, "worker-1")
		assert.Contains(t, workers, "worker-2")
		assert.Contains(t, workers, "worker-3")
	})
}

func TestHashPassword(t *testing.T) {
	t.Run("generates consistent hashes", func(t *testing.T) {
		hash1 := hashPassword("password123")
		hash2 := hashPassword("password123")

		assert.Equal(t, hash1, hash2)
		assert.NotEmpty(t, hash1)
	})

	t.Run("generates different hashes for different passwords", func(t *testing.T) {
		hash1 := hashPassword("password123")
		hash2 := hashPassword("password456")

		assert.NotEqual(t, hash1, hash2)
	})
}

func TestGenerateSecureToken(t *testing.T) {
	t.Run("generates non-empty token", func(t *testing.T) {
		token, err := generateSecureToken()

		assert.NoError(t, err)
		assert.NotEmpty(t, token)
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		token1, err := generateSecureToken()
		require.NoError(t, err)

		token2, err := generateSecureToken()
		require.NoError(t, err)

		assert.NotEqual(t, token1, token2)
	})

	t.Run("generates tokens of expected length", func(t *testing.T) {
		token, err := generateSecureToken()
		require.NoError(t, err)

		// Base64 encoded 32 bytes should be 44 characters
		assert.True(t, len(token) >= 40)
	})
}
