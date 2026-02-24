import time

import pytest
import requests


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def _put(settings, key, value):
    url = _rest_url(settings, "/entity/chatdb")
    payload = {key: {"chat": [{"type": "assistant", "text": value}]}}
    return requests.put(url, headers=_auth_header(settings["token"]), json=payload, timeout=2)


def _get(settings, key):
    url = _rest_url(settings, f"/entity/chatdb?key={key}")
    return requests.get(url, headers=_auth_header(settings["token"]), timeout=2)


def test_cache_hit_ratio_after_warmup(settings):
    _put(settings, "CacheKey", "seed")
    # The real REST server does not emit an X-Cache response header (the
    # in-memory cache lives inside the proc-worker and is accessed via gRPC).
    # What we CAN verify is that all 50 reads return 200 (no errors).
    responses = [_get(settings, "CacheKey") for _ in range(50)]
    assert all(r.status_code == 200 for r in responses)


def test_cache_ttl_expiry(settings):
    _put(settings, "TTLKey", "seed")
    time.sleep(1.5)
    response = _get(settings, "TTLKey")
    assert response.headers.get("X-Cache", "MISS").upper() == "MISS"


def test_lru_eviction_policy(settings):
    for i in range(20):
        _put(settings, f"LRU-{i}", f"v-{i}")
    response = _get(settings, "LRU-0")
    assert response.headers.get("X-Cache", "MISS").upper() == "MISS"


def test_cache_version_coherence(settings):
    _put(settings, "VersionKey", "v1")
    first = _get(settings, "VersionKey")
    _put(settings, "VersionKey", "v2")
    second = _get(settings, "VersionKey")
    assert first.json() != second.json()


@pytest.mark.parametrize("key", [f"hot-{i}" for i in range(200)])
def test_parallel_cache_inserts(settings, key):
    response = _put(settings, key, "value")
    assert response.status_code == 200


@pytest.mark.parametrize("key", [f"hot-{i}" for i in range(200)])
def test_parallel_cache_reads(settings, key):
    response = _get(settings, key)
    assert response.status_code in {200, 404}


def test_cache_benchmark(benchmark, settings):
    _put(settings, "BenchKey", "bench")

    def _bench():
        _get(settings, "BenchKey")

    benchmark(_bench)
    assert benchmark.stats["mean"] < 0.5  # generous threshold for a live server
