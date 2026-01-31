package fs

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStorage(t *testing.T) (*Storage, string) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "test_db")

	storage, err := NewStorage(basePath)
	require.NoError(t, err)
	require.NotNil(t, storage)

	return storage, basePath
}

func TestNewStorage(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "db")

	storage, err := NewStorage(basePath)
	require.NoError(t, err)
	require.NotNil(t, storage)

	// Verify directories were created
	assert.DirExists(t, basePath)
	assert.DirExists(t, storage.GetFilesDir())
	assert.DirExists(t, storage.GetTemplatesDir())
}

func TestNewStorageEmptyPath(t *testing.T) {
	_, err := NewStorage("")
	assert.Error(t, err)
}

func TestWriteAndReadFile(t *testing.T) {
	storage, _ := setupTestStorage(t)

	// Create test data
	id := "test_entity"
	blob := []byte("encrypted test data")
	metadata := FileMetadata{
		KeyID:     "key-123",
		Algorithm: "AES-GCM",
		IV:        base64.StdEncoding.EncodeToString([]byte("test-nonce")),
		Tag:       base64.StdEncoding.EncodeToString([]byte("test-tag")),
		SchemaID:  "test.v1",
		Version:   1,
		WriterID:  "worker-1",
		Timestamp: time.Now(),
		Database:  "testdb",
		EntityKey: id,
	}

	// Write file
	err := storage.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	// Verify files exist
	assert.FileExists(t, storage.GetBlobPath(id))
	assert.FileExists(t, storage.GetMetaPath(id))

	// Read file back
	fileData, err := storage.ReadFile(id)
	require.NoError(t, err)
	require.NotNil(t, fileData)

	// Verify data matches
	assert.Equal(t, blob, fileData.Blob)
	assert.Equal(t, metadata.KeyID, fileData.Metadata.KeyID)
	assert.Equal(t, metadata.Algorithm, fileData.Metadata.Algorithm)
	assert.Equal(t, metadata.SchemaID, fileData.Metadata.SchemaID)
	assert.Equal(t, metadata.Version, fileData.Metadata.Version)
	assert.Equal(t, metadata.WriterID, fileData.Metadata.WriterID)
}

func TestWriteFileEmptyID(t *testing.T) {
	storage, _ := setupTestStorage(t)

	blob := []byte("test data")
	metadata := FileMetadata{Algorithm: "AES-GCM"}

	err := storage.WriteFile("", blob, metadata)
	assert.ErrorIs(t, err, ErrInvalidID)
}

func TestReadFileNotFound(t *testing.T) {
	storage, _ := setupTestStorage(t)

	_, err := storage.ReadFile("nonexistent")
	assert.ErrorIs(t, err, ErrFileNotFound)
}

func TestReadFileEmptyID(t *testing.T) {
	storage, _ := setupTestStorage(t)

	_, err := storage.ReadFile("")
	assert.ErrorIs(t, err, ErrInvalidID)
}

