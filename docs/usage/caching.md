---
layout: default
title: Caching Model
parent: Usage
nav_order: 8
---

# Caching Model

Each Processing Worker maintains an **LRU (Least Recently Used) in-memory cache** of decrypted entities. The cache is the primary performance lever in DeltaDatabase — most reads in a warm system never touch disk.

---

## Overview

- **Type:** LRU-only eviction. No time-based TTL expiry by default.
- **Scope:** Per-worker, in-memory. Not shared across workers (each worker has its own cache).
- **Key:** `"{database}/{entity_key}"` — e.g., `"chatdb/session_001"`.
- **Value:** Decrypted JSON document + version number.
- **Persistence:** None. The cache is cleared on worker shutdown.

---

## Write Path (PUT)

```
Client PUT request
      │
      ▼
Main Worker (auth + routing)
      │
      ▼
Processing Worker
  1. Validate JSON against schema
  2. Encrypt with AES-256-GCM
  3. Write .json.enc + .meta.json to storage atomically
  4. ✅ Update LRU cache with the new decrypted JSON
```

After a write, the entity is immediately available in cache for subsequent reads.

---

## Read Path (GET)

```
Client GET request
      │
      ▼
Main Worker (auth + routing — prefers worker that last served this entity)
      │
      ▼
Processing Worker
  1. Check LRU cache
     │
     ├─ 🟢 CACHE HIT  → return decrypted JSON (no disk I/O)
     │
     └─ 🔴 CACHE MISS
           │
           ├─ Is cache full?
           │     Yes → evict LRU entry to make room
           │
           ├─ Read .json.enc + .meta.json from storage
           ├─ Check meta.version against any stale cached entry
           ├─ Decrypt with AES-256-GCM
           ├─ Store in cache
           └─ Return decrypted JSON
```

---

## Cache Coherence

Because multiple Processing Workers share the same storage backend, a write by worker A should not serve a stale read from worker B's cache.

DeltaDatabase handles this with **version-based coherence**:

1. Every write increments the entity's `version` in `.meta.json`.
2. When a worker reads an entity from disk, it checks `meta.version` against the cached version.
3. If the versions differ, the disk copy wins and the cache is refreshed.

The Main Worker's **cache-aware routing** reduces cross-worker reads: it routes each entity's requests to the same worker that most recently served it, maximising cache hit rates.

---

## Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `-cache-size` | `256` | Maximum number of entities per worker LRU cache |
| `-cache-ttl` | `0` | TTL per cache entry. `0` = LRU-only eviction |

### Choosing a Cache Size

The optimal cache size depends on your working set:

| Working set | Recommended `-cache-size` |
|------------|--------------------------|
| < 256 entities | `256` (default) — everything fits in cache |
| 256 – 1000 entities | `512` or `1024` |
| > 1000 entities | Increase cache or add more Processing Workers |

Each cached entry holds the full decrypted JSON document in memory. A typical chat session (~50 messages) uses ~10 KB. 256 entries × 10 KB = ~2.5 MB per worker.

### Time-Based Expiry

If you need cache entries to expire after a fixed duration (e.g., for compliance or to bound memory usage), set `-cache-ttl`:

```bash
./bin/proc-worker \
  -cache-size=512 \
  -cache-ttl=10m \
  ...
```

With `-cache-ttl=10m`, entries are evicted after 10 minutes regardless of LRU pressure.

---

## Cache and Multiple Workers

When running multiple Processing Workers:

- Each worker has its **own independent LRU cache**.
- The Main Worker's cache-aware routing minimises cross-worker cache misses.
- If the same entity is accessed by two different workers simultaneously, both may hold a copy in cache — this is expected and safe (version checks ensure coherence).

---

## Benchmark Impact

The LRU cache is the biggest performance lever:

| Scenario | Mean latency |
|----------|-------------|
| GET — warm cache (cache hit) | ~1 ms |
| GET — cold cache (cache miss, disk read + decrypt) | ~3–5 ms |
| PUT — (validate + encrypt + disk write + cache update) | ~1 ms |

See the [Benchmark Results](benchmarks) page for full numbers.
