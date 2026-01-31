package cache

import (
	"fmt"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// CacheEntry represents a cached item with metadata.
type CacheEntry struct {
	// Data is the actual cached data (typically decrypted JSON)
	Data []byte
	
	// Version is the file version for coherence checking
	Version string
	
	// Expiry is when this entry should be considered stale
	Expiry time.Time
	
	// CachedAt is when this entry was added to cache
	CachedAt time.Time
	
	// AccessCount tracks how many times this entry was accessed
	AccessCount int64
	
	// LastAccessed tracks the last access time
	LastAccessed time.Time
}

// IsExpired returns true if the entry has passed its expiry time.
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.Expiry)
}

// Cache is a thread-safe LRU cache with TTL support.
type Cache struct {
	cache *lru.Cache[string, *CacheEntry]
	mu    sync.RWMutex
	
	// Configuration
	maxSize    int
	defaultTTL time.Duration
	
	// Statistics
	hits   int64
	misses int64
	evicts int64
}

// CacheConfig holds configuration for the cache.
type CacheConfig struct {
	// MaxSize is the maximum number of entries in the cache
	MaxSize int
	
	// DefaultTTL is the default time-to-live for cache entries
	DefaultTTL time.Duration
}

// NewCache creates a new LRU cache with the given configuration.
func NewCache(config CacheConfig) (*Cache, error) {
	if config.MaxSize <= 0 {
		return nil, fmt.Errorf("max size must be positive, got %d", config.MaxSize)
	}
	
	if config.DefaultTTL <= 0 {
		config.DefaultTTL = 5 * time.Minute // Default: 5 minutes
	}
	
	// Create LRU cache with eviction callback
	lruCache, err := lru.NewWithEvict(config.MaxSize, func(key string, value *CacheEntry) {
		// Eviction callback (for statistics)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create LRU cache: %w", err)
	}
	
	c := &Cache{
		cache:      lruCache,
		maxSize:    config.MaxSize,
		defaultTTL: config.DefaultTTL,
	}
	
	// Start background cleanup goroutine
	go c.cleanupExpired()
	
	return c, nil
}

// Get retrieves a value from the cache.
// Returns the entry and true if found and not expired, nil and false otherwise.
func (c *Cache) Get(key string) (*CacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	entry, found := c.cache.Get(key)
	if !found {
		c.misses++
		return nil, false
	}
	
	// Check if expired
	if entry.IsExpired() {
		c.cache.Remove(key)
		c.misses++
		return nil, false
	}
	
	// Update access statistics
	entry.AccessCount++
	entry.LastAccessed = time.Now()
	c.hits++
	
	return entry, true
}

// Set adds or updates a value in the cache with the default TTL.
func (c *Cache) Set(key string, data []byte, version string) {
	c.SetWithTTL(key, data, version, c.defaultTTL)
}

// SetWithTTL adds or updates a value in the cache with a custom TTL.
func (c *Cache) SetWithTTL(key string, data []byte, version string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	now := time.Now()
	entry := &CacheEntry{
		Data:         data,
		Version:      version,
		Expiry:       now.Add(ttl),
		CachedAt:     now,
		AccessCount:  0,
		LastAccessed: now,
	}
	
	// Check if this is an eviction (LRU full)
	if c.cache.Len() >= c.maxSize {
		c.evicts++
	}
	
	c.cache.Add(key, entry)
}

// Evict removes a specific entry from the cache.
func (c *Cache) Evict(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	removed := c.cache.Remove(key)
	if removed {
		c.evicts++
	}
	return removed
}

// Clear removes all entries from the cache.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.cache.Purge()
	c.hits = 0
	c.misses = 0
	c.evicts = 0
}

// Contains checks if a key exists in the cache (doesn't update LRU).
func (c *Cache) Contains(key string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.cache.Contains(key)
}

// Len returns the number of items in the cache.
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.cache.Len()
}

// Keys returns all keys in the cache (in LRU order).
func (c *Cache) Keys() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	return c.cache.Keys()
}

// Stats returns cache statistics.
func (c *Cache) Stats() CacheStats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	total := c.hits + c.misses
	hitRate := 0.0
	if total > 0 {
		hitRate = float64(c.hits) / float64(total)
	}
	
	return CacheStats{
		Hits:     c.hits,
		Misses:   c.misses,
		Evicts:   c.evicts,
		Size:     int64(c.cache.Len()),
		MaxSize:  int64(c.maxSize),
		HitRate:  hitRate,
	}
}

// CacheStats holds cache performance statistics.
type CacheStats struct {
	Hits     int64   `json:"hits"`
	Misses   int64   `json:"misses"`
	Evicts   int64   `json:"evicts"`
	Size     int64   `json:"size"`
	MaxSize  int64   `json:"max_size"`
	HitRate  float64 `json:"hit_rate"`
}

// cleanupExpired periodically removes expired entries.
func (c *Cache) cleanupExpired() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		c.removeExpiredEntries()
	}
}

// removeExpiredEntries scans and removes all expired entries.
func (c *Cache) removeExpiredEntries() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	keys := c.cache.Keys()
	for _, key := range keys {
		if entry, ok := c.cache.Peek(key); ok {
			if entry.IsExpired() {
				c.cache.Remove(key)
			}
		}
	}
}

// GetOrSet retrieves a value from cache, or sets it using the provided function if not found.
func (c *Cache) GetOrSet(key string, loader func() ([]byte, string, error)) (*CacheEntry, error) {
	// Try to get from cache first
	if entry, found := c.Get(key); found {
		return entry, nil
	}
	
	// Not in cache, load it
	data, version, err := loader()
	if err != nil {
		return nil, err
	}
	
	// Store in cache
	c.Set(key, data, version)
	
	// Return the newly cached entry
	entry, _ := c.Get(key)
	return entry, nil
}

// UpdateVersion updates the version of a cached entry without changing the data.
// Returns false if the key doesn't exist.
func (c *Cache) UpdateVersion(key string, newVersion string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	entry, found := c.cache.Peek(key)
	if !found {
		return false
	}
	
	entry.Version = newVersion
	return true
}

// GetVersion retrieves just the version of a cached entry.
// Returns empty string if not found or expired.
func (c *Cache) GetVersion(key string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entry, found := c.cache.Peek(key)
	if !found {
		return ""
	}
	
	if entry.IsExpired() {
		return ""
	}
	
	return entry.Version
}

// Resize changes the maximum size of the cache.
// This will evict oldest entries if the new size is smaller than current size.
func (c *Cache) Resize(newSize int) error {
	if newSize <= 0 {
		return fmt.Errorf("new size must be positive, got %d", newSize)
	}
	
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.cache.Resize(newSize)
	c.maxSize = newSize
	
	return nil
}

// ResetStats resets all cache statistics.
func (c *Cache) ResetStats() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	c.hits = 0
	c.misses = 0
	c.evicts = 0
}
