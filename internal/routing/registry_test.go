package routing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorkerRegistry(t *testing.T) {
	r := NewWorkerRegistry()
	assert.NotNil(t, r)
	assert.Equal(t, 0, r.Count())
}

func TestWorkerRegistry_Register(t *testing.T) {
	t.Run("registers worker as Available", func(t *testing.T) {
		r := NewWorkerRegistry()
		err := r.Register("worker-1", "key-1", map[string]string{"env": "test"})
		require.NoError(t, err)

		info, exists := r.GetWorker("worker-1")
		assert.True(t, exists)
		assert.Equal(t, "worker-1", info.WorkerID)
		assert.Equal(t, WorkerStatusAvailable, info.Status)
		assert.Equal(t, "key-1", info.KeyID)
		assert.Equal(t, map[string]string{"env": "test"}, info.Tags)
		assert.WithinDuration(t, time.Now(), info.LastSeen, 2*time.Second)
	})

	t.Run("returns error for empty worker ID", func(t *testing.T) {
		r := NewWorkerRegistry()
		err := r.Register("", "key-1", nil)
		assert.Error(t, err)
	})

	t.Run("re-registers existing worker refreshes status", func(t *testing.T) {
		r := NewWorkerRegistry()
		require.NoError(t, r.Register("worker-2", "key-1", nil))
		r.Unregister("worker-2")

		info, _ := r.GetWorker("worker-2")
		assert.Equal(t, WorkerStatusUnavailable, info.Status)

		// Re-register should mark as Available again.
		require.NoError(t, r.Register("worker-2", "key-2", nil))
		info, _ = r.GetWorker("worker-2")
		assert.Equal(t, WorkerStatusAvailable, info.Status)
		assert.Equal(t, "key-2", info.KeyID)
	})
}

func TestWorkerRegistry_Unregister(t *testing.T) {
	t.Run("marks registered worker as Unavailable", func(t *testing.T) {
		r := NewWorkerRegistry()
		require.NoError(t, r.Register("worker-3", "key-1", nil))

		r.Unregister("worker-3")

		info, exists := r.GetWorker("worker-3")
		assert.True(t, exists)
		assert.Equal(t, WorkerStatusUnavailable, info.Status)
	})

	t.Run("is a no-op for unknown worker", func(t *testing.T) {
		r := NewWorkerRegistry()
		// Should not panic.
		r.Unregister("nonexistent")
		assert.Equal(t, 0, r.Count())
	})
}

func TestWorkerRegistry_GetWorker(t *testing.T) {
	t.Run("returns false for unknown worker", func(t *testing.T) {
		r := NewWorkerRegistry()
		_, exists := r.GetWorker("unknown")
		assert.False(t, exists)
	})

	t.Run("returns worker info when registered", func(t *testing.T) {
		r := NewWorkerRegistry()
		require.NoError(t, r.Register("worker-4", "key-1", nil))

		info, exists := r.GetWorker("worker-4")
		assert.True(t, exists)
		assert.NotNil(t, info)
	})
}

func TestWorkerRegistry_ListWorkers(t *testing.T) {
	t.Run("returns empty slice when no workers", func(t *testing.T) {
		r := NewWorkerRegistry()
		workers := r.ListWorkers()
		assert.Empty(t, workers)
	})

	t.Run("returns all workers regardless of status", func(t *testing.T) {
		r := NewWorkerRegistry()
		require.NoError(t, r.Register("worker-a", "key-1", nil))
		require.NoError(t, r.Register("worker-b", "key-1", nil))
		r.Unregister("worker-b")

		workers := r.ListWorkers()
		assert.Len(t, workers, 2)
	})
}

func TestWorkerRegistry_ListAvailableWorkers(t *testing.T) {
	t.Run("returns only Available workers", func(t *testing.T) {
		r := NewWorkerRegistry()
		require.NoError(t, r.Register("worker-c", "key-1", nil))
		require.NoError(t, r.Register("worker-d", "key-1", nil))
		r.Unregister("worker-d")

		available := r.ListAvailableWorkers()
		assert.Len(t, available, 1)
		assert.Equal(t, "worker-c", available[0].WorkerID)
	})
}

