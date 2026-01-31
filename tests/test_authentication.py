import base64
import time

import grpc
import pytest
import requests


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def test_subscribe_success_grpc_live(grpc_stub):
    pb2, stub = grpc_stub
    response = stub.Subscribe(
        pb2.SubscribeRequest(worker_id="worker-1", pubkey=b"pubkey")
    )
    assert response.token
    assert response.wrapped_key
    assert response.key_id


def test_subscribe_invalid_worker_id_grpc_live(grpc_stub):
    pb2, stub = grpc_stub
    with pytest.raises(grpc.RpcError) as exc:
        stub.Subscribe(pb2.SubscribeRequest(worker_id="", pubkey=b"pubkey"))
    assert exc.value.code() in {
        grpc.StatusCode.UNAUTHENTICATED,
        grpc.StatusCode.PERMISSION_DENIED,
        grpc.StatusCode.INVALID_ARGUMENT,
    }


@pytest.mark.parametrize("token", ["", " ", "Bearer", "invalid", "expired", "null"])
def test_rest_missing_or_bad_bearer(settings, token):
    headers = {"Authorization": token} if token else {}
    url = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url, headers=headers, timeout=1)
    assert response.status_code in {401, 403}


@pytest.mark.parametrize(
    "token",
    [
        f"bad-{i}-{base64.b64encode(str(i).encode()).decode()}"
        for i in range(1000)
    ],
)
def test_rest_invalid_token_fuzz(settings, token):
    url = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url, headers=_auth_header(token), timeout=1)
    assert response.status_code in {401, 403}


def test_rest_authorization_scope(settings):
    token = "valid-but-no-scope"
    url = _rest_url(settings, "/entity/secretdb?key=TopSecret")
    response = requests.get(url, headers=_auth_header(token), timeout=1)
    assert response.status_code in {401, 403}


def test_worker_token_expiry_enforced(settings, grpc_stub):
    pb2, stub = grpc_stub
    response = stub.Subscribe(
        pb2.SubscribeRequest(worker_id="worker-expiry", pubkey=b"pubkey")
    )
    time.sleep(1.1)
    req = pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        schema_id="chat.v1",
        operation="GET",
        payload=b"",
        token=response.token,
    )
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(req)
    assert exc.value.code() in {
        grpc.StatusCode.UNAUTHENTICATED,
        grpc.StatusCode.PERMISSION_DENIED,
    }


@pytest.mark.parametrize("worker_id", [f"worker-{i}" for i in range(200)])
def test_mass_subscription_denial_for_unknown_workers(grpc_stub, worker_id):
    pb2, stub = grpc_stub
    with pytest.raises(grpc.RpcError):
        stub.Subscribe(pb2.SubscribeRequest(worker_id=worker_id, pubkey=b"bad"))
