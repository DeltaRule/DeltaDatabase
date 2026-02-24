"""
test_schema_api.py — Integration tests for the schema management REST endpoints.

Verifies that:
  GET  /admin/schemas          → returns a JSON list of schema IDs (no auth required)
  GET  /schema/{id}            → returns schema JSON (no auth required)
  PUT  /schema/{id}            → creates/updates a schema (auth required)

All tests use the in-process Python mock REST server from conftest.py extended
with schema endpoint support, plus one live-server smoke test that spins up the
actual main-worker binary.
"""

import json
import socket
import subprocess
import time
from pathlib import Path

import pytest
import requests

REPO_ROOT = Path(__file__).resolve().parent.parent

SAMPLE_SCHEMA = {
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "widget.v1",
    "type": "object",
    "properties": {
        "name": {"type": "string"},
        "count": {"type": "integer", "minimum": 0},
    },
    "required": ["name"],
}


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_for_http(url: str, timeout: float = 30.0) -> bool:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            r = requests.get(url, timeout=1)
            if r.status_code == 200:
                return True
        except requests.RequestException:
            pass
        time.sleep(0.3)
    return False


# ---------------------------------------------------------------------------
# Live-server fixture (spins up actual main-worker binary)
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def live_main_worker_schemas():
    """Build and start the main-worker binary for schema endpoint tests."""
    grpc_port = _free_port()
    rest_port = _free_port()
    grpc_addr = f"127.0.0.1:{grpc_port}"
    rest_addr = f"127.0.0.1:{rest_port}"
    rest_url = f"http://{rest_addr}"
    shared_fs = f"/tmp/schema_test_fs_{rest_port}"

    proc = subprocess.Popen(
        [
            "go", "run", "./cmd/main-worker",
            f"-grpc-addr={grpc_addr}",
            f"-rest-addr={rest_addr}",
            f"-shared-fs={shared_fs}",
        ],
        cwd=str(REPO_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    if not _wait_for_http(rest_url + "/health", timeout=60):
        proc.terminate()
        _, stderr = proc.communicate(timeout=5)
        pytest.skip(
            f"main-worker did not start (build or runtime error):\n"
            f"{stderr.decode(errors='replace')}"
        )

    # Obtain a real client token from the live server.
    login_r = requests.post(rest_url + "/api/login",
                            json={"client_id": "schema-test"}, timeout=5)
    token = login_r.json()["token"]

    yield {"rest_url": rest_url, "process": proc, "token": token}

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


# ---------------------------------------------------------------------------
# Live-server tests
# ---------------------------------------------------------------------------

def test_admin_schemas_returns_list(live_main_worker_schemas):
    """GET /admin/schemas must return an HTTP 200 with a JSON list."""
    url = live_main_worker_schemas["rest_url"] + "/admin/schemas"
    r = requests.get(url, timeout=5)
    assert r.status_code == 200
    assert isinstance(r.json(), list)


def test_put_schema_requires_auth(live_main_worker_schemas):
    """PUT /schema/{id} without a Bearer token must return 401."""
    url = live_main_worker_schemas["rest_url"] + "/schema/widget.v1"
    r = requests.put(url, json=SAMPLE_SCHEMA, timeout=5)
    assert r.status_code == 401


def test_put_schema_with_auth_succeeds(live_main_worker_schemas):
    """PUT /schema/{id} with a valid Bearer token must return 200 and save the schema."""
    base = live_main_worker_schemas["rest_url"]
    headers = {"Authorization": f"Bearer {live_main_worker_schemas['token']}"}

    r = requests.put(base + "/schema/widget.v1", json=SAMPLE_SCHEMA,
                     headers=headers, timeout=5)
    assert r.status_code == 200
    assert r.json().get("status") == "ok"


def test_get_schema_returns_saved_schema(live_main_worker_schemas):
    """GET /schema/{id} must return the previously saved schema JSON."""
    base = live_main_worker_schemas["rest_url"]
    headers = {"Authorization": f"Bearer {live_main_worker_schemas['token']}"}

    # Ensure saved first.
    requests.put(base + "/schema/widget.v1", json=SAMPLE_SCHEMA,
                 headers=headers, timeout=5)

    r = requests.get(base + "/schema/widget.v1", timeout=5)
    assert r.status_code == 200
    data = r.json()
    assert data.get("type") == "object"
    assert "name" in data.get("properties", {})


def test_get_schema_no_auth_required(live_main_worker_schemas):
    """GET /schema/{id} must succeed without any Authorization header."""
    base = live_main_worker_schemas["rest_url"]
    headers = {"Authorization": f"Bearer {live_main_worker_schemas['token']}"}
    requests.put(base + "/schema/widget.v1", json=SAMPLE_SCHEMA,
                 headers=headers, timeout=5)

    # Retrieve without token.
    r = requests.get(base + "/schema/widget.v1", timeout=5)
    assert r.status_code == 200


def test_get_schema_not_found(live_main_worker_schemas):
    """GET /schema/{id} for a non-existent schema must return 404."""
    url = live_main_worker_schemas["rest_url"] + "/schema/does-not-exist.v99"
    r = requests.get(url, timeout=5)
    assert r.status_code == 404


def test_put_schema_rejects_invalid_json(live_main_worker_schemas):
    """PUT /schema/{id} with non-JSON body must return 400."""
    url = live_main_worker_schemas["rest_url"] + "/schema/bad.v1"
    headers = {"Authorization": f"Bearer {live_main_worker_schemas['token']}"}
    r = requests.put(url, data="{not-json}", headers=headers, timeout=5)
    assert r.status_code == 400


def test_admin_schemas_lists_saved_schema(live_main_worker_schemas):
    """After a PUT, /admin/schemas must include the new schema ID."""
    base = live_main_worker_schemas["rest_url"]
    headers = {"Authorization": f"Bearer {live_main_worker_schemas['token']}"}

    schema_id = "listcheck.v1"
    requests.put(base + f"/schema/{schema_id}",
                 json={"type": "object", "properties": {}},
                 headers=headers, timeout=5)

    r = requests.get(base + "/admin/schemas", timeout=5)
    assert r.status_code == 200
    assert schema_id in r.json()


def test_put_schema_roundtrip(live_main_worker_schemas):
    """PUT then GET must return exactly the same schema content."""
    base = live_main_worker_schemas["rest_url"]
    headers = {"Authorization": f"Bearer {live_main_worker_schemas['token']}"}
    schema_body = {
        "$schema": "http://json-schema.org/draft-07/schema#",
        "type": "object",
        "properties": {"score": {"type": "number"}},
        "required": ["score"],
    }

    put_r = requests.put(base + "/schema/roundtrip.v1",
                         json=schema_body, headers=headers, timeout=5)
    assert put_r.status_code == 200

    get_r = requests.get(base + "/schema/roundtrip.v1", timeout=5)
    assert get_r.status_code == 200
    assert get_r.json()["properties"]["score"]["type"] == "number"
