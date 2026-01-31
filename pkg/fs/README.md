# pkg/fs

Shared filesystem interaction and file locking helpers for DeltaDatabase.

## Overview

This package provides persistent storage and concurrency control for encrypted JSON blobs:
- **Storage**: Atomic file operations for encrypted data and metadata
- **Locking**: Advisory file locking for concurrent access control

## Components

### storage.go

Handles reading and writing encrypted files with associated metadata to the shared filesystem.

**Key Functions:**
- `NewStorage(basePath)`: Creates a storage instance and initializes directory structure
- `WriteFile(id, data, metadata)`: Atomically writes encrypted blob and metadata files
- `ReadFile(id)`: Reads encrypted blob and metadata
- `DeleteFile(id)`: Removes both files for an entity
- `FileExists(id)`: Checks if entity files exist
- `ListFiles()`: Returns all entity IDs in storage
- `WriteTemplate(schemaID, data)`: Writes JSON schema templates
- `ReadTemplate(schemaID)`: Reads JSON schema templates

**File Structure:**
```
/basePath/
  ├── files/
  │   ├── <entity-id>.json.enc    # Encrypted blob
  │   └── <entity-id>.meta.json   # Metadata
  └── templates/
      └── <schema-id>.json        # JSON Schema templates
```

**Metadata Structure:**
```go
type FileMetadata struct {
    KeyID     string    // Encryption key identifier
    Algorithm string    // "AES-GCM"
    IV        string    // Nonce (base64)
    Tag       string    // Auth tag (base64)
    SchemaID  string    // JSON schema identifier
    Version   int       // File version for cache coherence
    WriterID  string    // Worker that wrote the file
    Timestamp time.Time // Write timestamp
    Database  string    // Database name
    EntityKey string    // Entity key within database
}
```

**Atomicity:**
- Uses temporary files with atomic rename to prevent partial writes
- No `.tmp` files remain after successful writes
- Safe for concurrent access when combined with proper locking

### lock.go

Implements advisory file locking using Windows LockFileEx API for concurrency control.

**Key Types:**
- `FileLock`: Low-level file lock handle
- `LockManager`: High-level lock management for multiple entities
- `LockType`: `LockShared` (read) or `LockExclusive` (write)

**FileLock Functions:**
- `NewFileLock(filePath)`: Creates a lock handle for a file
- `Lock(lockType)`: Acquires a lock (blocking)
- `TryLock(lockType)`: Attempts to acquire lock (non-blocking)
- `Unlock()`: Releases the lock
- `Close()`: Releases lock and closes file handle

**LockManager Functions:**
- `NewLockManager(storage)`: Creates a lock manager
- `AcquireLock(id, lockType)`: Acquires lock for entity (blocking)
- `TryAcquireLock(id, lockType)`: Attempts to acquire lock (non-blocking)
- `ReleaseLock(id)`: Releases lock for entity
- `ReleaseAll()`: Releases all managed locks

**Lock Semantics:**
- **Shared locks** (read): Multiple readers can hold simultaneously
- **Exclusive locks** (write): Only one writer, no readers allowed
- Locks are advisory (cooperative) - all processes must use the same locking mechanism
- Lock files are created at `<entity-id>.json.enc.lock`

## Usage Examples

### Basic Storage Operations

```go
// Create storage
storage, err := fs.NewStorage("/shared/db")
if err != nil {
    log.Fatal(err)
}

// Write file
metadata := fs.FileMetadata{
    KeyID:     "key-123",
    Algorithm: "AES-GCM",
    IV:        base64.StdEncoding.EncodeToString(nonce),
    Tag:       base64.StdEncoding.EncodeToString(tag),
    SchemaID:  "chat.v1",
    Version:   1,
    WriterID:  "worker-1",
    Timestamp: time.Now(),
    Database:  "chatdb",
    EntityKey: "Chat_id",
}

encryptedData := []byte("...encrypted JSON...")
err = storage.WriteFile("Chat_id", encryptedData, metadata)
if err != nil {
    log.Fatal(err)
}

// Read file
fileData, err := storage.ReadFile("Chat_id")
if err != nil {
    log.Fatal(err)
}
fmt.Printf("Version: %d\n", fileData.Metadata.Version)
```

