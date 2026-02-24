package fs

// Backend defines the storage interface satisfied by both the local-filesystem
// Storage and the S3-compatible S3Storage. Callers that need to support both
// backends should program against this interface.
type Backend interface {
	// WriteFile writes an encrypted blob and its metadata atomically.
	WriteFile(id string, data []byte, metadata FileMetadata) error

	// ReadFile reads an encrypted blob and its metadata.
	ReadFile(id string) (*FileData, error)

	// FileExists reports whether a file with the given ID exists.
	FileExists(id string) bool

	// DeleteFile removes both the encrypted blob and metadata.
	DeleteFile(id string) error

	// ListFiles returns all entity IDs present in the store.
	ListFiles() ([]string, error)

	// WriteTemplate persists a JSON-schema template.
	WriteTemplate(schemaID string, schemaData []byte) error

	// ReadTemplate retrieves a JSON-schema template.
	ReadTemplate(schemaID string) ([]byte, error)

	// GetTemplatesDir returns a local filesystem path suitable for
	// initialising the schema Validator. Implementations that do not store
	// templates on a local filesystem (e.g. S3Storage) return an empty string,
	// which causes the Validator to be skipped.
	GetTemplatesDir() string
}

// LockManagerInterface abstracts per-entity locking so that both the
// file-based LockManager and the in-memory MemoryLockManager can be used
// interchangeably by ProcWorkerServer.
type LockManagerInterface interface {
	// AcquireLock acquires a blocking lock of the given type for entity id.
	AcquireLock(id string, lockType LockType) error

	// TryAcquireLock attempts a non-blocking lock acquisition.
	TryAcquireLock(id string, lockType LockType) error

	// ReleaseLock releases the lock held for entity id.
	ReleaseLock(id string) error

	// ReleaseAll releases every lock currently held by this manager.
	ReleaseAll() error
}

// FileLockManagerAdapter wraps a *LockManager so it satisfies
// LockManagerInterface (discarding the *FileLock return values that the
// underlying LockManager exposes but callers do not need).
type FileLockManagerAdapter struct {
	mgr *LockManager
}

// NewFileLockManagerAdapter creates a FileLockManagerAdapter backed by a
// new LockManager for the given Storage.
func NewFileLockManagerAdapter(storage *Storage) *FileLockManagerAdapter {
	return &FileLockManagerAdapter{mgr: NewLockManager(storage)}
}

func (a *FileLockManagerAdapter) AcquireLock(id string, lockType LockType) error {
	_, err := a.mgr.AcquireLock(id, lockType)
	return err
}

func (a *FileLockManagerAdapter) TryAcquireLock(id string, lockType LockType) error {
	_, err := a.mgr.TryAcquireLock(id, lockType)
	return err
}

func (a *FileLockManagerAdapter) ReleaseLock(id string) error {
	return a.mgr.ReleaseLock(id)
}

func (a *FileLockManagerAdapter) ReleaseAll() error {
	return a.mgr.ReleaseAll()
}
