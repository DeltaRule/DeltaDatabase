
# Benchmark Results

The following numbers were measured on a standard CI Linux runner (2 vCPU, 7 GB RAM) with both workers running in-process (`go run`). Real hardware will be faster.

---

## Results

| Benchmark | Mean latency | Throughput |
|-----------|-------------|------------|
| REST PUT (sequential, warm) | ~1 ms | ~1,000 ops/s |
| REST GET (warm cache, sequential) | ~1 ms | ~1,000 ops/s |
| REST PUT→GET round-trip | ~2 ms | ~520 ops/s |
| gRPC PUT (proc-worker direct) | ~0.6 ms | ~1,700 ops/s |
| gRPC GET (warm cache, proc-worker) | ~0.3 ms | ~3,500 ops/s |
| Concurrent PUT (32 threads × 20 each) | — | ~910 ops/s total |
| Concurrent GET (32 threads × 25 each) | — | ~970 ops/s total |
| Bulk write 1,000 entities | ~1 s total | ~1,000 ops/s |

---

## Running the Benchmarks

```bash
# Install Python dependencies (once)
cd tests && pip install -r requirements.txt

# Run all benchmarks sorted by mean latency
pytest tests/test_benchmarks.py -v --benchmark-sort=mean

# Compare against a previous run
pytest-benchmark compare
```

---

## Key Observations

### Cache Hit Rate Is Critical

- **Warm cache GET:** ~1 ms — data served directly from LRU cache, no disk I/O.
- **Cold cache GET:** ~3–5 ms — disk read + AES-GCM decrypt + cache update.

Tuning `-cache-size` to cover your working set is the single most impactful performance optimisation.

### gRPC vs REST

gRPC is ~40–50% faster than REST for the same operation:

| Protocol | GET latency | PUT latency |
|----------|------------|------------|
| REST (HTTP/JSON) | ~1 ms | ~1 ms |
| gRPC (direct to proc-worker) | ~0.3 ms | ~0.6 ms |

For latency-sensitive applications, use the gRPC API directly.

### Horizontal Scaling

Adding more Processing Workers increases concurrent throughput:

| Workers | Concurrent PUT ops/s |
|---------|---------------------|
| 1 | ~910 |
| 2 | ~1,700 (estimated) |
| 4 | ~3,000 (estimated) |

Throughput scales linearly until the shared filesystem becomes the bottleneck. Use S3-compatible storage for higher-throughput multi-worker deployments.

---

## Tracking Regressions

Run benchmarks on your branch and compare against `main`:

```bash
# Run and save baseline
pytest tests/test_benchmarks.py --benchmark-save=baseline

# After changes
pytest tests/test_benchmarks.py --benchmark-compare=baseline
```
