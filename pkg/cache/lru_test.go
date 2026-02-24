package cache

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCache(t *testing.T) {
	t.Run("creates cache with valid config", func(t *testing.T) {
		config := CacheConfig{
			MaxSize:    100,
			DefaultTTL: 5 * time.Minute,
		}
		
		cache, err := NewCache(config)
		
		assert.NoError(t, err)
		assert.NotNil(t, cache)
		assert.Equal(t, 100, cache.maxSize)
		assert.Equal(t, 5*time.Minute, cache.defaultTTL)
	})
	
	t.Run("returns error for invalid max size", func(t *testing.T) {
		config := CacheConfig{
			MaxSize:    0,
			DefaultTTL: 5 * time.Minute,
		}
		
		cache, err := NewCache(config)
		
		assert.Error(t, err)
		assert.Nil(t, cache)
		assert.Contains(t, err.Error(), "must be positive")
	})
	
	t.Run("TTL zero means LRU-only eviction (no time-based expiry)", func(t *testing.T) {
		config := CacheConfig{
			MaxSize:    100,
			DefaultTTL: 0,
		}
		
		cache, err := NewCache(config)
		
		assert.NoError(t, err)
		// DefaultTTL of 0 is preserved — entries never expire by time.
		assert.Equal(t, time.Duration(0), cache.defaultTTL)

		// Verify that an entry with TTL=0 is never reported as expired.
		cache.Set("lru-only", []byte("data"), "v1")
		entry, found := cache.Get("lru-only")
		assert.True(t, found)
		assert.False(t, entry.IsExpired(), "entry with zero TTL must never be considered expired")
	})
}

func TestCacheSetAndGet(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("sets and gets value successfully", func(t *testing.T) {
		data := []byte("test data")
		cache.Set("key1", data, "v1")
		
		entry, found := cache.Get("key1")
		
		assert.True(t, found)
		assert.NotNil(t, entry)
		assert.Equal(t, data, entry.Data)
		assert.Equal(t, "v1", entry.Version)
		assert.Equal(t, int64(1), entry.AccessCount)
	})
	
	t.Run("returns false for non-existent key", func(t *testing.T) {
		entry, found := cache.Get("nonexistent")
		
		assert.False(t, found)
		assert.Nil(t, entry)
	})
	
	t.Run("updates existing entry", func(t *testing.T) {
		cache.Set("key2", []byte("data1"), "v1")
		cache.Set("key2", []byte("data2"), "v2")
		
		entry, found := cache.Get("key2")
		
		assert.True(t, found)
		assert.Equal(t, []byte("data2"), entry.Data)
		assert.Equal(t, "v2", entry.Version)
	})
	
	t.Run("tracks access count", func(t *testing.T) {
		cache.Set("key3", []byte("data"), "v1")
		
		cache.Get("key3")
		cache.Get("key3")
		entry, _ := cache.Get("key3")
		
		assert.Equal(t, int64(3), entry.AccessCount)
	})
}

func TestCacheSetWithTTL(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("sets entry with custom TTL", func(t *testing.T) {
		cache.SetWithTTL("key1", []byte("data"), "v1", 2*time.Hour)
		
		entry, found := cache.Get("key1")
		
		assert.True(t, found)
		assert.True(t, entry.Expiry.After(time.Now().Add(1*time.Hour)))
	})
	
	t.Run("entry expires after TTL", func(t *testing.T) {
		cache.SetWithTTL("key2", []byte("data"), "v1", 50*time.Millisecond)
		
		// Immediately accessible
		entry, found := cache.Get("key2")
		assert.True(t, found)
		assert.NotNil(t, entry)
		
		// Wait for expiration
		time.Sleep(100 * time.Millisecond)
		
		entry, found = cache.Get("key2")
		assert.False(t, found)
		assert.Nil(t, entry)
	})
}

func TestCacheEvict(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("evicts existing entry", func(t *testing.T) {
		cache.Set("key1", []byte("data"), "v1")
		
		removed := cache.Evict("key1")
		
		assert.True(t, removed)
		_, found := cache.Get("key1")
		assert.False(t, found)
	})
	
	t.Run("returns false for non-existent key", func(t *testing.T) {
		removed := cache.Evict("nonexistent")
		
		assert.False(t, removed)
	})
}

