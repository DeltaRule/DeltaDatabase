package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── APIKey helpers ──────────────────────────────────────────────────────────

func TestAPIKey_HasPermission(t *testing.T) {
	t.Run("matches exact permission", func(t *testing.T) {
		k := &APIKey{Permissions: []Permission{PermRead}}
		assert.True(t, k.HasPermission(PermRead))
		assert.False(t, k.HasPermission(PermWrite))
	})

	t.Run("admin grants all permissions", func(t *testing.T) {
		k := &APIKey{Permissions: []Permission{PermAdmin}}
		assert.True(t, k.HasPermission(PermRead))
		assert.True(t, k.HasPermission(PermWrite))
		assert.True(t, k.HasPermission(PermAdmin))
	})

	t.Run("empty permissions denies all", func(t *testing.T) {
		k := &APIKey{}
		assert.False(t, k.HasPermission(PermRead))
	})
}

func TestAPIKey_IsExpired(t *testing.T) {
	t.Run("nil expiry never expires", func(t *testing.T) {
		k := &APIKey{}
		assert.False(t, k.IsExpired())
	})

	t.Run("future expiry is not expired", func(t *testing.T) {
		future := time.Now().Add(time.Hour)
		k := &APIKey{ExpiresAt: &future}
		assert.False(t, k.IsExpired())
	})

	t.Run("past expiry is expired", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		k := &APIKey{ExpiresAt: &past}
		assert.True(t, k.IsExpired())
	})
}

// ── KeyManager ──────────────────────────────────────────────────────────────

func TestNewKeyManager_InMemory(t *testing.T) {
	km, err := NewKeyManager("")
	require.NoError(t, err)
	assert.NotNil(t, km)
	assert.Equal(t, 0, km.Count())
}

func TestNewKeyManager_Persistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	km, err := NewKeyManager(path)
	require.NoError(t, err)

	secret, key, err := km.CreateKey("test-key", []Permission{PermRead}, nil)
	require.NoError(t, err)
	require.NotEmpty(t, secret)

	// Re-load from disk.
	km2, err := NewKeyManager(path)
	require.NoError(t, err)
	assert.Equal(t, 1, km2.Count())

	got, err := km2.GetKey(key.ID)
	require.NoError(t, err)
	assert.Equal(t, "test-key", got.Name)
}

func TestNewKeyManager_NonExistentFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing.json")
	km, err := NewKeyManager(path)
	require.NoError(t, err) // missing file is not an error
	assert.Equal(t, 0, km.Count())
}

func TestNewKeyManager_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0o600))

	_, err := NewKeyManager(path)
	assert.Error(t, err)
}

// ── CreateKey ───────────────────────────────────────────────────────────────

func TestCreateKey(t *testing.T) {
	km, _ := NewKeyManager("")

	t.Run("creates key with correct fields", func(t *testing.T) {
		perms := []Permission{PermRead, PermWrite}
		secret, key, err := km.CreateKey("my-key", perms, nil)

		require.NoError(t, err)
		assert.NotEmpty(t, secret)
		assert.True(t, len(secret) > 10, "secret should not be trivially short")
		assert.NotEmpty(t, key.ID)
		assert.Equal(t, "my-key", key.Name)
		assert.Equal(t, perms, key.Permissions)
		assert.Nil(t, key.ExpiresAt)
		assert.True(t, key.Enabled)
		assert.False(t, key.CreatedAt.IsZero())
	})

	t.Run("secret starts with dk_ prefix", func(t *testing.T) {
		secret, _, err := km.CreateKey("prefixed", []Permission{PermRead}, nil)
		require.NoError(t, err)
		assert.True(t, len(secret) > 3 && secret[:3] == "dk_")
	})

	t.Run("sets expiry when provided", func(t *testing.T) {
		future := time.Now().Add(24 * time.Hour)
		_, key, err := km.CreateKey("expiring", []Permission{PermRead}, &future)
		require.NoError(t, err)
		require.NotNil(t, key.ExpiresAt)
		assert.True(t, key.ExpiresAt.After(time.Now()))
	})

	t.Run("error for empty name", func(t *testing.T) {
		_, _, err := km.CreateKey("", []Permission{PermRead}, nil)
		assert.Error(t, err)
	})

	t.Run("error for empty permissions", func(t *testing.T) {
		_, _, err := km.CreateKey("no-perms", nil, nil)
		assert.Error(t, err)
	})

	t.Run("generates unique IDs and secrets", func(t *testing.T) {
		s1, k1, err := km.CreateKey("a", []Permission{PermRead}, nil)
		require.NoError(t, err)
		s2, k2, err := km.CreateKey("b", []Permission{PermRead}, nil)
		require.NoError(t, err)

		assert.NotEqual(t, s1, s2)
		assert.NotEqual(t, k1.ID, k2.ID)
	})
}

