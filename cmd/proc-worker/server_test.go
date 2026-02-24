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

	for _, op := range []string{"DELETE", "bad", ""} {
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

// ---------------------------------------------------------------------------
// Process — PUT: invalid argument cases
// ---------------------------------------------------------------------------

func TestProcess_PUT_EmptyPayload(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "Chat_id",
		Operation:    "PUT",
		Payload:      nil,
	}
	_, err := srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestProcess_PUT_SchemaValidationFailure(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	// Write a simple schema that requires a "name" field.
	schemaJSON := []byte(`{
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"required": ["name"]
	}`)
	err := srv.storage.WriteTemplate("test_schema", schemaJSON)
	require.NoError(t, err)

	// Payload missing required "name" field → validation should fail.
	req := &proto.ProcessRequest{
		DatabaseName: "db",
		EntityKey:    "entity1",
		Operation:    "PUT",
		SchemaId:     "test_schema",
		Payload:      []byte(`{"other": "field"}`),
	}
	_, err = srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestProcess_PUT_NoEncryptionKey(t *testing.T) {
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

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "Chat_id",
		Operation:    "PUT",
		Payload:      []byte(`{"chat":[]}`),
	}
	_, err = srv.Process(context.Background(), req)
	require.Error(t, err)
	assert.Equal(t, codes.Unavailable, status.Code(err))
}

// ---------------------------------------------------------------------------
// Process — PUT: successful create (new entity)
// ---------------------------------------------------------------------------

func TestProcess_PUT_CreateNew(t *testing.T) {
	srv, storage, key := setupProcWorkerServer(t)

	payload := []byte(`{"chat":[{"type":"assistant","text":"hello"}]}`)
	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "NewEntity",
		Operation:    "PUT",
		Payload:      payload,
	}
	resp, err := srv.Process(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.GetStatus())
	assert.Equal(t, "1", resp.GetVersion())

	// Wait for the asynchronous disk write to complete before inspecting the FS.
	srv.WaitForPendingWrites()

	// Verify the file was written to disk.
	entityID := "chatdb_NewEntity"
	assert.True(t, storage.FileExists(entityID), "encrypted file should exist on disk")

	// Verify the metadata contains expected fields.
	fileData, err := storage.ReadFile(entityID)
	require.NoError(t, err)
	assert.Equal(t, "AES-GCM", fileData.Metadata.Algorithm)
	assert.Equal(t, 1, fileData.Metadata.Version)
	assert.NotEmpty(t, fileData.Metadata.IV)
	assert.NotEmpty(t, fileData.Metadata.Tag)

	// Verify decryptability.
	nonce, err := base64.StdEncoding.DecodeString(fileData.Metadata.IV)
	require.NoError(t, err)
	tag, err := base64.StdEncoding.DecodeString(fileData.Metadata.Tag)
	require.NoError(t, err)
	plaintext, err := pkgcrypto.Decrypt(key, fileData.Blob, nonce, tag)
	require.NoError(t, err)
	assert.Equal(t, payload, plaintext)
}

// ---------------------------------------------------------------------------
// Process — PUT: version increment on update
// ---------------------------------------------------------------------------

func TestProcess_PUT_VersionIncrement(t *testing.T) {
	srv, storage, key := setupProcWorkerServer(t)

	entityID := "chatdb_VersionTest"
	// Pre-populate the entity on disk at version 3.
	encResult, err := pkgcrypto.Encrypt(key, []byte(`{"chat":[]}`))
	require.NoError(t, err)
	err = storage.WriteFile(entityID, encResult.Ciphertext, fs.FileMetadata{
		KeyID:     "test-key-id",
		Algorithm: "AES-GCM",
		IV:        base64.StdEncoding.EncodeToString(encResult.Nonce),
		Tag:       base64.StdEncoding.EncodeToString(encResult.Tag),
		Version:   3,
	})
	require.NoError(t, err)

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "VersionTest",
		Operation:    "PUT",
		Payload:      []byte(`{"chat":[{"type":"user","text":"hi"}]}`),
	}
	resp, err := srv.Process(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.GetStatus())
	assert.Equal(t, "4", resp.GetVersion(), "version should be incremented to 4")

	// Wait for the asynchronous disk write before verifying the metadata.
	srv.WaitForPendingWrites()

	// Verify the metadata on disk.
	fileData, err := storage.ReadFile(entityID)
	require.NoError(t, err)
	assert.Equal(t, 4, fileData.Metadata.Version)
}