func TestCacheClear(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("clears all entries", func(t *testing.T) {
		for i := 0; i < 5; i++ {
			cache.Set(fmt.Sprintf("key%d", i), []byte("data"), "v1")
		}
		
		assert.Equal(t, 5, cache.Len())
		
		cache.Clear()
		
		assert.Equal(t, 0, cache.Len())
	})
	
	t.Run("resets statistics", func(t *testing.T) {
		cache.Set("key1", []byte("data"), "v1")
		cache.Get("key1")
		cache.Get("nonexistent")
		
		stats := cache.Stats()
		assert.Greater(t, stats.Hits, int64(0))
		assert.Greater(t, stats.Misses, int64(0))
		
		cache.Clear()
		
		stats = cache.Stats()
		assert.Equal(t, int64(0), stats.Hits)
		assert.Equal(t, int64(0), stats.Misses)
	})
}

func TestCacheContains(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	cache.Set("key1", []byte("data"), "v1")
	
	t.Run("returns true for existing key", func(t *testing.T) {
		assert.True(t, cache.Contains("key1"))
	})
	
	t.Run("returns false for non-existent key", func(t *testing.T) {
		assert.False(t, cache.Contains("nonexistent"))
	})
}

func TestCacheLen(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("returns correct length", func(t *testing.T) {
		assert.Equal(t, 0, cache.Len())
		
		cache.Set("key1", []byte("data"), "v1")
		assert.Equal(t, 1, cache.Len())
		
		cache.Set("key2", []byte("data"), "v1")
		assert.Equal(t, 2, cache.Len())
		
		cache.Evict("key1")
		assert.Equal(t, 1, cache.Len())
	})
}

func TestCacheKeys(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("returns all keys", func(t *testing.T) {
		cache.Set("key1", []byte("data"), "v1")
		cache.Set("key2", []byte("data"), "v1")
		cache.Set("key3", []byte("data"), "v1")
		
		keys := cache.Keys()
		
		assert.Len(t, keys, 3)
		assert.Contains(t, keys, "key1")
		assert.Contains(t, keys, "key2")
		assert.Contains(t, keys, "key3")
	})
}

func TestCacheStats(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("tracks hits and misses", func(t *testing.T) {
		cache.Set("key1", []byte("data"), "v1")
		
		cache.Get("key1")        // hit
		cache.Get("key1")        // hit
		cache.Get("nonexistent") // miss
		cache.Get("missing")     // miss
		
		stats := cache.Stats()
		
		assert.Equal(t, int64(2), stats.Hits)
		assert.Equal(t, int64(2), stats.Misses)
		assert.Equal(t, 0.5, stats.HitRate)
	})
	
	t.Run("tracks size", func(t *testing.T) {
		cache.Clear()
		
		for i := 0; i < 5; i++ {
			cache.Set(fmt.Sprintf("key%d", i), []byte("data"), "v1")
		}
		
		stats := cache.Stats()
		
		assert.Equal(t, int64(5), stats.Size)
		assert.Equal(t, int64(10), stats.MaxSize)
	})
}

func TestCacheLRUEviction(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    3,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("evicts least recently used entry", func(t *testing.T) {
		cache.Set("key1", []byte("data1"), "v1")
		cache.Set("key2", []byte("data2"), "v1")
		cache.Set("key3", []byte("data3"), "v1")
		
		assert.Equal(t, 3, cache.Len())
		
		// Add one more, should evict key1
		cache.Set("key4", []byte("data4"), "v1")
		
		assert.Equal(t, 3, cache.Len())
		assert.False(t, cache.Contains("key1"))
		assert.True(t, cache.Contains("key2"))
		assert.True(t, cache.Contains("key3"))
		assert.True(t, cache.Contains("key4"))
	})
	
	t.Run("accessing entry updates LRU order", func(t *testing.T) {
		cache.Clear()
		
		cache.Set("key1", []byte("data1"), "v1")
		cache.Set("key2", []byte("data2"), "v1")
		cache.Set("key3", []byte("data3"), "v1")
		
		// Access key1 to make it recently used
		cache.Get("key1")
		
		// Add key4, should evict key2 (oldest)
		cache.Set("key4", []byte("data4"), "v1")
		
		assert.True(t, cache.Contains("key1"))
		assert.False(t, cache.Contains("key2"))
		assert.True(t, cache.Contains("key3"))
		assert.True(t, cache.Contains("key4"))
	})
}

