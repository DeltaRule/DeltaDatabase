import base64
import socket
import subprocess
import time
from pathlib import Path

import grpc
import pytest
import requests

REPO_ROOT = Path(__file__).resolve().parent.parent


def _free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_for_http(url, timeout=60.0):
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


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def test_subscribe_success_grpc_live(grpc_stub, rsa_key_pair):
    pb2, stub = grpc_stub
    response = stub.Subscribe(
        pb2.SubscribeRequest(worker_id="worker-1", pubkey=rsa_key_pair["pub_pem"])
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


def test_worker_token_expiry_enforced(proto_modules):
    from cryptography.hazmat.backends import default_backend
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric import rsa

    grpc_port = _free_port()
    rest_port = _free_port()
    grpc_addr = f"127.0.0.1:{grpc_port}"
    rest_addr = f"127.0.0.1:{rest_port}"

    proc = subprocess.Popen(
        [
            "go", "run", "./cmd/main-worker",
            f"-grpc-addr={grpc_addr}",
            f"-rest-addr={rest_addr}",
            "-worker-ttl=1s",
        ],
        cwd=str(REPO_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    try:
        if not _wait_for_http(f"http://{rest_addr}/health", timeout=120):
            pytest.skip("Short-TTL server did not start in time")

        pb2, pb2_grpc = proto_modules
        channel = grpc.insecure_channel(grpc_addr)
        stub = pb2_grpc.MainWorkerStub(channel)

        private_key = rsa.generate_private_key(65537, 2048, default_backend())
        pub_pem = private_key.public_key().public_bytes(
            serialization.Encoding.PEM,
            serialization.PublicFormat.SubjectPublicKeyInfo,
        )
        response = stub.Subscribe(
            pb2.SubscribeRequest(worker_id="worker-expiry", pubkey=pub_pem)
        )
        token = response.token

        time.sleep(1.1)

        req = pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="Chat_id",
            schema_id="chat.v1",
            operation="GET",
            payload=b"",
            token=token,
        )
        with pytest.raises(grpc.RpcError) as exc:
            stub.Process(req)
        assert exc.value.code() in {
            grpc.StatusCode.UNAUTHENTICATED,
            grpc.StatusCode.PERMISSION_DENIED,
        }
    finally:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()


@pytest.mark.parametrize("worker_id", [f"worker-{i}" for i in range(200)])
def test_mass_subscription_denial_for_unknown_workers(grpc_stub, worker_id):
    pb2, stub = grpc_stub
    with pytest.raises(grpc.RpcError):
        stub.Subscribe(pb2.SubscribeRequest(worker_id=worker_id, pubkey=b"bad"))

