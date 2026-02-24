# Cache Package

## Overview
The cache package provides a high-performance, thread-safe LRU (Least Recently Used) cache with TTL (Time To Live) support for the DeltaDatabase system. It is designed to minimize disk I/O and decryption overhead by caching decrypted JSON entities in memory.

## Features
- **LRU Eviction**: Automatically evicts least recently used entries when cache reaches maximum size
- **TTL Support**: Entries expire after configurable time-to-live duration
- **Thread-Safe**: All operations are protected with read-write locks for concurrent access
- **Statistics Tracking**: Monitors cache hits, misses, evictions, and hit rate
- **Background Cleanup**: Automatic removal of expired entries via background goroutine
- **Version Tracking**: Supports version metadata for cache coherence checking
- **Cache-Aside Pattern**: Built-in `GetOrSet` for lazy loading from external sources
- **Dynamic Resizing**: Adjust cache size at runtime

## Architecture

### CacheEntry
Represents a single cached item with metadata:
```go
type CacheEntry struct {
    Data         []byte    // The cached data (e.g., decrypted JSON)
    Version      string    // Version identifier for coherence checking
    Expiry       time.Time // When this entry expires
    CachedAt     time.Time // When this entry was first cached
    AccessCount  int64     // Number of times accessed
    LastAccessed time.Time // Last access timestamp
}
```

### Cache
The main cache structure with configuration:
```go
type Cache struct {
    cache           *lru.Cache[string, *CacheEntry] // Underlying LRU cache
    maxSize         int                             // Maximum number of entries
    defaultTTL      time.Duration                   // Default time-to-live for entries
    cleanupInterval time.Duration                   // Background cleanup ticker interval
    mu              sync.RWMutex                    // Protects concurrent access
    hits            int64                           // Cache hit counter
    misses          int64                           // Cache miss counter
    evicts          int64                           // Eviction counter
    stopCleanup     chan struct{}                    // Signal to stop cleanup goroutine
}
```

## Usage

### Creating a Cache
```go
import "delta-db/pkg/cache"

// Create cache with configuration
config := cache.CacheConfig{
    MaxSize:    1000,              // Maximum 1000 entries
    DefaultTTL: 10 * time.Minute,  // 10 minute default TTL
}

c, err := cache.NewCache(config)
if err != nil {
    log.Fatal(err)
}
defer c.Close() // Stop background cleanup
```

### Basic Operations

#### Set and Get
```go
// Set an entry with default TTL
data := []byte(`{"user_id": "123", "name": "Alice"}`)
c.Set("user:123", data, "v1")

// Get an entry
entry, found := c.Get("user:123")
if found {
    fmt.Printf("Data: %s, Version: %s\n", entry.Data, entry.Version)
}
```

#### Custom TTL
```go
// Set with custom TTL (5 minutes)
c.SetWithTTL("session:abc", sessionData, "v1", 5*time.Minute)
```

#### Eviction
```go
// Manually remove an entry
removed := c.Evict("user:123")
```

#### Clear Cache
```go
// Remove all entries
c.Clear()
```

### Advanced Operations

#### Cache-Aside Pattern
```go
// GetOrSet: get from cache, or load and cache if missing
entry, err := c.GetOrSet("user:123", func() ([]byte, string, error) {
    // This function only called if cache miss
    data, err := loadUserFromDisk("user:123")
    if err != nil {
        return nil, "", err
    }
    return data, "v1", nil
})
```

#### Version Tracking
```go
// Check current version
version := c.GetVersion("user:123")

// Update version (e.g., after write)
c.UpdateVersion("user:123", "v2")
```

#### Cache Statistics
```go
stats := c.Stats()
fmt.Printf("Hits: %d, Misses: %d, Hit Rate: %.2f%%\n", 
    stats.Hits, stats.Misses, stats.HitRate*100)
fmt.Printf("Size: %d/%d, Evictions: %d\n",
    stats.Size, stats.MaxSize, stats.Evicts)
```

#### Dynamic Resizing
```go
// Increase cache size
err := c.Resize(2000)

// Decrease size (will evict LRU entries)
err := c.Resize(500)
```

### Utility Methods

```go
// Check if key exists (doesn't count as hit/miss)
exists := c.Contains("user:123")

// Get all cached keys
keys := c.Keys()

// Get cache size
size := c.Len()

// Reset statistics counters
c.ResetStats()
```

## Configuration

### CacheConfig
```go
type CacheConfig struct {
    MaxSize         int           // Maximum number of entries (required, must be > 0)
    DefaultTTL      time.Duration // Default TTL (optional, defaults to 5 minutes)
    CleanupInterval time.Duration // Background cleanup interval (optional, defaults to 1 minute)
}
```

## Performance Characteristics

