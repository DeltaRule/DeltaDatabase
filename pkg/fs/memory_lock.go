package fs

import "sync"

// MemoryLockManager provides in-process, per-entity exclusive locking.
// It implements the LockBackend interface using Go mutexes rather than
// filesystem advisory locks, making it suitable for storage backends
// (such as S3-compatible object stores) where file-based locking is
// unavailable.
//
// Both LockShared and LockExclusive acquire the same exclusive mutex.
// This is safe for all use-cases: cache hits bypass locking entirely
// (the lock is only needed on a cache-miss read or any write), so the
// loss of true shared-lock concurrency within a single process has
// negligible impact on throughput.
//
// For cross-process/cross-pod synchronisation when using an S3 backend,
// the stale-write guard in the Processing Worker (which compares on-disk
// version numbers before committing) provides the necessary safety net.
type MemoryLockManager struct {
	mu    sync.Mutex
	locks map[string]*memLock
}

// memLock holds a per-entity mutex and tracks whether it is currently held.
type memLock struct {
	mu   sync.Mutex
	held bool // protected by MemoryLockManager.mu
}

// NewMemoryLockManager creates a new MemoryLockManager ready for use.
func NewMemoryLockManager() *MemoryLockManager {
	return &MemoryLockManager{
		locks: make(map[string]*memLock),
	}
}

// get returns (creating if necessary) the memLock for the given entity id.
// The caller must NOT hold m.mu when calling get.
func (m *MemoryLockManager) get(id string) *memLock {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[id]
	if !ok {
		l = &memLock{}
		m.locks[id] = l
	}
	return l
}

// AcquireLock acquires an exclusive in-process lock for id.  The call blocks
// until the lock is available.  The lockType argument is accepted for
// interface compatibility; both LockShared and LockExclusive use the same
// exclusive mutex.
//
// Returns (nil, nil) on success — the *FileLock return value is not
// meaningful for in-memory locks and is always nil.
func (m *MemoryLockManager) AcquireLock(id string, _ LockType) (*FileLock, error) {
	l := m.get(id)
	l.mu.Lock() // blocks until acquired
	m.mu.Lock()
	l.held = true
	m.mu.Unlock()
	return nil, nil
}

// TryAcquireLock attempts to acquire the lock without blocking.
// Returns (nil, ErrAlreadyLocked) if the lock is already held.
func (m *MemoryLockManager) TryAcquireLock(id string, _ LockType) (*FileLock, error) {
	l := m.get(id)
	if !l.mu.TryLock() {
		return nil, ErrAlreadyLocked
	}
	m.mu.Lock()
	l.held = true
	m.mu.Unlock()
	return nil, nil
}

// ReleaseLock releases the lock for id.  It is a no-op if no lock is held.
func (m *MemoryLockManager) ReleaseLock(id string) error {
	m.mu.Lock()
	l, ok := m.locks[id]
	if ok && l.held {
		l.held = false
		m.mu.Unlock()
		l.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	return nil
}

// ReleaseAll releases all currently held locks and removes all lock state.
func (m *MemoryLockManager) ReleaseAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, l := range m.locks {
		if l.held {
			l.held = false
			l.mu.Unlock()
		}
		delete(m.locks, id)
	}
	return nil
}
