package fs

// StorageBackend is the interface implemented by all storage backends.
// The local filesystem backend (*Storage) and the S3-compatible backend
// (*S3Storage) both satisfy this interface, allowing callers to switch
// between backends without changing any processing logic.
type StorageBackend interface {
	// WriteFile writes an encrypted blob and its metadata atomically.
	WriteFile(id string, data []byte, metadata FileMetadata) error

	// ReadFile reads the encrypted blob and its metadata.
	ReadFile(id string) (*FileData, error)

	// FileExists reports whether a complete (blob + metadata) entity exists.
	FileExists(id string) bool

	// DeleteFile removes the blob and metadata for the given entity ID.
	DeleteFile(id string) error

	// ListFiles returns the IDs of all stored entities.
	ListFiles() ([]string, error)

	// WriteTemplate stores a JSON Schema template identified by schemaID.
	WriteTemplate(schemaID string, schemaData []byte) error

	// ReadTemplate retrieves a JSON Schema template by schemaID.
	ReadTemplate(schemaID string) ([]byte, error)

	// GetTemplatesDir returns a local filesystem path to the directory that
	// holds template files.  The schema validator uses this path to load
	// schemas by filename.
	//
	// Backends backed by remote object storage (e.g. S3Storage) must sync
	// all templates to a local directory and return that path, so that the
	// file-based schema.Validator continues to work unchanged.
	GetTemplatesDir() string
}

// LockBackend is the interface for acquiring and releasing per-entity locks.
// The file-based LockManager (for local FS) and the in-memory MemoryLockManager
// (for S3-compatible backends) both satisfy this interface.
type LockBackend interface {
	// AcquireLock acquires a lock of the specified type for the entity with
	// the given id.  The call blocks until the lock can be granted.
	// The returned *FileLock may be nil for non-filesystem backends.
	AcquireLock(id string, lockType LockType) (*FileLock, error)

	// TryAcquireLock attempts a non-blocking lock acquisition.
	// Returns ErrAlreadyLocked if the lock cannot be obtained immediately.
	TryAcquireLock(id string, lockType LockType) (*FileLock, error)

	// ReleaseLock releases the lock for id.
	ReleaseLock(id string) error

	// ReleaseAll releases all locks currently held by this manager.
	ReleaseAll() error
}

// Compile-time interface satisfaction checks.
var _ StorageBackend = (*Storage)(nil)
var _ StorageBackend = (*S3Storage)(nil)
var _ LockBackend = (*LockManager)(nil)
var _ LockBackend = (*MemoryLockManager)(nil)
