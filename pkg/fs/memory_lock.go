package fs

import (
	"sync"
)

// MemoryLockManager provides per-entity reader/writer locking using
// in-process mutexes. It satisfies LockManagerInterface and is used by
// backends (such as S3Storage) that have no shared filesystem to place
// lock files on.
//
// Note: MemoryLockManager is single-process only. For multi-process / multi-pod
// deployments sharing an S3 backend, a distributed lock (e.g. backed by
// DynamoDB or Redis) is required to prevent concurrent writes from different
// pods overwriting each other.
type MemoryLockManager struct {
	mu    sync.Mutex
	locks map[string]*memLockEntry
}

// memLockEntry holds the per-entity RWMutex and a reference count so the
// entry can be removed from the map when no goroutine is using it.
type memLockEntry struct {
	rw       sync.RWMutex
	lockType LockType
	refs     int
	locked   bool
}

// NewMemoryLockManager creates a new MemoryLockManager.
func NewMemoryLockManager() *MemoryLockManager {
	return &MemoryLockManager{
		locks: make(map[string]*memLockEntry),
	}
}

// AcquireLock acquires a blocking lock of the given type for entity id.
func (m *MemoryLockManager) AcquireLock(id string, lockType LockType) error {
	entry := m.getOrCreate(id)
	if lockType == LockShared {
		entry.rw.RLock()
	} else {
		entry.rw.Lock()
	}
	m.mu.Lock()
	entry.locked = true
	entry.lockType = lockType
	m.mu.Unlock()
	return nil
}

// TryAcquireLock attempts a non-blocking lock acquisition for entity id.
// Returns ErrLockFailed if the lock is already held by another goroutine.
func (m *MemoryLockManager) TryAcquireLock(id string, lockType LockType) error {
	entry := m.getOrCreate(id)
	var ok bool
	if lockType == LockShared {
		ok = entry.rw.TryRLock()
	} else {
		ok = entry.rw.TryLock()
	}
	if !ok {
		m.decRef(id)
		return ErrLockFailed
	}
	m.mu.Lock()
	entry.locked = true
	entry.lockType = lockType
	m.mu.Unlock()
	return nil
}

// ReleaseLock releases the lock held for entity id.
func (m *MemoryLockManager) ReleaseLock(id string) error {
	m.mu.Lock()
	entry, exists := m.locks[id]
	if !exists || !entry.locked {
		m.mu.Unlock()
		return nil
	}
	entry.locked = false
	lockType := entry.lockType
	m.mu.Unlock()

	if lockType == LockShared {
		entry.rw.RUnlock()
	} else {
		entry.rw.Unlock()
	}
	m.decRef(id)
	return nil
}

// ReleaseAll releases every lock currently held by this manager.
func (m *MemoryLockManager) ReleaseAll() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.locks))
	for id := range m.locks {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		_ = m.ReleaseLock(id) //nolint:errcheck
	}
	return nil
}

// getOrCreate returns (or creates) the entry for the given id, incrementing
// the reference count.
func (m *MemoryLockManager) getOrCreate(id string) *memLockEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.locks[id]
	if !ok {
		entry = &memLockEntry{}
		m.locks[id] = entry
	}
	entry.refs++
	return entry
}

// decRef decrements the reference count for id and removes the entry from the
// map if no goroutine holds a reference.
func (m *MemoryLockManager) decRef(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if entry, ok := m.locks[id]; ok {
		entry.refs--
		if entry.refs <= 0 {
			delete(m.locks, id)
		}
	}
}
