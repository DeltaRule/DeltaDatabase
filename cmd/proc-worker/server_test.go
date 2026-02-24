package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"
	"time"

	"delta-db/api/proto"
	"delta-db/pkg/cache"
	pkgcrypto "delta-db/pkg/crypto"
	"delta-db/pkg/fs"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// setupProcWorkerServer creates a ready-to-use ProcWorkerServer backed by a
// temporary filesystem, a small cache, and an encryption key injected directly
// into a ProcWorker without performing a real network handshake.
func setupProcWorkerServer(t *testing.T) (*ProcWorkerServer, *fs.Storage, []byte) {
	t.Helper()

	tmpDir := t.TempDir()

	storage, err := fs.NewStorage(tmpDir)
	require.NoError(t, err)

	c, err := cache.NewCache(cache.CacheConfig{
		MaxSize:    16,
		DefaultTTL: 5 * time.Minute,
	})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	privKey, _, err := pkgcrypto.GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	worker := &ProcWorker{
		config: &ProcConfig{
			MainAddr: "127.0.0.1:1",
			WorkerID: "test-worker",
		},
		privateKey: privKey,
	}

	key, err := pkgcrypto.GenerateKey(32)
	require.NoError(t, err)

	// Inject the encryption key directly (simulating a successful Handshake).
	worker.mu.Lock()
	worker.encryptionKey = key
	worker.sessionToken = "test-token"
	worker.keyID = "test-key-id"
	worker.mu.Unlock()

	srv := NewProcWorkerServer(worker, storage, c)
	return srv, storage, key
}

// writeEncryptedEntity encrypts data and writes it to the storage directory.
func writeEncryptedEntity(t *testing.T, storage *fs.Storage, entityID string, plaintext []byte, key []byte) fs.FileMetadata {
	t.Helper()

	result, err := pkgcrypto.Encrypt(key, plaintext)
	require.NoError(t, err)

	metadata := fs.FileMetadata{
		KeyID:     "test-key-id",
		Algorithm: "AES-GCM",
		IV:        base64.StdEncoding.EncodeToString(result.Nonce),
		Tag:       base64.StdEncoding.EncodeToString(result.Tag),
		SchemaID:  "test.v1",
		Version:   1,
	}

	err = storage.WriteFile(entityID, result.Ciphertext, metadata)
	require.NoError(t, err)

	return metadata
}

// ---------------------------------------------------------------------------
// NewProcWorkerServer
// ---------------------------------------------------------------------------

func TestNewProcWorkerServer(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)
	assert.NotNil(t, srv)
	assert.NotNil(t, srv.worker)
	assert.NotNil(t, srv.storage)
	assert.NotNil(t, srv.lockMgr)
	assert.NotNil(t, srv.cache)
}

// ---------------------------------------------------------------------------
// Process — invalid / missing arguments
// ---------------------------------------------------------------------------

