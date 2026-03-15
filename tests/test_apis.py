import json

import grpc
import pytest
import requests


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def test_rest_get_requires_auth(settings):
    url = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url, timeout=2)
    assert response.status_code in {401, 403}


def test_rest_put_and_get_roundtrip(settings):
    url_put = _rest_url(settings, "/entity/chatdb")
    payload = {"Chat_id": {"chat": [{"type": "assistant", "text": "hi"}]}}
    response = requests.put(url_put, headers=_auth_header(settings["token"]), json=payload, timeout=2)
    assert response.status_code == 200

    url_get = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url_get, headers=_auth_header(settings["token"]), timeout=2)
    assert response.status_code == 200
    assert response.json() == payload["Chat_id"]


def test_rest_rejects_bad_json(settings):
    url = _rest_url(settings, "/entity/chatdb")
    response = requests.put(
        url,
        headers=_auth_header(settings["token"]),
        data="{not-json}",
        timeout=2,
    )
    assert response.status_code == 400


def test_grpc_process_get_put(grpc_stub, grpc_token):
    pb2, stub = grpc_stub
    put = pb2.ProcessRequest(
        schema_id="chat.v1",
        entity_key="Chat_id",
        operation="PUT",
        payload=json.dumps({"chat": []}).encode("utf-8"),
        token=grpc_token,
    )
    get = pb2.ProcessRequest(
        schema_id="chat.v1",
        entity_key="Chat_id",
        operation="GET",
        payload=b"",
        token=grpc_token,
    )
    put_resp = stub.Process(put)
    get_resp = stub.Process(get)
    assert put_resp.status
    assert get_resp.status


def test_grpc_rejects_invalid_operation(grpc_stub, grpc_token):
    pb2, stub = grpc_stub
    req = pb2.ProcessRequest(
        schema_id="chat.v1",
        entity_key="Chat_id",
        operation="BAD",
        payload=b"",
        token=grpc_token,
    )
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(req)
    assert exc.value.code() in {
        grpc.StatusCode.INVALID_ARGUMENT,
        grpc.StatusCode.UNIMPLEMENTED,
        grpc.StatusCode.UNAUTHENTICATED,
    }


def test_admin_workers_endpoint(settings):
    url = _rest_url(settings, "/admin/workers")
    # Requires a valid Bearer token since we added authentication.
    response = requests.get(url, headers=_auth_header(settings["token"]), timeout=2)
    assert response.status_code == 200
    data = response.json()
    assert isinstance(data, list)


def test_admin_workers_shows_connected_worker(settings):
    """After the application starts, at least one Processing Worker must appear
    as Available in GET /admin/workers.

    The conftest live_server fixture starts both the main-worker and a
    proc-worker (worker-id=session-proc-1) and waits for the worker to
    complete its Subscribe handshake before yielding.  This test confirms
    that the worker is actually visible through the REST API.
    """
    url = _rest_url(settings, "/admin/workers")
    # Use the admin key directly as a Bearer token for unambiguous admin access.
    response = requests.get(
        url, headers=_auth_header(settings["admin_key"]), timeout=5
    )
    assert response.status_code == 200

    workers = response.json()
    assert isinstance(workers, list), "/admin/workers must return a JSON array"
    assert len(workers) >= 1, (
        "Expected at least one connected Processing Worker after startup, "
        f"but /admin/workers returned an empty list: {workers}"
    )

    available = [w for w in workers if w.get("status") == "Available"]
    assert available, (
        f"No worker has status 'Available' after startup. Workers: {workers}"
    )

    # In local mode the conftest exposes the specific worker ID it started.
    # Verify that exact worker is present and Available.
    worker_id = settings.get("proc_worker_id", "")
    if worker_id:
        worker_ids = {w.get("worker_id") for w in workers}
        assert worker_id in worker_ids, (
            f"Expected proc-worker '{worker_id}' in the registry but got: {worker_ids}"
        )
        matching = next(w for w in workers if w.get("worker_id") == worker_id)
        assert matching.get("status") == "Available", (
            f"Worker '{worker_id}' is registered but not Available: {matching}"
        )


def test_admin_workers_requires_auth(settings):
    """GET /admin/workers without a token must return 401."""
    url = _rest_url(settings, "/admin/workers")
    response = requests.get(url, timeout=2)
    assert response.status_code in {401, 403}


def test_health_endpoint(settings):
    url = _rest_url(settings, "/health")
    response = requests.get(url, timeout=2)
    assert response.status_code == 200
    assert response.json().get("status") == "ok"

