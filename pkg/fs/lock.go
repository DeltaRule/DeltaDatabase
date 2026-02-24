package fs

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

var (
	// ErrLockFailed is returned when lock acquisition fails
	ErrLockFailed = errors.New("failed to acquire lock")
	// ErrUnlockFailed is returned when lock release fails
	ErrUnlockFailed = errors.New("failed to release lock")
	// ErrAlreadyLocked is returned when trying to lock an already locked file
	ErrAlreadyLocked = errors.New("file is already locked")
)

// LockType represents the type of file lock
type LockType int

const (
	// LockShared represents a shared (read) lock.
	// Multiple readers can hold shared locks simultaneously.
	LockShared LockType = iota
	// LockExclusive represents an exclusive (write) lock.
	// Only one writer can hold an exclusive lock, and no readers can hold locks.
	LockExclusive
)

// FileLock represents a file lock handle
type FileLock struct {
	file     *os.File
	lockType LockType
	mu       sync.Mutex
	locked   bool
}

// NewFileLock creates a new file lock for the given file path.
// The file is opened (or created if it doesn't exist) for locking purposes.
func NewFileLock(filePath string) (*FileLock, error) {
	// Open or create the file for locking.
	// Use O_RDWR to allow both read and write locks.
	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open file for locking: %w", err)
	}

	return &FileLock{
		file:   file,
		locked: false,
	}, nil
}

// Lock acquires a lock on the file.
// lockType specifies whether to acquire a shared (read) or exclusive (write) lock.
func (fl *FileLock) Lock(lockType LockType) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	if fl.locked {
		return ErrAlreadyLocked
	}

	if err := fl.platformLock(lockType, false); err != nil {
		return fmt.Errorf("%w: %v", ErrLockFailed, err)
	}

	fl.locked = true
	fl.lockType = lockType
	return nil
}

// TryLock attempts to acquire a lock without blocking.
// Returns ErrLockFailed if the lock cannot be acquired immediately.
func (fl *FileLock) TryLock(lockType LockType) error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	if fl.locked {
		return ErrAlreadyLocked
	}

	if err := fl.platformLock(lockType, true); err != nil {
		return fmt.Errorf("%w: %v", ErrLockFailed, err)
	}

	fl.locked = true
	fl.lockType = lockType
	return nil
}

// Unlock releases the lock on the file.
func (fl *FileLock) Unlock() error {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	if !fl.locked {
		return nil // Already unlocked
	}

	if err := fl.platformUnlock(); err != nil {
		return fmt.Errorf("%w: %v", ErrUnlockFailed, err)
	}

	fl.locked = false
	return nil
}

// Close releases the lock (if held) and closes the underlying file.
func (fl *FileLock) Close() error {
	// Unlock if still locked
	if fl.locked {
		if err := fl.Unlock(); err != nil {
			// Try to close the file anyway
			fl.file.Close()
			return err
		}
	}

	return fl.file.Close()
}

// IsLocked returns whether the file is currently locked.
func (fl *FileLock) IsLocked() bool {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return fl.locked
}

// LockManager provides a higher-level interface for managing locks on multiple
// files.  It implements the LockBackend interface using POSIX advisory file
// locks, making it suitable for shared POSIX filesystems (local, NFS, CIFS…).
type LockManager struct {
	storage *Storage
	locks   map[string]*FileLock
	mu      sync.Mutex
}

// NewLockManager creates a new lock manager for the given storage
func NewLockManager(storage *Storage) *LockManager {
	return &LockManager{
		storage: storage,
		locks:   make(map[string]*FileLock),
	}
}

// AcquireLock acquires a lock for the given entity ID.
// The lock file is separate from the data file (using .lock extension).
func (lm *LockManager) AcquireLock(id string, lockType LockType) (*FileLock, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	// Create a lock file path (separate from data file)
	lockPath := lm.storage.GetBlobPath(id) + ".lock"

	// Check if we already have a lock for this ID
	if existingLock, exists := lm.locks[id]; exists {
		// If already locked, return error
		if existingLock.IsLocked() {
			return nil, ErrAlreadyLocked
		}
	}

	// Create a new file lock
	lock, err := NewFileLock(lockPath)
	if err != nil {
		return nil, err
	}

	// Acquire the lock
	if err := lock.Lock(lockType); err != nil {
		lock.Close()
		return nil, err
	}

	// Store the lock
	lm.locks[id] = lock

	return lock, nil
}

// TryAcquireLock attempts to acquire a lock without blocking
func (lm *LockManager) TryAcquireLock(id string, lockType LockType) (*FileLock, error) {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lockPath := lm.storage.GetBlobPath(id) + ".lock"

	if existingLock, exists := lm.locks[id]; exists {
		if existingLock.IsLocked() {
			return nil, ErrAlreadyLocked
		}
	}

	lock, err := NewFileLock(lockPath)
	if err != nil {
		return nil, err
	}

	if err := lock.TryLock(lockType); err != nil {
		lock.Close()
		return nil, err
	}

	lm.locks[id] = lock
	return lock, nil
}

// ReleaseLock releases and removes a lock for the given entity ID
func (lm *LockManager) ReleaseLock(id string) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	lock, exists := lm.locks[id]
	if !exists {
		return nil // No lock to release
	}

	if err := lock.Close(); err != nil {
		return err
	}

	delete(lm.locks, id)
	return nil
}

// ReleaseAll releases all locks managed by this LockManager
func (lm *LockManager) ReleaseAll() error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	var lastErr error
	for id, lock := range lm.locks {
		if err := lock.Close(); err != nil {
			lastErr = err
		}
		delete(lm.locks, id)
	}

	return lastErr
}
