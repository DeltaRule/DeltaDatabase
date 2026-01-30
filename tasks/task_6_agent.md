# Task 6: In-Memory Caching

## Objective
Implement a high-performance LRU cache to minimize disk I/O and decryption overhead.

## Requirements
- Implement `pkg/cache/lru.go`:
  - Cache entries with a TTL.
  - Thread-safe `Get`, `Set`, and `Evict` operations.
- Use `github.com/hashicorp/golang-lru`.
- Structure entry: `Data`, `Version`, `Expiry`.

## Dependencies
- Builds on: [Task 5](task_5_agent.md).
- Validated by: `tests/test_task_6.py`.

## Deliverables
- `pkg/cache/` source files.
- Unit tests for TTL and Eviction.
