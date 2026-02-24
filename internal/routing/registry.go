// Package routing provides worker routing and registry logic for the Main Worker.
package routing

import (
	"fmt"
	"sync"
	"time"
)

// maxWorkerActiveRequests is the threshold above which a worker is considered
// heavily loaded. When a worker's ActiveRequests exceeds this value it is
// passed over in favour of a less-loaded worker, preventing a single worker
// from becoming a bottleneck while its cache fills up.
const maxWorkerActiveRequests = 100

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
	// ActiveRequests is the number of in-flight requests currently being
	// processed by this worker, as tracked by the Main Worker. When this
	// exceeds maxWorkerActiveRequests the worker is considered overloaded and
	// new requests are routed to a less-loaded worker instead.
	ActiveRequests int64 `json:"active_requests"`
}

// WorkerRegistry tracks the status of registered Processing Workers.
// It is safe for concurrent use.
type WorkerRegistry struct {
	workers map[string]*WorkerInfo
	mu      sync.RWMutex

	// entityWorkerMap maps entity IDs to the worker that last served them.
	// Used by the Main Worker for cache-aware routing: if a worker recently
	// served an entity it likely has it in its LRU cache, so subsequent
	// requests for the same entity should prefer that worker.
	entityWorkerMap map[string]string
}

// NewWorkerRegistry creates a new, empty WorkerRegistry.
func NewWorkerRegistry() *WorkerRegistry {
	return &WorkerRegistry{
		workers:         make(map[string]*WorkerInfo),
		entityWorkerMap: make(map[string]string),
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

// IncrementLoad increments the in-flight request count for a worker.
// It is called by the Main Worker before forwarding a request to the worker.
func (r *WorkerRegistry) IncrementLoad(workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, exists := r.workers[workerID]; exists {
		info.ActiveRequests++
	}
}

// DecrementLoad decrements the in-flight request count for a worker.
// It is called by the Main Worker after a forwarded request completes.
func (r *WorkerRegistry) DecrementLoad(workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, exists := r.workers[workerID]; exists && info.ActiveRequests > 0 {
		info.ActiveRequests--
	}
}

// UpdateEntityLocation records that workerID was the last worker to serve
// entityID. This enables cache-aware routing: the Main Worker can route
// subsequent requests for the same entity to the worker that is most likely
// to have it in its LRU cache.
func (r *WorkerRegistry) UpdateEntityLocation(entityID, workerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entityWorkerMap[entityID] = workerID
}

// FindWorkerForEntity returns the available, non-overloaded worker that last
// served entityID, so that the Main Worker can exploit its LRU cache. Returns
// nil if no such worker exists or the preferred worker is overloaded
// (ActiveRequests > maxWorkerActiveRequests), in which case the caller should
// fall back to FindLeastLoadedWorker.
func (r *WorkerRegistry) FindWorkerForEntity(entityID string) *WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	workerID, ok := r.entityWorkerMap[entityID]
	if !ok {
		return nil
	}
	info, exists := r.workers[workerID]
	if !exists || info.Status != WorkerStatusAvailable {
		return nil
	}
	if info.ActiveRequests > maxWorkerActiveRequests {
		// Worker is under heavy load; prefer a different worker.
		return nil
	}
	return info
}

// FindLeastLoadedWorker returns the available worker with the fewest active
// requests. It is used as the fallback when no cache-preferred worker is
// available or when the preferred worker is overloaded. Returns nil when no
// available workers are registered.
func (r *WorkerRegistry) FindLeastLoadedWorker() *WorkerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var best *WorkerInfo
	for _, info := range r.workers {
		if info.Status != WorkerStatusAvailable {
			continue
		}
		if best == nil || info.ActiveRequests < best.ActiveRequests {
			best = info
		}
	}
	return best
}
