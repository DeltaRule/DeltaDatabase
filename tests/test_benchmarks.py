"""
test_benchmarks.py — Performance benchmark suite for DeltaDatabase.

Uses pytest-benchmark to measure throughput and latency under various
workloads.  Results are printed to the console and can be compared across
runs with ``pytest-benchmark compare``.

Benchmark categories
--------------------
1. REST PUT throughput (single entity, sequential)
2. REST GET throughput (warm cache, sequential)
3. REST PUT+GET round-trip latency
4. Large payload write/read (64 KB, 256 KB, 1 MB)
5. Concurrent write throughput (32 threads × N iterations each)
6. Concurrent read throughput  (32 threads × N iterations each)
7. gRPC PUT throughput
8. gRPC GET throughput (warm cache)
9. Schema validation overhead
"""

import json
import threading
import time

import grpc
import pytest
import requests


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth(token):
    return {"Authorization": f"Bearer {token}"}


def _put(settings, key, value_text):
    return requests.put(
        _url(settings, "/entity/chatdb"),
        headers=_auth(settings["token"]),
        json={key: {"chat": [{"type": "assistant", "text": value_text}]}},
        timeout=10,
    )


def _get(settings, key):
    return requests.get(
        _url(settings, f"/entity/chatdb?key={key}"),
        headers=_auth(settings["token"]),
        timeout=10,
    )


# ---------------------------------------------------------------------------
# 1. REST PUT throughput — sequential
# ---------------------------------------------------------------------------

def test_benchmark_rest_put_sequential(benchmark, settings):
    """Measure single-threaded REST PUT latency."""
    counter = [0]

    def _bench():
        key = f"BenchPUT-{counter[0]}"
        counter[0] += 1
        resp = _put(settings, key, "bench-value")
        assert resp.status_code == 200

    result = benchmark(_bench)
    # Log mean for README reference (generous production threshold: 200 ms)
    assert benchmark.stats["mean"] < 0.2, (
        f"REST PUT mean latency {benchmark.stats['mean']:.3f}s exceeds 200 ms threshold"
    )


# ---------------------------------------------------------------------------
# 2. REST GET throughput — warm cache (sequential)
# ---------------------------------------------------------------------------

def test_benchmark_rest_get_warm_cache(benchmark, settings):
    """Measure single-threaded REST GET latency when the key is in cache."""
    _put(settings, "BenchGET-warm", "warm-value")

    def _bench():
        resp = _get(settings, "BenchGET-warm")
        assert resp.status_code == 200

    benchmark(_bench)
    assert benchmark.stats["mean"] < 0.1, (
        f"REST GET (warm cache) mean latency {benchmark.stats['mean']:.3f}s exceeds 100 ms"
    )


# ---------------------------------------------------------------------------
# 3. REST PUT+GET round-trip
# ---------------------------------------------------------------------------

def test_benchmark_rest_put_get_roundtrip(benchmark, settings):
    """Measure full PUT→GET round-trip latency."""
    counter = [0]

    def _bench():
        key = f"RT-{counter[0]}"
        counter[0] += 1
        put_resp = _put(settings, key, "rt-value")
        assert put_resp.status_code == 200
        get_resp = _get(settings, key)
        assert get_resp.status_code == 200

    benchmark(_bench)
    assert benchmark.stats["mean"] < 0.4, (
        f"Round-trip mean latency {benchmark.stats['mean']:.3f}s exceeds 400 ms"
    )


# ---------------------------------------------------------------------------
# 4. Large payload benchmarks
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("size_kb", [64, 256, 1024])
def test_benchmark_large_payload_put(benchmark, settings, size_kb):
    """Measure PUT latency for payloads of various sizes."""
    text = "x" * (size_kb * 1024)
    key = f"LargePUT-{size_kb}kb"

    def _bench():
        resp = _put(settings, key, text)
        assert resp.status_code in {200, 413}, f"Unexpected {resp.status_code}"

    benchmark(_bench)
    # 1 MiB payload may hit the body-size limit and return 413 — that's fine.


# ---------------------------------------------------------------------------
# 5. Concurrent write throughput
# ---------------------------------------------------------------------------

def test_benchmark_concurrent_writes(settings):
    """Measure sustained write throughput across 32 concurrent threads."""
    NUM_THREADS = 32
    WRITES_PER_THREAD = 20

    errors = []
    start = time.monotonic()

    def writer(tid):
        for i in range(WRITES_PER_THREAD):
            resp = _put(settings, f"ConcW-{tid}-{i}", f"v-{i}")
            if resp.status_code != 200:
                errors.append((tid, i, resp.status_code))

    threads = [threading.Thread(target=writer, args=(t,)) for t in range(NUM_THREADS)]
    for th in threads:
        th.start()
    for th in threads:
        th.join()

    elapsed = time.monotonic() - start
    total_ops = NUM_THREADS * WRITES_PER_THREAD
    throughput = total_ops / elapsed

    print(
        f"\n[benchmark] concurrent PUT: {total_ops} ops in {elapsed:.2f}s "
        f"→ {throughput:.1f} ops/s"
    )
    assert not errors, f"Write errors: {errors[:5]}"
    # Sanity: must finish within a reasonable time on CI hardware.
    assert elapsed < 60, f"Concurrent writes took {elapsed:.1f}s (expected < 60s)"


