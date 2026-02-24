package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// writeFileSync writes data to path and calls Sync() before closing the file,
// ensuring the data is flushed from the OS page-cache to stable storage before
// any subsequent rename. This prevents data loss on unexpected power-off.
func writeFileSync(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close() //nolint:errcheck
		return err
	}
	return f.Close()
}

var (
	// ErrFileNotFound is returned when the requested file does not exist
	ErrFileNotFound = errors.New("file not found")
	// ErrInvalidID is returned when the entity ID is invalid
	ErrInvalidID = errors.New("invalid entity ID")
	// ErrWriteFailed is returned when file write operation fails
	ErrWriteFailed = errors.New("write operation failed")
	// ErrReadFailed is returned when file read operation fails
	ErrReadFailed = errors.New("read operation failed")
)

// isValidEntityID returns true when id is safe to use as a filename component.
// It rejects empty strings, path separators (/ and \), and any component
// containing ".." to prevent directory-traversal attacks.
func isValidEntityID(id string) bool {
	if id == "" {
		return false
	}
	if strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}
	return true
}

// FileMetadata represents the metadata stored alongside encrypted files
type FileMetadata struct {
	KeyID      string    `json:"key_id"`       // ID of the encryption key used
	Algorithm  string    `json:"alg"`          // Encryption algorithm (e.g., "AES-GCM")
	IV         string    `json:"iv"`           // Initialization vector (nonce) in base64
	Tag        string    `json:"tag"`          // Authentication tag in base64
	SchemaID   string    `json:"schema_id"`    // JSON schema identifier
	Version    int       `json:"version"`      // File version for cache coherence
	WriterID   string    `json:"writer_id"`    // ID of the worker that wrote this file
	Timestamp  time.Time `json:"timestamp"`    // When the file was written
	Database   string    `json:"database"`     // Database name
	EntityKey  string    `json:"entity_key"`   // Entity key within database
}

// FileData represents the complete file information
type FileData struct {
	Blob     []byte        // Encrypted data blob
	Metadata FileMetadata  // Associated metadata
}

// Storage handles reading and writing encrypted files to the shared filesystem
type Storage struct {
	basePath string // Base path to shared filesystem (e.g., /shared/db)
}

// NewStorage creates a new Storage instance with the given base path
func NewStorage(basePath string) (*Storage, error) {
	if basePath == "" {
		return nil, fmt.Errorf("base path cannot be empty")
	}

	// Ensure base path exists
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %w", err)
	}

	// Create files subdirectory
	filesDir := filepath.Join(basePath, "files")
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create files directory: %w", err)
	}

	// Create templates subdirectory
	templatesDir := filepath.Join(basePath, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create templates directory: %w", err)
	}

	return &Storage{
		basePath: basePath,
	}, nil
}

// GetFilesDir returns the path to the files directory
func (s *Storage) GetFilesDir() string {
	return filepath.Join(s.basePath, "files")
}

// GetTemplatesDir returns the path to the templates directory
func (s *Storage) GetTemplatesDir() string {
	return filepath.Join(s.basePath, "templates")
}

// WriteFile writes an encrypted blob and its metadata to disk atomically.
// The operation uses temporary files and atomic rename to ensure consistency.
//
// Parameters:
//   - id: unique identifier for the entity (e.g., "Chat_id")
//   - data: encrypted blob to write
//   - metadata: metadata about the encryption and entity
//
// Returns an error if the write operation fails.
//
// Security notes:
//   - Uses atomic rename to prevent partial writes
//   - No temporary files are left on disk after completion
//   - Caller should hold an exclusive lock before calling this function
func (s *Storage) WriteFile(id string, data []byte, metadata FileMetadata) error {
	if !isValidEntityID(id) {
		return ErrInvalidID
	}

	filesDir := s.GetFilesDir()

	// Define target paths
	blobPath := filepath.Join(filesDir, fmt.Sprintf("%s.json.enc", id))
	metaPath := filepath.Join(filesDir, fmt.Sprintf("%s.meta.json", id))

	// Define temporary paths
	blobTempPath := blobPath + ".tmp"
	metaTempPath := metaPath + ".tmp"

	// Marshal metadata to JSON
	metaJSON, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("%w: failed to marshal metadata: %v", ErrWriteFailed, err)
	}

	// Write blob to temporary file and sync to disk before rename.
	if err := writeFileSync(blobTempPath, data, 0644); err != nil {
		return fmt.Errorf("%w: failed to write blob: %v", ErrWriteFailed, err)
	}

	// Write metadata to temporary file and sync to disk before rename.
	if err := writeFileSync(metaTempPath, metaJSON, 0644); err != nil {
		// Clean up blob temp file
		os.Remove(blobTempPath) //nolint:errcheck
		return fmt.Errorf("%w: failed to write metadata: %v", ErrWriteFailed, err)
	}

	// Atomically rename blob temp file to final location.
	if err := os.Rename(blobTempPath, blobPath); err != nil {
		// Clean up both temp files
		os.Remove(blobTempPath) //nolint:errcheck
		os.Remove(metaTempPath) //nolint:errcheck
		return fmt.Errorf("%w: failed to rename blob: %v", ErrWriteFailed, err)
	}

	// Atomically rename metadata temp file to final location.
	// If this rename fails after the blob rename succeeded, attempt to roll
	// back the blob rename so neither file is updated, preserving consistency.
	if err := os.Rename(metaTempPath, metaPath); err != nil {
		// Attempt rollback of blob rename (best-effort).
		os.Rename(blobPath, blobTempPath) //nolint:errcheck
		os.Remove(metaTempPath)           //nolint:errcheck
		return fmt.Errorf("%w: failed to rename metadata: %v", ErrWriteFailed, err)
	}

	return nil
}