### Time Complexity
- `Get`: O(1) average
- `Set`: O(1) average, O(n) when eviction needed
- `Evict`: O(1)
- `Contains`: O(1)
- `Clear`: O(n)

### Space Complexity
- O(n) where n is MaxSize

### Concurrency
- Read operations (`Get`, `Contains`, `GetVersion`, `Stats`) use read locks (multiple concurrent readers)
- Write operations (`Set`, `Evict`, `UpdateVersion`) use write locks (exclusive access)
- Background cleanup runs every 1 minute to remove expired entries

## Integration with DeltaDatabase

### Processing Worker Usage
```go
// Initialize cache for Processing Worker
cache, _ := cache.NewCache(cache.CacheConfig{
    MaxSize:    10000,            // Cache up to 10k entities
    DefaultTTL: 30 * time.Minute, // 30 minute default
})

// On entity read request
entityKey := "chatdb:Chat_123"
entry, err := cache.GetOrSet(entityKey, func() ([]byte, string, error) {
    // Cache miss: load from disk
    encrypted, meta, err := fs.ReadFile(filepath)
    if err != nil {
        return nil, "", err
    }
    
    // Decrypt
    decrypted, err := crypto.Decrypt(encrypted, key)
    if err != nil {
        return nil, "", err
    }
    
    return decrypted, meta.Version, nil
})

// Use the cached/loaded data
return entry.Data
```

### Cache Coherence
When multiple Processing Workers share the same filesystem, use version checking:

```go
// Processing Worker 1: writes entity
newData := []byte(`{"chat": [...]}`)
encrypted, _ := crypto.Encrypt(newData, key)
fs.WriteFile(filepath, encrypted, meta)

// Invalidate/update cache
cache.UpdateVersion("chatdb:Chat_123", "v2")

// Processing Worker 2: reads entity
cachedEntry, _ := cache.Get("chatdb:Chat_123")
metaFromDisk, _ := fs.ReadMetadata(filepath)

if cachedEntry.Version != metaFromDisk.Version {
    // Version mismatch: reload from disk
    cache.Evict("chatdb:Chat_123")
    // ... reload and cache new version
}
```

## Testing
Comprehensive test coverage (99.1%):
```bash
go test ./pkg/cache -v -cover
```

### Test Scenarios
- Cache creation and configuration
- Basic set/get/evict operations
- TTL expiration behavior
- LRU eviction when full
- Concurrent access (100+ goroutines)
- Statistics tracking
- Version management
- Cache-aside pattern
- Dynamic resizing
- Background cleanup

### Benchmarks
```bash
go test ./pkg/cache -bench=. -benchmem
```

## Best Practices

### 1. Size Configuration
- Set `MaxSize` based on available memory and average entity size
- Rule of thumb: `MaxSize * AvgEntitySize * 1.5 < AvailableMemory`
- Monitor hit rate; if < 80%, consider increasing size

### 2. TTL Configuration
- Use shorter TTL for frequently updated entities
- Use longer TTL for read-heavy entities
- Consider access patterns when setting TTL

### 3. Error Handling
```go
// Always check if entry was found
entry, found := cache.Get(key)
if !found {
    // Handle cache miss
}

// Check for expiration explicitly if needed
if entry.IsExpired() {
    // Reload from source
}
```

### 4. Memory Management
```go
// Close cache when shutting down to stop background goroutine
defer cache.Close()

// Periodic cache clearing if memory constrained
if memoryPressure() {
    cache.Clear()
}
```

### 5. Statistics Monitoring
```go
// Periodic monitoring
ticker := time.NewTicker(1 * time.Minute)
go func() {
    for range ticker.C {
        stats := cache.Stats()
        if stats.HitRate < 0.7 {
            log.Warn("Low cache hit rate", "rate", stats.HitRate)
        }
    }
}()
```

## Thread Safety Guarantees
- All public methods are thread-safe
- Safe for concurrent reads and writes from multiple goroutines
- No external synchronization required
- Uses fine-grained locking (RWMutex) for optimal concurrency

## Known Limitations
1. **In-Memory Only**: Cache is not persisted; cleared on restart
2. **No Distributed Coherence**: Each worker maintains independent cache
3. **No Compression**: Data stored as-is in memory
4. **Fixed Eviction Policy**: LRU only (no LFU, FIFO options)

## Dependencies
- `github.com/hashicorp/golang-lru/v2`: High-performance LRU implementation
- Go standard library: `sync`, `time`

## Future Enhancements
- [ ] Compression support for large entries
- [ ] Pub/sub for cache invalidation across workers
- [ ] Configurable eviction policies (LFU, FIFO)
- [ ] Persistence layer for warm restarts
- [ ] Metrics export (Prometheus)