func TestWorkerRegistry_Count(t *testing.T) {
	r := NewWorkerRegistry()
	assert.Equal(t, 0, r.Count())

	require.NoError(t, r.Register("worker-x", "key-1", nil))
	assert.Equal(t, 1, r.Count())

	require.NoError(t, r.Register("worker-y", "key-1", nil))
	assert.Equal(t, 2, r.Count())
}

func TestWorkerRegistry_IncrementDecrementLoad(t *testing.T) {
	r := NewWorkerRegistry()
	require.NoError(t, r.Register("worker-load", "key-1", nil))

	r.IncrementLoad("worker-load")
	r.IncrementLoad("worker-load")

	info, _ := r.GetWorker("worker-load")
	assert.Equal(t, int64(2), info.ActiveRequests)

	r.DecrementLoad("worker-load")
	info, _ = r.GetWorker("worker-load")
	assert.Equal(t, int64(1), info.ActiveRequests)

	// Decrement below zero is a no-op.
	r.DecrementLoad("worker-load")
	r.DecrementLoad("worker-load")
	info, _ = r.GetWorker("worker-load")
	assert.Equal(t, int64(0), info.ActiveRequests)

	// Unknown worker is a no-op.
	r.IncrementLoad("nonexistent")
	r.DecrementLoad("nonexistent")
}

func TestWorkerRegistry_UpdateEntityLocation(t *testing.T) {
	r := NewWorkerRegistry()
	require.NoError(t, r.Register("worker-e", "key-1", nil))

	r.UpdateEntityLocation("db/entity1", "worker-e")

	// FindWorkerForEntity should return the worker that served the entity.
	w := r.FindWorkerForEntity("db/entity1")
	require.NotNil(t, w)
	assert.Equal(t, "worker-e", w.WorkerID)

	// Unknown entity returns nil.
	assert.Nil(t, r.FindWorkerForEntity("db/unknown"))
}

func TestWorkerRegistry_FindWorkerForEntity_UnavailableWorker(t *testing.T) {
	r := NewWorkerRegistry()
	require.NoError(t, r.Register("worker-u", "key-1", nil))
	r.UpdateEntityLocation("db/e1", "worker-u")

	// Mark the worker as unavailable.
	r.Unregister("worker-u")

	// Should not return an unavailable worker.
	assert.Nil(t, r.FindWorkerForEntity("db/e1"))
}

func TestWorkerRegistry_FindWorkerForEntity_OverloadedWorker(t *testing.T) {
	r := NewWorkerRegistry()
	require.NoError(t, r.Register("worker-ol", "key-1", nil))
	r.UpdateEntityLocation("db/e2", "worker-ol")

	// Simulate overload by pushing ActiveRequests above the threshold.
	for i := 0; i <= maxWorkerActiveRequests; i++ {
		r.IncrementLoad("worker-ol")
	}

	// Overloaded worker should not be returned for cache-aware routing.
	assert.Nil(t, r.FindWorkerForEntity("db/e2"))
}

func TestWorkerRegistry_FindLeastLoadedWorker(t *testing.T) {
	r := NewWorkerRegistry()

	// No workers → nil.
	assert.Nil(t, r.FindLeastLoadedWorker())

	require.NoError(t, r.Register("worker-l1", "key-1", nil))
	require.NoError(t, r.Register("worker-l2", "key-1", nil))

	r.IncrementLoad("worker-l1")
	r.IncrementLoad("worker-l1")

	// worker-l2 has 0 active requests, so it should be selected.
	w := r.FindLeastLoadedWorker()
	require.NotNil(t, w)
	assert.Equal(t, "worker-l2", w.WorkerID)

	// After making worker-l1 unavailable only worker-l2 is eligible.
	r.Unregister("worker-l2")
	w = r.FindLeastLoadedWorker()
	require.NotNil(t, w)
	assert.Equal(t, "worker-l1", w.WorkerID)

	// All unavailable → nil.
	r.Unregister("worker-l1")
	assert.Nil(t, r.FindLeastLoadedWorker())
}
