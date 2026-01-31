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
    response = requests.put(url_put, headers=_auth_header("valid-token"), json=payload, timeout=2)
    assert response.status_code == 200

    url_get = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url_get, headers=_auth_header("valid-token"), timeout=2)
    assert response.status_code == 200
    assert response.json() == payload["Chat_id"]


def test_rest_rejects_bad_json(settings):
    url = _rest_url(settings, "/entity/chatdb")
    response = requests.put(
        url,
        headers=_auth_header("valid-token"),
        data="{not-json}",
        timeout=2,
    )
    assert response.status_code == 400


def test_grpc_process_get_put(grpc_stub):
    pb2, stub = grpc_stub
    put = pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        schema_id="chat.v1",
        operation="PUT",
        payload=json.dumps({"chat": []}).encode("utf-8"),
        token="token",
    )
    get = pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        schema_id="chat.v1",
        operation="GET",
        payload=b"",
        token="token",
    )
    put_resp = stub.Process(put)
    get_resp = stub.Process(get)
    assert put_resp.status
    assert get_resp.status


def test_grpc_rejects_invalid_operation(grpc_stub):
    pb2, stub = grpc_stub
    req = pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        schema_id="chat.v1",
        operation="BAD",
        payload=b"",
        token="token",
    )
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(req)
    assert exc.value.code() in {grpc.StatusCode.INVALID_ARGUMENT, grpc.StatusCode.UNIMPLEMENTED}


def test_admin_workers_endpoint(settings):
    url = _rest_url(settings, "/admin/workers")
    response = requests.get(url, headers=_auth_header("valid-token"), timeout=2)
    assert response.status_code == 200
    data = response.json()
    assert isinstance(data, list)


def test_health_endpoint(settings):
    url = _rest_url(settings, "/health")
    response = requests.get(url, timeout=2)
    assert response.status_code == 200
    assert response.json().get("status") == "ok"