func TestCacheGetOrSet(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("gets existing entry", func(t *testing.T) {
		cache.Set("key1", []byte("data1"), "v1")
		
		entry, err := cache.GetOrSet("key1", func() ([]byte, string, error) {
			return []byte("data2"), "v2", nil
		})
		
		assert.NoError(t, err)
		assert.Equal(t, []byte("data1"), entry.Data)
		assert.Equal(t, "v1", entry.Version)
	})
	
	t.Run("loads and sets missing entry", func(t *testing.T) {
		entry, err := cache.GetOrSet("key2", func() ([]byte, string, error) {
			return []byte("loaded"), "v1", nil
		})
		
		assert.NoError(t, err)
		assert.Equal(t, []byte("loaded"), entry.Data)
		
		// Verify it's now in cache
		cachedEntry, found := cache.Get("key2")
		assert.True(t, found)
		assert.Equal(t, []byte("loaded"), cachedEntry.Data)
	})
	
	t.Run("returns error from loader", func(t *testing.T) {
		entry, err := cache.GetOrSet("key3", func() ([]byte, string, error) {
			return nil, "", fmt.Errorf("load failed")
		})
		
		assert.Error(t, err)
		assert.Nil(t, entry)
		assert.Contains(t, err.Error(), "load failed")
	})
}

func TestCacheUpdateVersion(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("updates version of existing entry", func(t *testing.T) {
		cache.Set("key1", []byte("data"), "v1")
		
		updated := cache.UpdateVersion("key1", "v2")
		
		assert.True(t, updated)
		entry, _ := cache.Get("key1")
		assert.Equal(t, "v2", entry.Version)
	})
	
	t.Run("returns false for non-existent key", func(t *testing.T) {
		updated := cache.UpdateVersion("nonexistent", "v1")
		
		assert.False(t, updated)
	})
}

func TestCacheGetVersion(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("returns version of existing entry", func(t *testing.T) {
		cache.Set("key1", []byte("data"), "v1")
		
		version := cache.GetVersion("key1")
		
		assert.Equal(t, "v1", version)
	})
	
	t.Run("returns empty string for non-existent key", func(t *testing.T) {
		version := cache.GetVersion("nonexistent")
		
		assert.Equal(t, "", version)
	})
	
	t.Run("returns empty string for expired entry", func(t *testing.T) {
		cache.SetWithTTL("key2", []byte("data"), "v1", 50*time.Millisecond)
		
		time.Sleep(100 * time.Millisecond)
		
		version := cache.GetVersion("key2")
		assert.Equal(t, "", version)
	})
}

func TestCacheResize(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    5,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("increases cache size", func(t *testing.T) {
		err := cache.Resize(10)
		
		assert.NoError(t, err)
		assert.Equal(t, 10, cache.maxSize)
	})
	
	t.Run("decreases cache size and evicts entries", func(t *testing.T) {
		cache.Clear()
		
		for i := 0; i < 5; i++ {
			cache.Set(fmt.Sprintf("key%d", i), []byte("data"), "v1")
		}
		
		err := cache.Resize(3)
		
		assert.NoError(t, err)
		assert.Equal(t, 3, cache.maxSize)
		assert.LessOrEqual(t, cache.Len(), 3)
	})
	
	t.Run("returns error for invalid size", func(t *testing.T) {
		err := cache.Resize(0)
		
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "must be positive")
	})
}

func TestCacheResetStats(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    10,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("resets all statistics", func(t *testing.T) {
		cache.Set("key1", []byte("data"), "v1")
		cache.Get("key1")
		cache.Get("nonexistent")
		cache.Evict("key1")
		
		stats := cache.Stats()
		assert.Greater(t, stats.Hits, int64(0))
		assert.Greater(t, stats.Misses, int64(0))
		
		cache.ResetStats()
		
		stats = cache.Stats()
		assert.Equal(t, int64(0), stats.Hits)
		assert.Equal(t, int64(0), stats.Misses)
		assert.Equal(t, int64(0), stats.Evicts)
	})
}

func TestCacheEntryIsExpired(t *testing.T) {
	t.Run("returns false for non-expired entry", func(t *testing.T) {
		entry := &CacheEntry{
			Expiry: time.Now().Add(1 * time.Hour),
		}
		
		assert.False(t, entry.IsExpired())
	})
	
	t.Run("returns true for expired entry", func(t *testing.T) {
		entry := &CacheEntry{
			Expiry: time.Now().Add(-1 * time.Hour),
		}
		
		assert.True(t, entry.IsExpired())
	})
}

