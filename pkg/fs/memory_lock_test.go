package fs

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryLockManager_AcquireRelease(t *testing.T) {
	m := NewMemoryLockManager()

	// Acquire exclusive lock.
	lock, err := m.AcquireLock("entity1", LockExclusive)
	require.NoError(t, err)
	assert.Nil(t, lock, "MemoryLockManager always returns nil *FileLock")

	// Release it.
	err = m.ReleaseLock("entity1")
	require.NoError(t, err)

	// Can be re-acquired after release.
	_, err = m.AcquireLock("entity1", LockExclusive)
	require.NoError(t, err)
	require.NoError(t, m.ReleaseLock("entity1"))
}

func TestMemoryLockManager_SharedLockType(t *testing.T) {
	m := NewMemoryLockManager()

	// LockShared also acquires the exclusive in-process mutex.
	lock, err := m.AcquireLock("entity2", LockShared)
	require.NoError(t, err)
	assert.Nil(t, lock)
	require.NoError(t, m.ReleaseLock("entity2"))
}

func TestMemoryLockManager_TryAcquire_Success(t *testing.T) {
	m := NewMemoryLockManager()

	lock, err := m.TryAcquireLock("tryentity", LockExclusive)
	require.NoError(t, err)
	assert.Nil(t, lock)
	require.NoError(t, m.ReleaseLock("tryentity"))
}

func TestMemoryLockManager_TryAcquire_Fails_WhenLocked(t *testing.T) {
	m := NewMemoryLockManager()

	// Acquire the lock from the main goroutine.
	_, err := m.AcquireLock("conflict", LockExclusive)
	require.NoError(t, err)

	// TryAcquireLock on the same entity from another goroutine must fail immediately.
	done := make(chan error, 1)
	go func() {
		_, e := m.TryAcquireLock("conflict", LockExclusive)
		done <- e
	}()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, ErrAlreadyLocked)
	case <-time.After(time.Second):
		t.Fatal("TryAcquireLock did not return immediately")
	}

	require.NoError(t, m.ReleaseLock("conflict"))
}

func TestMemoryLockManager_Blocks_UntilReleased(t *testing.T) {
	m := NewMemoryLockManager()

	// Acquire lock in main goroutine.
	_, err := m.AcquireLock("blocking", LockExclusive)
	require.NoError(t, err)

	var acquired atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.AcquireLock("blocking", LockExclusive) //nolint:errcheck
		acquired.Store(true)
		m.ReleaseLock("blocking") //nolint:errcheck
	}()

	// Give the goroutine time to block.
	time.Sleep(50 * time.Millisecond)
	assert.False(t, acquired.Load(), "goroutine should be blocked")

	// Release allows the goroutine to proceed.
	require.NoError(t, m.ReleaseLock("blocking"))

	wg.Wait()
	assert.True(t, acquired.Load())
}

func TestMemoryLockManager_IndependentEntities(t *testing.T) {
	m := NewMemoryLockManager()

	// Lock entity A.
	_, err := m.AcquireLock("entityA", LockExclusive)
	require.NoError(t, err)

	// Lock entity B concurrently — must not block.
	done := make(chan error, 1)
	go func() {
		_, e := m.AcquireLock("entityB", LockExclusive)
		done <- e
	}()

	select {
	case err := <-done:
		require.NoError(t, err, "locking a different entity should not block")
	case <-time.After(time.Second):
		t.Fatal("locking entityB blocked while entityA was locked")
	}

	require.NoError(t, m.ReleaseLock("entityA"))
	require.NoError(t, m.ReleaseLock("entityB"))
}

func TestMemoryLockManager_ReleaseAll(t *testing.T) {
	m := NewMemoryLockManager()

	for _, id := range []string{"a", "b", "c"} {
		_, err := m.AcquireLock(id, LockExclusive)
		require.NoError(t, err)
	}

	require.NoError(t, m.ReleaseAll())

	// All locks should be releasable again after ReleaseAll.
	for _, id := range []string{"a", "b", "c"} {
		_, err := m.AcquireLock(id, LockExclusive)
		require.NoError(t, err, "lock %q should be acquirable after ReleaseAll", id)
		require.NoError(t, m.ReleaseLock(id))
	}
}

func TestMemoryLockManager_ReleaseLock_NoOp_WhenNotHeld(t *testing.T) {
	m := NewMemoryLockManager()
	// Releasing a lock that was never acquired must not panic or error.
	err := m.ReleaseLock("ghost")
	assert.NoError(t, err)
}

func TestMemoryLockManager_ImplementsLockBackend(t *testing.T) {
	// Compile-time check surfaced at test time.
	var _ LockBackend = NewMemoryLockManager()
}
