// Package routing provides worker routing and registry logic for the Main Worker.
package routing

import (
	"fmt"
	"sync"
	"time"
)

// WorkerStatus represents the operational status of a Processing Worker.
type WorkerStatus string

const (
	// WorkerStatusAvailable means the worker has subscribed and is ready for work.
	WorkerStatusAvailable WorkerStatus = "Available"
	// WorkerStatusUnavailable means the worker is disconnected or has unsubscribed.
	WorkerStatusUnavailable WorkerStatus = "Unavailable"
)

// WorkerInfo holds metadata about a registered Processing Worker.
type WorkerInfo struct {
	// WorkerID is the unique identifier of the Processing Worker.
	WorkerID string `json:"worker_id"`
	// Status is the current operational status of the worker.
	Status WorkerStatus `json:"status"`
	// KeyID is the encryption key ID distributed to this worker.
	KeyID string `json:"key_id"`
	// LastSeen is the time the worker last contacted the Main Worker.
	LastSeen time.Time `json:"last_seen"`
	// Tags are optional metadata labels for the worker.
	Tags map[string]string `json:"tags,omitempty"`
}

// WorkerRegistry tracks the status of registered Processing Workers.
// It is safe for concurrent use.
type WorkerRegistry struct {
	workers map[string]*WorkerInfo
	mu      sync.RWMutex
}

// NewWorkerRegistry creates a new, empty WorkerRegistry.
func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{
		workers: make(map[string]*WorkerInfo),
	}
}

// Register adds or updates a worker in the registry, marking it as Available.
// If the worker already exists its status is refreshed.
func (r *WorkerRegistry) Register(workerID, keyID string, tags map[string]string) error {
	if workerID == "" {
		return fmt.Errorf("worker ID cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.workers[workerID] = &WorkerInfo{
		WorkerID: workerID,
		Status:   WorkerStatusAvailable,
		KeyID:    keyID,
		LastSeen: time.Now(),
		Tags:     tags,
	}
	return nil
}

// Unregister marks a worker as Unavailable without removing it from the registry.
func (r *WorkerRegistry) Unregister(workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if info, exists := r.workers[workerID]; exists {
		info.Status = WorkerStatusUnavailable
	}
}

// GetWorker returns the WorkerInfo for a specific worker.
// Returns nil and false if the worker is not registered.
func (r *WorkerRegistry) GetWorker(workerID string) (*WorkerInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	info, exists := r.workers[workerID]
	return info, exists
}

// ListWorkers returns a snapshot of all registered workers regardless of status.
func (r *WorkerRegistry) ListWorkers() []*WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workers := make([]*WorkerInfo, 0, len(r.workers))
	for _, info := range r.workers {
		workers = append(workers, info)
	}
	return workers
}

// ListAvailableWorkers returns only workers with Available status.
func (r *WorkerRegistry) ListAvailableWorkers() []*WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var workers []*WorkerInfo
	for _, info := range r.workers {
		if info.Status == WorkerStatusAvailable {
			workers = append(workers, info)
		}
	}
	return workers
}

// Count returns the total number of registered workers.
func (r *WorkerRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.workers)
}