// ReadFile reads an encrypted blob and its metadata from disk.
//
// Parameters:
//   - id: unique identifier for the entity (e.g., "Chat_id")
//
// Returns the file data (blob + metadata) or an error if the read fails.
//
// Security notes:
//   - Caller should hold at least a shared lock before calling this function
//   - Returns ErrFileNotFound if the file doesn't exist
func (s *Storage) ReadFile(id string) (*FileData, error) {
	if !isValidEntityID(id) {
		return nil, ErrInvalidID
	}

	filesDir := s.GetFilesDir()

	// Define file paths
	blobPath := filepath.Join(filesDir, fmt.Sprintf("%s.json.enc", id))
	metaPath := filepath.Join(filesDir, fmt.Sprintf("%s.meta.json", id))

	// Check if files exist
	if _, err := os.Stat(blobPath); os.IsNotExist(err) {
		return nil, ErrFileNotFound
	}
	if _, err := os.Stat(metaPath); os.IsNotExist(err) {
		return nil, ErrFileNotFound
	}

	// Read encrypted blob
	blob, err := os.ReadFile(blobPath)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read blob: %v", ErrReadFailed, err)
	}

	// Read metadata
	metaJSON, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read metadata: %v", ErrReadFailed, err)
	}

	// Unmarshal metadata
	var metadata FileMetadata
	if err := json.Unmarshal(metaJSON, &metadata); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal metadata: %v", ErrReadFailed, err)
	}

	return &FileData{
		Blob:     blob,
		Metadata: metadata,
	}, nil
}

// FileExists checks if a file with the given ID exists on disk
func (s *Storage) FileExists(id string) bool {
	if !isValidEntityID(id) {
		return false
	}

	filesDir := s.GetFilesDir()
	blobPath := filepath.Join(filesDir, fmt.Sprintf("%s.json.enc", id))
	metaPath := filepath.Join(filesDir, fmt.Sprintf("%s.meta.json", id))

	_, blobErr := os.Stat(blobPath)
	_, metaErr := os.Stat(metaPath)

	return blobErr == nil && metaErr == nil
}

// DeleteFile removes both the encrypted blob and metadata files
//
// Parameters:
//   - id: unique identifier for the entity
//
// Returns an error if deletion fails.
//
// Security notes:
//   - Caller should hold an exclusive lock before calling this function
func (s *Storage) DeleteFile(id string) error {
	if !isValidEntityID(id) {
		return ErrInvalidID
	}

	filesDir := s.GetFilesDir()

	blobPath := filepath.Join(filesDir, fmt.Sprintf("%s.json.enc", id))
	metaPath := filepath.Join(filesDir, fmt.Sprintf("%s.meta.json", id))

	// Remove blob file (ignore if not exists)
	if err := os.Remove(blobPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete blob: %w", err)
	}

	// Remove metadata file (ignore if not exists)
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	return nil
}

// ListFiles returns a list of all entity IDs in the storage
func (s *Storage) ListFiles() ([]string, error) {
	filesDir := s.GetFilesDir()

	entries, err := os.ReadDir(filesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	// Use a map to deduplicate (both .json.enc and .meta.json files)
	ids := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Check for .json.enc extension
		if len(name) > 9 && name[len(name)-9:] == ".json.enc" {
			id := name[:len(name)-9]
			ids[id] = true
		}

		// Check for .meta.json extension
		if len(name) > 10 && name[len(name)-10:] == ".meta.json" {
			id := name[:len(name)-10]
			ids[id] = true
		}
	}

	// Convert map to slice
	result := make([]string, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}

	return result, nil
}

// GetBlobPath returns the full path to the encrypted blob file
func (s *Storage) GetBlobPath(id string) string {
	return filepath.Join(s.GetFilesDir(), fmt.Sprintf("%s.json.enc", id))
}

// GetMetaPath returns the full path to the metadata file
func (s *Storage) GetMetaPath(id string) string {
	return filepath.Join(s.GetFilesDir(), fmt.Sprintf("%s.meta.json", id))
}

// WriteTemplate writes a JSON schema template to the templates directory
func (s *Storage) WriteTemplate(schemaID string, schemaData []byte) error {
	if !isValidEntityID(schemaID) {
		return fmt.Errorf("invalid schema ID")
	}

	templatesDir := s.GetTemplatesDir()
	templatePath := filepath.Join(templatesDir, fmt.Sprintf("%s.json", schemaID))

	if err := os.WriteFile(templatePath, schemaData, 0644); err != nil {
		return fmt.Errorf("failed to write template: %w", err)
	}

	return nil
}

// ReadTemplate reads a JSON schema template from the templates directory
func (s *Storage) ReadTemplate(schemaID string) ([]byte, error) {
	if !isValidEntityID(schemaID) {
		return nil, fmt.Errorf("invalid schema ID")
	}

	templatesDir := s.GetTemplatesDir()
	templatePath := filepath.Join(templatesDir, fmt.Sprintf("%s.json", schemaID))

	data, err := os.ReadFile(templatePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, fmt.Errorf("failed to read template: %w", err)
	}

	return data, nil
}