func TestProcess_Subscribe_Unimplemented(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)
	_, err := srv.Subscribe(context.Background(), &proto.SubscribeRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestProcess_InvalidOperation(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	for _, op := range []string{"PUT", "DELETE", "bad", ""} {
		t.Run(fmt.Sprintf("op=%q", op), func(t *testing.T) {
			req := &proto.ProcessRequest{
				DatabaseName: "db",
				EntityKey:    "key",
				Operation:    op,
			}
			_, err := srv.Process(context.Background(), req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestProcess_MissingDatabaseName(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	req := &proto.ProcessRequest{
		DatabaseName: "",
		EntityKey:    "Chat_id",
		Operation:    "GET",
	}
	_, err := srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestProcess_MissingEntityKey(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "",
		Operation:    "GET",
	}
	_, err := srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestProcess_EntityNotFound(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "nonexistent",
		Operation:    "GET",
	}
	_, err := srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestProcess_NoEncryptionKey(t *testing.T) {
	// Worker without an injected encryption key.
	tmpDir := t.TempDir()
	storage, err := fs.NewStorage(tmpDir)
	require.NoError(t, err)

	c, err := cache.NewCache(cache.CacheConfig{MaxSize: 8, DefaultTTL: time.Minute})
	require.NoError(t, err)
	defer c.Close()

	privKey, _, err := pkgcrypto.GenerateRSAKeyPair(2048)
	require.NoError(t, err)

	worker := &ProcWorker{
		config:     &ProcConfig{MainAddr: "127.0.0.1:1", WorkerID: "no-key-worker"},
		privateKey: privKey,
		// encryptionKey intentionally empty
	}

	srv := NewProcWorkerServer(worker, storage, c)

	// Write a dummy file so the storage read succeeds.
	key, err := pkgcrypto.GenerateKey(32)
	require.NoError(t, err)
	writeEncryptedEntity(t, storage, "chatdb_Chat_id", []byte(`{"chat":[]}`), key)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "Chat_id",
		Operation:    "GET",
	}
	_, err = srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// ---------------------------------------------------------------------------
// Process — successful GET (cache miss → FS read → decrypt)
// ---------------------------------------------------------------------------

func TestProcess_GET_Success(t *testing.T) {
	srv, storage, key := setupProcWorkerServer(t)

	payload := map[string]interface{}{
		"chat": []map[string]string{
			{"type": "assistant", "text": "hello"},
		},
	}
	plaintext, err := json.Marshal(payload)
	require.NoError(t, err)

	writeEncryptedEntity(t, storage, "chatdb_Chat_id", plaintext, key)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "Chat_id",
		Operation:    "GET",
	}
	resp, err := srv.Process(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.GetStatus())
	assert.JSONEq(t, string(plaintext), string(resp.GetResult()))
	assert.Equal(t, "1", resp.GetVersion())
}

// ---------------------------------------------------------------------------
// Process — cache hit path
// ---------------------------------------------------------------------------

func TestProcess_GET_CacheHit(t *testing.T) {
	srv, storage, key := setupProcWorkerServer(t)

	plaintext := []byte(`{"chat":[{"type":"user","text":"hi"}]}`)
	writeEncryptedEntity(t, storage, "chatdb_CacheKey", plaintext, key)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "CacheKey",
		Operation:    "GET",
	}

	// First call: cache miss → reads from FS and populates cache.
	resp1, err := srv.Process(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "OK", resp1.GetStatus())

	// Second call: should be served from cache.
	// To verify, corrupt the file on disk and confirm the cached value is returned.
	entityID := "chatdb_CacheKey"
	err = storage.WriteFile(entityID, []byte("corrupted"), fs.FileMetadata{
		Algorithm: "AES-GCM",
		IV:        base64.StdEncoding.EncodeToString([]byte("000000000000")),
		Tag:       base64.StdEncoding.EncodeToString(make([]byte, 16)),
		Version:   2,
	})
	require.NoError(t, err)

	resp2, err := srv.Process(context.Background(), req)
	require.NoError(t, err, "second call should be served from cache, not the corrupted disk file")
	assert.Equal(t, "OK", resp2.GetStatus())
	assert.Equal(t, resp1.GetResult(), resp2.GetResult(),
		"cached result should match the first (clean) result")
}

// ---------------------------------------------------------------------------
// Process — version is forwarded correctly
// ---------------------------------------------------------------------------

func TestProcess_GET_VersionForwarded(t *testing.T) {
	srv, storage, key := setupProcWorkerServer(t)

	plaintext := []byte(`{"chat":[]}`)
	result, err := pkgcrypto.Encrypt(key, plaintext)
	require.NoError(t, err)

	const wantVersion = 7
	metadata := fs.FileMetadata{
		KeyID:     "test-key-id",
		Algorithm: "AES-GCM",
		IV:        base64.StdEncoding.EncodeToString(result.Nonce),
		Tag:       base64.StdEncoding.EncodeToString(result.Tag),
		Version:   wantVersion,
	}
	err = storage.WriteFile("versiondb_versioned", result.Ciphertext, metadata)
	require.NoError(t, err)

	req := &proto.ProcessRequest{
		DatabaseName: "versiondb",
		EntityKey:    "versioned",
		Operation:    "GET",
	}
	resp, err := srv.Process(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, strconv.Itoa(wantVersion), resp.GetVersion())
}

// ---------------------------------------------------------------------------
// Process — decryption fails when wrong key is used
// ---------------------------------------------------------------------------

func TestProcess_GET_WrongKey(t *testing.T) {
	srv, storage, _ := setupProcWorkerServer(t)

	// Encrypt with a different key than the one in the worker.
	wrongKey, err := pkgcrypto.GenerateKey(32)
	require.NoError(t, err)
	writeEncryptedEntity(t, storage, "chatdb_WrongKey", []byte(`{"chat":[]}`), wrongKey)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "WrongKey",
		Operation:    "GET",
	}
	_, err = srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}
