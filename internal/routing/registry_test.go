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