// ── ValidateKey ─────────────────────────────────────────────────────────────

func TestValidateKey(t *testing.T) {
	km, _ := NewKeyManager("")

	t.Run("validates correct secret", func(t *testing.T) {
		secret, created, err := km.CreateKey("k", []Permission{PermRead}, nil)
		require.NoError(t, err)

		got, err := km.ValidateKey(secret)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
	})

	t.Run("error for empty secret", func(t *testing.T) {
		_, err := km.ValidateKey("")
		assert.Error(t, err)
	})

	t.Run("error for unknown secret", func(t *testing.T) {
		_, err := km.ValidateKey("dk_notavalidkey")
		assert.Error(t, err)
	})

	t.Run("error for expired key", func(t *testing.T) {
		past := time.Now().Add(-time.Hour)
		secret, _, err := km.CreateKey("expired", []Permission{PermRead}, &past)
		require.NoError(t, err)

		_, err = km.ValidateKey(secret)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "expired")
	})

	t.Run("error for disabled key", func(t *testing.T) {
		secret, key, err := km.CreateKey("disabled", []Permission{PermRead}, nil)
		require.NoError(t, err)

		require.NoError(t, km.RevokeKey(key.ID))

		_, err = km.ValidateKey(secret)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "disabled")
	})
}

// ── RevokeKey / DeleteKey ───────────────────────────────────────────────────

func TestRevokeKey(t *testing.T) {
	km, _ := NewKeyManager("")

	t.Run("disables key", func(t *testing.T) {
		_, key, _ := km.CreateKey("k", []Permission{PermRead}, nil)
		require.NoError(t, km.RevokeKey(key.ID))

		got, err := km.GetKey(key.ID)
		require.NoError(t, err)
		assert.False(t, got.Enabled)
	})

	t.Run("error for unknown ID", func(t *testing.T) {
		err := km.RevokeKey("nonexistent")
		assert.Error(t, err)
	})
}

func TestDeleteKey(t *testing.T) {
	km, _ := NewKeyManager("")

	t.Run("removes key permanently", func(t *testing.T) {
		_, key, _ := km.CreateKey("to-delete", []Permission{PermRead}, nil)
		assert.Equal(t, 1, km.Count())

		require.NoError(t, km.DeleteKey(key.ID))
		assert.Equal(t, 0, km.Count())
	})

	t.Run("error for unknown ID", func(t *testing.T) {
		err := km.DeleteKey("nonexistent")
		assert.Error(t, err)
	})
}

// ── ListKeys / GetKey ───────────────────────────────────────────────────────

func TestListKeys(t *testing.T) {
	km, _ := NewKeyManager("")

	km.CreateKey("a", []Permission{PermRead}, nil)  //nolint:errcheck
	km.CreateKey("b", []Permission{PermWrite}, nil) //nolint:errcheck
	km.CreateKey("c", []Permission{PermAdmin}, nil) //nolint:errcheck

	keys := km.ListKeys()
	assert.Len(t, keys, 3)

	names := make(map[string]bool)
	for _, k := range keys {
		names[k.Name] = true
	}
	assert.True(t, names["a"])
	assert.True(t, names["b"])
	assert.True(t, names["c"])
}

func TestGetKey(t *testing.T) {
	km, _ := NewKeyManager("")

	_, created, _ := km.CreateKey("lookup", []Permission{PermRead}, nil)

	t.Run("returns key by ID", func(t *testing.T) {
		got, err := km.GetKey(created.ID)
		require.NoError(t, err)
		assert.Equal(t, created.ID, got.ID)
		assert.Equal(t, "lookup", got.Name)
	})

	t.Run("error for unknown ID", func(t *testing.T) {
		_, err := km.GetKey("no-such-id")
		assert.Error(t, err)
	})
}

// ── Persistence round-trip ──────────────────────────────────────────────────

func TestKeyManager_PersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	km1, _ := NewKeyManager(path)
	future := time.Now().Add(48 * time.Hour)
	secret1, key1, _ := km1.CreateKey("persistent", []Permission{PermRead, PermWrite}, &future)
	_, key2, _ := km1.CreateKey("admin-key", []Permission{PermAdmin}, nil)

	// Force save and re-load.
	km1.save()
	km2, err := NewKeyManager(path)
	require.NoError(t, err)
	assert.Equal(t, 2, km2.Count())

	// Key1 should still validate.
	got, err := km2.ValidateKey(secret1)
	require.NoError(t, err)
	assert.Equal(t, key1.ID, got.ID)

	// Key2 metadata preserved.
	got2, err := km2.GetKey(key2.ID)
	require.NoError(t, err)
	assert.Equal(t, "admin-key", got2.Name)
	assert.True(t, got2.HasPermission(PermAdmin))
}
