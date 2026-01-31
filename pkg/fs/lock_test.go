package fs

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewFileLock(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "test.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)
	require.NotNil(t, lock)
	defer lock.Close()

	assert.False(t, lock.IsLocked())
}

func TestFileLockShared(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "shared.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock.Close()

	// Acquire shared lock
	err = lock.Lock(LockShared)
	require.NoError(t, err)
	assert.True(t, lock.IsLocked())

	// Unlock
	err = lock.Unlock()
	require.NoError(t, err)
	assert.False(t, lock.IsLocked())
}

func TestFileLockExclusive(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "exclusive.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock.Close()

	// Acquire exclusive lock
	err = lock.Lock(LockExclusive)
	require.NoError(t, err)
	assert.True(t, lock.IsLocked())

	// Unlock
	err = lock.Unlock()
	require.NoError(t, err)
	assert.False(t, lock.IsLocked())
}

func TestFileLockAlreadyLocked(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "already_locked.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock.Close()

	// Acquire first lock
	err = lock.Lock(LockShared)
	require.NoError(t, err)

	// Try to acquire again
	err = lock.Lock(LockShared)
	assert.ErrorIs(t, err, ErrAlreadyLocked)
}

func TestFileLockUnlockNotLocked(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "not_locked.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock.Close()

	// Unlock without locking should not error
	err = lock.Unlock()
	assert.NoError(t, err)
}

func TestFileLockClose(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "close.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)

	// Lock
	err = lock.Lock(LockShared)
	require.NoError(t, err)

	// Close should unlock and close file
	err = lock.Close()
	require.NoError(t, err)
	assert.False(t, lock.IsLocked())
}

func TestFileLockTryLockSuccess(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "trylock_success.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock.Close()

	// Try lock should succeed immediately
	err = lock.TryLock(LockShared)
	require.NoError(t, err)
	assert.True(t, lock.IsLocked())
}

func TestFileLockTryLockContention(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "trylock_contention.lock")

	// First lock
	lock1, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock1.Close()

	err = lock1.Lock(LockExclusive)
	require.NoError(t, err)

	// Second lock should fail with TryLock
	lock2, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock2.Close()

	err = lock2.TryLock(LockExclusive)
	assert.ErrorIs(t, err, ErrLockFailed)
}

func TestMultipleSharedLocks(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "multiple_shared.lock")

	// First shared lock
	lock1, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock1.Close()

	err = lock1.Lock(LockShared)
	require.NoError(t, err)

	// Second shared lock should succeed
	lock2, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock2.Close()

	err = lock2.Lock(LockShared)
	require.NoError(t, err)

	// Both should be locked
	assert.True(t, lock1.IsLocked())
	assert.True(t, lock2.IsLocked())
}

func TestExclusiveLockBlocksShared(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "exclusive_blocks_shared.lock")

	// Exclusive lock
	lock1, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock1.Close()

	err = lock1.Lock(LockExclusive)
	require.NoError(t, err)

	// Shared lock should not succeed with TryLock
	lock2, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock2.Close()

	err = lock2.TryLock(LockShared)
	assert.ErrorIs(t, err, ErrLockFailed)
}

func TestExclusiveLockBlocksExclusive(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "exclusive_blocks_exclusive.lock")

	// First exclusive lock
	lock1, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock1.Close()

	err = lock1.Lock(LockExclusive)
	require.NoError(t, err)

	// Second exclusive lock should fail with TryLock
	lock2, err := NewFileLock(lockPath)
	require.NoError(t, err)
	defer lock2.Close()

	err = lock2.TryLock(LockExclusive)
	assert.ErrorIs(t, err, ErrLockFailed)
}

func TestConcurrentSharedLocks(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "concurrent_shared.lock")

	const numReaders = 10
	var wg sync.WaitGroup
	errors := make(chan error, numReaders)

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			lock, err := NewFileLock(lockPath)
			if err != nil {
				errors <- err
				return
			}
			defer lock.Close()

			if err := lock.Lock(LockShared); err != nil {
				errors <- err
				return
			}

			// Hold lock briefly
			time.Sleep(10 * time.Millisecond)

			if err := lock.Unlock(); err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent shared lock failed: %v", err)
	}
}

func TestConcurrentExclusiveLocks(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "concurrent_exclusive.lock")

	const numWriters = 5
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			lock, err := NewFileLock(lockPath)
			if err != nil {
				return
			}
			defer lock.Close()

			// Try to acquire exclusive lock
			if err := lock.TryLock(LockExclusive); err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()

				time.Sleep(10 * time.Millisecond)
				lock.Unlock()
			}
		}()
	}

	wg.Wait()

	// At least one should succeed
	assert.Greater(t, successCount, 0)
}

func TestNewLockManager(t *testing.T) {
	storage, _ := setupTestStorage(t)

	lm := NewLockManager(storage)
	require.NotNil(t, lm)
}

func TestLockManagerAcquireRelease(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	id := "test_entity"

	// Acquire lock
	lock, err := lm.AcquireLock(id, LockExclusive)
	require.NoError(t, err)
	require.NotNil(t, lock)
	assert.True(t, lock.IsLocked())

	// Release lock
	err = lm.ReleaseLock(id)
	require.NoError(t, err)
}

func TestLockManagerAcquireTwice(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	id := "test_entity"

	// Acquire first lock
	lock1, err := lm.AcquireLock(id, LockExclusive)
	require.NoError(t, err)
	require.NotNil(t, lock1)

	// Try to acquire again
	_, err = lm.AcquireLock(id, LockExclusive)
	assert.ErrorIs(t, err, ErrAlreadyLocked)

	// Release
	lm.ReleaseLock(id)
}