func TestWriteFileAtomicity(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "atomic_test"
	blob := []byte("test data")
	metadata := FileMetadata{
		KeyID:     "key-1",
		Algorithm: "AES-GCM",
		SchemaID:  "test.v1",
		Version:   1,
	}

	err := storage.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	// Verify no temporary files remain
	filesDir := storage.GetFilesDir()
	entries, err := os.ReadDir(filesDir)
	require.NoError(t, err)

	for _, entry := range entries {
		name := entry.Name()
		assert.NotContains(t, name, ".tmp", "Temporary file should not exist after write")
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "overwrite_test"

	// Write first version
	blob1 := []byte("version 1")
	metadata1 := FileMetadata{
		KeyID:     "key-1",
		Algorithm: "AES-GCM",
		Version:   1,
	}
	err := storage.WriteFile(id, blob1, metadata1)
	require.NoError(t, err)

	// Write second version
	blob2 := []byte("version 2")
	metadata2 := FileMetadata{
		KeyID:     "key-1",
		Algorithm: "AES-GCM",
		Version:   2,
	}
	err = storage.WriteFile(id, blob2, metadata2)
	require.NoError(t, err)

	// Read and verify latest version
	fileData, err := storage.ReadFile(id)
	require.NoError(t, err)
	assert.Equal(t, blob2, fileData.Blob)
	assert.Equal(t, 2, fileData.Metadata.Version)
}

func TestFileExists(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "exists_test"

	// Should not exist initially
	assert.False(t, storage.FileExists(id))

	// Write file
	blob := []byte("test data")
	metadata := FileMetadata{Algorithm: "AES-GCM"}
	err := storage.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	// Should exist now
	assert.True(t, storage.FileExists(id))
}

func TestFileExistsEmptyID(t *testing.T) {
	storage, _ := setupTestStorage(t)

	assert.False(t, storage.FileExists(""))
}

func TestFileExistsPartialFiles(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "partial_test"

	// Create only the blob file, not metadata
	blobPath := storage.GetBlobPath(id)
	err := os.WriteFile(blobPath, []byte("test"), 0644)
	require.NoError(t, err)

	// Should return false because metadata is missing
	assert.False(t, storage.FileExists(id))
}

func TestDeleteFile(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "delete_test"

	// Write file
	blob := []byte("test data")
	metadata := FileMetadata{Algorithm: "AES-GCM"}
	err := storage.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	// Verify exists
	assert.True(t, storage.FileExists(id))

	// Delete file
	err = storage.DeleteFile(id)
	require.NoError(t, err)

	// Verify deleted
	assert.False(t, storage.FileExists(id))
}

func TestDeleteFileEmptyID(t *testing.T) {
	storage, _ := setupTestStorage(t)

	err := storage.DeleteFile("")
	assert.ErrorIs(t, err, ErrInvalidID)
}

func TestDeleteFileNotFound(t *testing.T) {
	storage, _ := setupTestStorage(t)

	// Should not error when deleting non-existent file
	err := storage.DeleteFile("nonexistent")
	assert.NoError(t, err)
}

func TestListFiles(t *testing.T) {
	storage, _ := setupTestStorage(t)

	// Initially empty
	files, err := storage.ListFiles()
	require.NoError(t, err)
	assert.Empty(t, files)

	// Write multiple files
	ids := []string{"file1", "file2", "file3"}
	metadata := FileMetadata{Algorithm: "AES-GCM"}

	for _, id := range ids {
		err := storage.WriteFile(id, []byte("data"), metadata)
		require.NoError(t, err)
	}

	// List files
	files, err = storage.ListFiles()
	require.NoError(t, err)
	assert.Len(t, files, 3)

	// Verify all IDs are present
	for _, id := range ids {
		assert.Contains(t, files, id)
	}
}

func TestGetBlobPath(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "test_id"
	path := storage.GetBlobPath(id)

	assert.Contains(t, path, "files")
	assert.Contains(t, path, "test_id.json.enc")
}

func TestGetMetaPath(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "test_id"
	path := storage.GetMetaPath(id)

	assert.Contains(t, path, "files")
	assert.Contains(t, path, "test_id.meta.json")
}

func TestWriteAndReadTemplate(t *testing.T) {
	storage, _ := setupTestStorage(t)

	schemaID := "user.v1"
	schemaData := []byte(`{
		"$id": "user.v1",
		"type": "object",
		"properties": {
			"id": {"type": "string"},
			"name": {"type": "string"}
		}
	}`)

	// Write template
	err := storage.WriteTemplate(schemaID, schemaData)
	require.NoError(t, err)

	// Read template
	readData, err := storage.ReadTemplate(schemaID)
	require.NoError(t, err)
	assert.Equal(t, schemaData, readData)
}

func TestWriteTemplateEmptyID(t *testing.T) {
	storage, _ := setupTestStorage(t)

	err := storage.WriteTemplate("", []byte("data"))
	assert.Error(t, err)
}

func TestReadTemplateNotFound(t *testing.T) {
	storage, _ := setupTestStorage(t)

	_, err := storage.ReadTemplate("nonexistent")
	assert.ErrorIs(t, err, ErrFileNotFound)
}

func TestReadTemplateEmptyID(t *testing.T) {
	storage, _ := setupTestStorage(t)

	_, err := storage.ReadTemplate("")
	assert.Error(t, err)
}

func TestMetadataJSONFormat(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "json_test"
	blob := []byte("test data")
	metadata := FileMetadata{
		KeyID:     "key-123",
		Algorithm: "AES-GCM",
		IV:        "dGVzdC1ub25jZQ==",
		Tag:       "dGVzdC10YWc=",
		SchemaID:  "test.v1",
		Version:   1,
		WriterID:  "worker-1",
		Timestamp: time.Now().Round(time.Second),
		Database:  "testdb",
		EntityKey: id,
	}

	err := storage.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	// Read raw metadata JSON
	metaPath := storage.GetMetaPath(id)
	metaJSON, err := os.ReadFile(metaPath)
	require.NoError(t, err)

	// Parse JSON
	var parsed FileMetadata
	err = json.Unmarshal(metaJSON, &parsed)
	require.NoError(t, err)

	// Verify fields
	assert.Equal(t, metadata.KeyID, parsed.KeyID)
	assert.Equal(t, metadata.Algorithm, parsed.Algorithm)
	assert.Equal(t, metadata.IV, parsed.IV)
	assert.Equal(t, metadata.Tag, parsed.Tag)
	assert.Equal(t, metadata.SchemaID, parsed.SchemaID)
	assert.Equal(t, metadata.Version, parsed.Version)
	assert.Equal(t, metadata.WriterID, parsed.WriterID)
	assert.Equal(t, metadata.Database, parsed.Database)
	assert.Equal(t, metadata.EntityKey, parsed.EntityKey)
}

func TestWriteFileLargeData(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "large_data_test"

	// Create 10MB of data
	largeBlob := make([]byte, 10*1024*1024)
	for i := range largeBlob {
		largeBlob[i] = byte(i % 256)
	}

	metadata := FileMetadata{
		Algorithm: "AES-GCM",
		Version:   1,
	}

	// Write large file
	err := storage.WriteFile(id, largeBlob, metadata)
	require.NoError(t, err)

	// Read back and verify
	fileData, err := storage.ReadFile(id)
	require.NoError(t, err)
	assert.Equal(t, largeBlob, fileData.Blob)
}

func TestWriteFileEmptyBlob(t *testing.T) {
	storage, _ := setupTestStorage(t)

	id := "empty_blob_test"
	blob := []byte{}
	metadata := FileMetadata{Algorithm: "AES-GCM"}

	err := storage.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	fileData, err := storage.ReadFile(id)
	require.NoError(t, err)
	assert.Empty(t, fileData.Blob)
}

func TestMultipleStorageInstances(t *testing.T) {
	tempDir := t.TempDir()
	basePath := filepath.Join(tempDir, "shared_db")

	// Create first storage instance
	storage1, err := NewStorage(basePath)
	require.NoError(t, err)

	// Write file with first instance
	id := "shared_test"
	blob := []byte("shared data")
	metadata := FileMetadata{Algorithm: "AES-GCM", Version: 1}
	err = storage1.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	// Create second storage instance pointing to same path
	storage2, err := NewStorage(basePath)
	require.NoError(t, err)

	// Read file with second instance
	fileData, err := storage2.ReadFile(id)
	require.NoError(t, err)
	assert.Equal(t, blob, fileData.Blob)
}

func TestListFilesWithOtherFiles(t *testing.T) {
	storage, _ := setupTestStorage(t)

	// Write normal files
	metadata := FileMetadata{Algorithm: "AES-GCM"}
	err := storage.WriteFile("file1", []byte("data"), metadata)
	require.NoError(t, err)

	// Create unrelated file in files directory
	filesDir := storage.GetFilesDir()
	err = os.WriteFile(filepath.Join(filesDir, "readme.txt"), []byte("info"), 0644)
	require.NoError(t, err)

	// List files should only return database files
	files, err := storage.ListFiles()
	require.NoError(t, err)
	assert.Contains(t, files, "file1")
	assert.NotContains(t, files, "readme")
}

// Benchmark tests
func BenchmarkWriteFile(b *testing.B) {
	tempDir := b.TempDir()
	storage, _ := NewStorage(filepath.Join(tempDir, "bench_db"))

	blob := []byte("benchmark data for write performance testing")
	metadata := FileMetadata{
		Algorithm: "AES-GCM",
		Version:   1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := filepath.Join("bench", string(rune(i)))
		_ = storage.WriteFile(id, blob, metadata)
	}
}

func BenchmarkReadFile(b *testing.B) {
	tempDir := b.TempDir()
	storage, _ := NewStorage(filepath.Join(tempDir, "bench_db"))

	// Prepare test file
	id := "bench_read"
	blob := []byte("benchmark data for read performance testing")
	metadata := FileMetadata{Algorithm: "AES-GCM"}
	_ = storage.WriteFile(id, blob, metadata)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = storage.ReadFile(id)
	}
}