func TestConcurrentAccess(t *testing.T) {
	cache, err := NewCache(CacheConfig{
		MaxSize:    100,
		DefaultTTL: 1 * time.Hour,
	})
	require.NoError(t, err)
	
	t.Run("handles concurrent sets", func(t *testing.T) {
		var wg sync.WaitGroup
		errors := make(chan error, 100)
		
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				key := fmt.Sprintf("key%d", id)
				cache.Set(key, []byte(fmt.Sprintf("data%d", id)), "v1")
			}(i)
		}
		
		wg.Wait()
		close(errors)
		
		for err := range errors {
			assert.NoError(t, err)
		}
		
		assert.Equal(t, 100, cache.Len())
	})
	
	t.Run("handles concurrent gets", func(t *testing.T) {
		cache.Clear()
		cache.Set("shared", []byte("data"), "v1")
		
		var wg sync.WaitGroup
		errors := make(chan error, 100)
		
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, found := cache.Get("shared")
				if !found {
					errors <- fmt.Errorf("key not found")
				}
			}()
		}
		
		wg.Wait()
		close(errors)
		
		for err := range errors {
			assert.NoError(t, err)
		}
	})
	
	t.Run("handles mixed concurrent operations", func(t *testing.T) {
		cache.Clear()
		
		var wg sync.WaitGroup
		
		// Writers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cache.Set(fmt.Sprintf("key%d", id), []byte("data"), "v1")
			}(i)
		}
		
		// Readers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cache.Get(fmt.Sprintf("key%d", id))
			}(i)
		}
		
		wg.Wait()
	})
}

func TestCacheExpiredCleanup(t *testing.T) {
	t.Run("background cleanup removes expired entries", func(t *testing.T) {
		cache, err := NewCache(CacheConfig{
			MaxSize:    10,
			DefaultTTL: 1 * time.Hour,
		})
		require.NoError(t, err)
		
		// Add some short-lived entries
		for i := 0; i < 5; i++ {
			cache.SetWithTTL(fmt.Sprintf("short%d", i), []byte("data"), "v1", 100*time.Millisecond)
		}
		
		// Add some long-lived entries
		for i := 0; i < 5; i++ {
			cache.Set(fmt.Sprintf("long%d", i), []byte("data"), "v1")
		}
		
		assert.Equal(t, 10, cache.Len())
		
		// Wait for short-lived entries to expire
		time.Sleep(150 * time.Millisecond)
		
		// Manually trigger cleanup
		cache.removeExpiredEntries()
		
		// Should only have long-lived entries
		assert.Equal(t, 5, cache.Len())
	})
}

func TestCacheClose(t *testing.T) {
	t.Run("stops background cleanup goroutine", func(t *testing.T) {
		cache, err := NewCache(CacheConfig{
			MaxSize:    10,
			DefaultTTL: 1 * time.Hour,
		})
		require.NoError(t, err)

		// Close should not panic
		cache.Close()

		// Calling Close again should not panic (sync.Once protects against double-close)
		assert.NotPanics(t, func() { cache.Close() })
	})

	t.Run("background ticker triggers cleanup", func(t *testing.T) {
		cache, err := NewCache(CacheConfig{
			MaxSize:         10,
			DefaultTTL:      1 * time.Hour,
			CleanupInterval: 50 * time.Millisecond,
		})
		require.NoError(t, err)
		defer cache.Close()

		// Add a short-lived entry
		cache.SetWithTTL("temp", []byte("data"), "v1", 30*time.Millisecond)
		assert.Equal(t, 1, cache.Len())

		// Verify entry is present immediately
		_, found := cache.Get("temp")
		assert.True(t, found)

		// Wait for the entry to expire and the ticker to fire cleanup
		time.Sleep(200 * time.Millisecond)

		// Background cleanup should have removed the expired entry
		assert.Equal(t, 0, cache.Len())
	})
}

func BenchmarkCacheSet(b *testing.B) {
	cache, _ := NewCache(CacheConfig{
		MaxSize:    10000,
		DefaultTTL: 1 * time.Hour,
	})
	
	data := []byte("benchmark data")
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Set(fmt.Sprintf("key%d", i), data, "v1")
	}
}

func BenchmarkCacheGet(b *testing.B) {
	cache, _ := NewCache(CacheConfig{
		MaxSize:    10000,
		DefaultTTL: 1 * time.Hour,
	})
	
	// Pre-populate
	for i := 0; i < 1000; i++ {
		cache.Set(fmt.Sprintf("key%d", i), []byte("data"), "v1")
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(fmt.Sprintf("key%d", i%1000))
	}
}

func BenchmarkCacheConcurrent(b *testing.B) {
	cache, _ := NewCache(CacheConfig{
		MaxSize:    10000,
		DefaultTTL: 1 * time.Hour,
	})
	
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			key := fmt.Sprintf("key%d", i%1000)
			if i%2 == 0 {
				cache.Set(key, []byte("data"), "v1")
			} else {
				cache.Get(key)
			}
			i++
		}
	})
}