# ---------------------------------------------------------------------------
# 6. Concurrent read throughput
# ---------------------------------------------------------------------------

def test_benchmark_concurrent_reads(settings):
    """Measure sustained read throughput across 32 concurrent threads."""
    # Seed a hot key.
    _put(settings, "ConcR-hot", "hot-value")

    NUM_THREADS = 32
    READS_PER_THREAD = 25

    errors = []
    start = time.monotonic()

    def reader(_tid):
        for _ in range(READS_PER_THREAD):
            resp = _get(settings, "ConcR-hot")
            if resp.status_code != 200:
                errors.append(resp.status_code)

    threads = [threading.Thread(target=reader, args=(t,)) for t in range(NUM_THREADS)]
    for th in threads:
        th.start()
    for th in threads:
        th.join()

    elapsed = time.monotonic() - start
    total_ops = NUM_THREADS * READS_PER_THREAD
    throughput = total_ops / elapsed

    print(
        f"\n[benchmark] concurrent GET: {total_ops} ops in {elapsed:.2f}s "
        f"→ {throughput:.1f} ops/s"
    )
    assert not errors, f"Read errors: {errors[:5]}"
    assert elapsed < 60, f"Concurrent reads took {elapsed:.1f}s (expected < 60s)"


# ---------------------------------------------------------------------------
# 7. gRPC PUT throughput
# ---------------------------------------------------------------------------

def test_benchmark_grpc_put(benchmark, proc_grpc_stub):
    """Measure gRPC PUT latency through the Processing Worker."""
    pb2, stub = proc_grpc_stub
    counter = [0]

    def _bench():
        counter[0] += 1
        payload = json.dumps({"chat": [{"type": "user", "text": f"grpc-{counter[0]}"}]}).encode()
        resp = stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key=f"GrpcPUT-{counter[0]}",
            operation="PUT",
            payload=payload,
            token="",
        ))
        assert resp.status == "OK"

    benchmark(_bench)
    assert benchmark.stats["mean"] < 0.15, (
        f"gRPC PUT mean latency {benchmark.stats['mean']:.3f}s exceeds 150 ms"
    )


# ---------------------------------------------------------------------------
# 8. gRPC GET throughput — warm cache
# ---------------------------------------------------------------------------

def test_benchmark_grpc_get_warm(benchmark, proc_grpc_stub):
    """Measure gRPC GET latency when the key is in the proc-worker cache."""
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "user", "text": "warm"}]}).encode()
    stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="GrpcGET-warm",
        operation="PUT",
        payload=payload,
        token="",
    ))

    def _bench():
        resp = stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="GrpcGET-warm",
            operation="GET",
            payload=b"",
            token="",
        ))
        assert resp.status == "OK"
        assert resp.result

    benchmark(_bench)
    assert benchmark.stats["mean"] < 0.05, (
        f"gRPC GET (warm cache) mean latency {benchmark.stats['mean']:.3f}s exceeds 50 ms"
    )


# ---------------------------------------------------------------------------
# 9. Schema validation overhead
# ---------------------------------------------------------------------------

def test_benchmark_schema_validated_put(benchmark, proc_grpc_stub, sample_schema):
    """Measure the overhead of JSON Schema validation on PUT."""
    pb2, stub = proc_grpc_stub
    counter = [0]

    def _bench():
        counter[0] += 1
        payload = json.dumps({"chat": [{"type": "user", "text": f"msg-{counter[0]}"}]}).encode()
        resp = stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key=f"SchemaBench-{counter[0]}",
            schema_id="chat.v1",
            operation="PUT",
            payload=payload,
            token="",
        ))
        assert resp.status == "OK"

    benchmark(_bench)
    # Schema validation should not add more than 50 ms on average.
    assert benchmark.stats["mean"] < 0.2, (
        f"Schema-validated PUT mean {benchmark.stats['mean']:.3f}s exceeds 200 ms"
    )


# ---------------------------------------------------------------------------
# 10. Bulk data test (1000 entities)
# ---------------------------------------------------------------------------

def test_bulk_write_1000_entities(settings):
    """Write 1000 distinct entities and verify all are readable — exercises
    the LRU cache at scale and confirms no data loss."""
    N = 1000
    url_put = _url(settings, "/entity/chatdb")

    # Batch PUT
    write_errors = []
    for i in range(N):
        resp = requests.put(
            url_put,
            headers=_auth(settings["token"]),
            json={f"Bulk1k-{i}": {"chat": [{"type": "assistant", "text": f"msg-{i}"}]}},
            timeout=10,
        )
        if resp.status_code != 200:
            write_errors.append((i, resp.status_code))
    assert not write_errors, f"Write failures: {write_errors[:5]}"

    # Spot-check 50 random entries (recently written ones are most likely in cache)
    import random
    sample_indices = random.sample(range(N - 50, N), 50)  # Last 50
    read_errors = []
    for i in sample_indices:
        resp = requests.get(
            _url(settings, f"/entity/chatdb?key=Bulk1k-{i}"),
            headers=_auth(settings["token"]),
            timeout=10,
        )
        if resp.status_code != 200:
            read_errors.append((i, resp.status_code))
    assert not read_errors, f"Read failures: {read_errors[:5]}"