func TestLockManagerTryAcquire(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	id := "test_entity"

	// Try acquire should succeed
	lock, err := lm.TryAcquireLock(id, LockShared)
	require.NoError(t, err)
	require.NotNil(t, lock)
	assert.True(t, lock.IsLocked())

	lm.ReleaseLock(id)
}

func TestLockManagerMultipleEntities(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	// Acquire locks on different entities
	lock1, err := lm.AcquireLock("entity1", LockExclusive)
	require.NoError(t, err)
	require.NotNil(t, lock1)

	lock2, err := lm.AcquireLock("entity2", LockExclusive)
	require.NoError(t, err)
	require.NotNil(t, lock2)

	// Both should be locked
	assert.True(t, lock1.IsLocked())
	assert.True(t, lock2.IsLocked())

	// Release both
	lm.ReleaseLock("entity1")
	lm.ReleaseLock("entity2")
}

func TestLockManagerReleaseAll(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	// Acquire multiple locks
	ids := []string{"entity1", "entity2", "entity3"}
	for _, id := range ids {
		_, err := lm.AcquireLock(id, LockShared)
		require.NoError(t, err)
	}

	// Release all
	err := lm.ReleaseAll()
	require.NoError(t, err)
}

func TestLockManagerReleaseNonExistent(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	// Should not error
	err := lm.ReleaseLock("nonexistent")
	assert.NoError(t, err)
}

func TestLockIntegrationWithStorage(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	id := "integration_test"
	blob := []byte("test data")
	metadata := FileMetadata{
		Algorithm: "AES-GCM",
		Version:   1,
	}

	// Acquire exclusive lock
	lock, err := lm.AcquireLock(id, LockExclusive)
	require.NoError(t, err)
	defer lm.ReleaseLock(id)

	// Write file while holding lock
	err = storage.WriteFile(id, blob, metadata)
	require.NoError(t, err)

	// Read file while still holding lock
	fileData, err := storage.ReadFile(id)
	require.NoError(t, err)
	assert.Equal(t, blob, fileData.Blob)

	// Release lock
	err = lock.Unlock()
	require.NoError(t, err)
}

func TestLockFileCreation(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	id := "lock_file_test"

	// Acquire lock
	_, err := lm.AcquireLock(id, LockExclusive)
	require.NoError(t, err)

	// Verify lock file was created
	lockPath := storage.GetBlobPath(id) + ".lock"
	assert.FileExists(t, lockPath)

	lm.ReleaseLock(id)
}

func TestConcurrentLockManagerAccess(t *testing.T) {
	storage, _ := setupTestStorage(t)
	lm := NewLockManager(storage)

	id := "concurrent_test"
	const numGoroutines = 10
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			_, err := lm.TryAcquireLock(id, LockExclusive)
			if err == nil {
				mu.Lock()
				successCount++
				mu.Unlock()

				time.Sleep(5 * time.Millisecond)
				lm.ReleaseLock(id)
			} else {
				// Wait and try again
				time.Sleep(20 * time.Millisecond)
				_, err = lm.TryAcquireLock(id, LockExclusive)
				if err == nil {
					mu.Lock()
					successCount++
					mu.Unlock()
					lm.ReleaseLock(id)
				}
			}
		}()
	}

	wg.Wait()

	// At least some should succeed
	assert.Greater(t, successCount, 0)
}

func TestLockPersistsAcrossProcesses(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "process_lock.lock")

	// Create first lock
	lock1, err := NewFileLock(lockPath)
	require.NoError(t, err)

	err = lock1.Lock(LockExclusive)
	require.NoError(t, err)

	// Create second lock (simulating another process)
	lock2, err := NewFileLock(lockPath)
	require.NoError(t, err)

	// Should not be able to acquire lock
	err = lock2.TryLock(LockExclusive)
	assert.ErrorIs(t, err, ErrLockFailed)

	// Release first lock
	lock1.Unlock()
	lock1.Close()

	// Now second lock should succeed
	err = lock2.Lock(LockExclusive)
	require.NoError(t, err)

	lock2.Close()
}

func TestFileLockInvalidPath(t *testing.T) {
	// Try to create lock in non-existent directory (without creating it)
	invalidPath := filepath.Join("Z:\\nonexistent\\directory", "test.lock")

	_, err := NewFileLock(invalidPath)
	assert.Error(t, err)
}

func TestLockFileCleanup(t *testing.T) {
	tempDir := t.TempDir()
	lockPath := filepath.Join(tempDir, "cleanup.lock")

	lock, err := NewFileLock(lockPath)
	require.NoError(t, err)

	err = lock.Lock(LockShared)
	require.NoError(t, err)

	// Close should clean up
	err = lock.Close()
	require.NoError(t, err)

	// Lock file should still exist (it's the lock file itself)
	assert.FileExists(t, lockPath)
}

// Benchmark tests
func BenchmarkLockUnlock(b *testing.B) {
	tempDir := b.TempDir()
	lockPath := filepath.Join(tempDir, "bench.lock")

	lock, _ := NewFileLock(lockPath)
	defer lock.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = lock.Lock(LockExclusive)
		_ = lock.Unlock()
	}
}

func BenchmarkLockManagerAcquireRelease(b *testing.B) {
	tempDir := b.TempDir()
	storage, _ := NewStorage(filepath.Join(tempDir, "bench_db"))
	lm := NewLockManager(storage)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := "bench_entity"
		_, _ = lm.AcquireLock(id, LockExclusive)
		_ = lm.ReleaseLock(id)
	}
}