// ---------------------------------------------------------------------------
// Process — PUT: cache is updated after write
// ---------------------------------------------------------------------------

func TestProcess_PUT_UpdatesCache(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	payload := []byte(`{"chat":[{"type":"user","text":"cached"}]}`)
	entityID := "chatdb_CacheWrite"

	req := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "CacheWrite",
		Operation:    "PUT",
		Payload:      payload,
	}
	_, err := srv.Process(context.Background(), req)
	require.NoError(t, err)

	// The entity should now be in cache.
	entry, ok := srv.cache.Get(entityID)
	require.True(t, ok, "entity should be cached after PUT")
	assert.Equal(t, payload, entry.Data)
	assert.Equal(t, "1", entry.Version)
}

// ---------------------------------------------------------------------------
// Process — PUT: schema validation passes for valid payload
// ---------------------------------------------------------------------------

func TestProcess_PUT_SchemaValidationSuccess(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	schemaJSON := []byte(`{
		"type": "object",
		"properties": {"name": {"type": "string"}},
		"required": ["name"]
	}`)
	err := srv.storage.WriteTemplate("name_schema", schemaJSON)
	require.NoError(t, err)

	req := &proto.ProcessRequest{
		DatabaseName: "db",
		EntityKey:    "entity2",
		Operation:    "PUT",
		SchemaId:     "name_schema",
		Payload:      []byte(`{"name": "alice"}`),
	}
	resp, err := srv.Process(context.Background(), req)
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.GetStatus())
}

// ---------------------------------------------------------------------------
// Process — PUT: metadata fields are populated correctly
// ---------------------------------------------------------------------------

func TestProcess_PUT_MetadataFields(t *testing.T) {
	srv, storage, _ := setupProcWorkerServer(t)

	req := &proto.ProcessRequest{
		DatabaseName: "mydb",
		EntityKey:    "MetaKey",
		Operation:    "PUT",
		Payload:      []byte(`{"chat":[]}`),
	}
	before := time.Now().UTC()
	resp, err := srv.Process(context.Background(), req)
	after := time.Now().UTC()
	require.NoError(t, err)
	assert.Equal(t, "OK", resp.GetStatus())

	// Wait for the asynchronous disk write before verifying the metadata.
	srv.WaitForPendingWrites()

	fileData, err := storage.ReadFile("mydb_MetaKey")
	require.NoError(t, err)
	meta := fileData.Metadata

	assert.Equal(t, "AES-GCM", meta.Algorithm)
	assert.Equal(t, "mydb", meta.Database)
	assert.Equal(t, "MetaKey", meta.EntityKey)
	assert.Equal(t, "test-worker", meta.WriterID)
	assert.Equal(t, "test-key-id", meta.KeyID)
	assert.True(t, !meta.Timestamp.Before(before) && !meta.Timestamp.After(after),
		"timestamp should be set at write time")
}

// ---------------------------------------------------------------------------
// Process — GET after PUT (round-trip)
// ---------------------------------------------------------------------------

func TestProcess_PUT_then_GET(t *testing.T) {
	srv, _, _ := setupProcWorkerServer(t)

	payload := []byte(`{"chat":[{"type":"assistant","text":"roundtrip"}]}`)

	putReq := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "RoundTrip",
		Operation:    "PUT",
		Payload:      payload,
	}
	putResp, err := srv.Process(context.Background(), putReq)
	require.NoError(t, err)
	assert.Equal(t, "OK", putResp.GetStatus())

	// Wait for the asynchronous disk write to complete before clearing the
	// cache, otherwise the subsequent GET would find no data on disk.
	srv.WaitForPendingWrites()

	// Clear cache to force a disk read on the subsequent GET.
	srv.cache.Clear()

	getReq := &proto.ProcessRequest{
		DatabaseName: "chatdb",
		EntityKey:    "RoundTrip",
		Operation:    "GET",
	}
	getResp, err := srv.Process(context.Background(), getReq)
	require.NoError(t, err)
	assert.Equal(t, "OK", getResp.GetStatus())
	assert.JSONEq(t, string(payload), string(getResp.GetResult()))
	assert.Equal(t, putResp.GetVersion(), getResp.GetVersion())
}