### File Locking for Concurrent Access

```go
storage, _ := fs.NewStorage("/shared/db")
lockManager := fs.NewLockManager(storage)

// Reading (shared lock)
func readEntity(id string) {
    lock, err := lockManager.AcquireLock(id, fs.LockShared)
    if err != nil {
        return err
    }
    defer lockManager.ReleaseLock(id)
    
    // Multiple readers can hold shared locks
    fileData, err := storage.ReadFile(id)
    // ... process data ...
}

// Writing (exclusive lock)
func writeEntity(id string, data []byte, meta fs.FileMetadata) {
    lock, err := lockManager.AcquireLock(id, fs.LockExclusive)
    if err != nil {
        return err
    }
    defer lockManager.ReleaseLock(id)
    
    // Only one writer allowed
    err = storage.WriteFile(id, data, meta)
    // ...
}
```

### Non-blocking Lock Attempts

```go
lockManager := fs.NewLockManager(storage)

// Try to acquire without blocking
lock, err := lockManager.TryAcquireLock("entity_id", fs.LockExclusive)
if err == fs.ErrLockFailed {
    // Lock is held by another process
    return errors.New("entity is currently locked")
}
defer lockManager.ReleaseLock("entity_id")

// Proceed with operation
```

## Dependencies

- Standard library only:
  - `os`: File operations
  - `encoding/json`: Metadata serialization
  - `path/filepath`: Path manipulation
  - `syscall`: Windows file locking API (LockFileEx/UnlockFileEx)
  - `sync`: Mutex for thread safety

## Concurrency Model

The filesystem layer implements advisory locking:

1. **Processing Workers** must acquire locks before file operations:
   - **Shared lock (read)**: Multiple workers can read simultaneously
   - **Exclusive lock (write)**: Single writer, no concurrent readers

2. **Lock Coordination:**
   - Locks are per-entity (not per-file)
   - Lock files: `<entity-id>.json.enc.lock`
   - Locks persist across worker restarts (file-based)

3. **Typical Workflow:**
   ```
   Lock(Exclusive) -> ReadFile -> Decrypt -> Modify -> Encrypt -> WriteFile -> Unlock
   ```

4. **Cache Coherence:**
   - Metadata `version` field tracks file changes
   - Workers check version before using cached data
   - Writers increment version on each update

## Testing

All functions have comprehensive unit tests:
- `storage_test.go`: File operations, atomicity, error handling
- `lock_test.go`: Lock acquisition, concurrency, blocking behavior

Run tests:
```bash
go test -v ./pkg/fs/...
```

Run with race detector:
```bash
go test -race ./pkg/fs/...
```

## Platform-Specific Notes

### Windows
- Uses native `LockFileEx` and `UnlockFileEx` APIs
- Mandatory byte-range locking
- Locks are automatically released when process terminates

### Cross-Platform Considerations
- The current implementation is Windows-specific
- For Linux/Unix support, use `syscall.Flock()` or `fcntl()` instead
- Lock semantics remain the same across platforms

## Error Handling

- `ErrFileNotFound`: Entity does not exist
- `ErrInvalidID`: Empty or invalid entity ID
- `ErrWriteFailed`: File write operation failed
- `ErrReadFailed`: File read operation failed
- `ErrLockFailed`: Lock acquisition failed
- `ErrUnlockFailed`: Lock release failed
- `ErrAlreadyLocked`: Attempt to lock already-locked entity

## Security Considerations

1. **Atomic Writes**: Temporary files prevent partial writes that could corrupt data
2. **No Plaintext**: Storage layer only handles encrypted blobs
3. **Lock Cleanup**: Always use `defer` to ensure locks are released
4. **Version Tracking**: Metadata versions enable cache invalidation
5. **Advisory Locks**: All workers must cooperate using the same locking scheme
