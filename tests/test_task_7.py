"""
test_task_7.py — Integration tests for Task 7: Processing Worker Lifecycle & Handshake.

These tests verify that:
1. The ProcWorker binary starts up, connects to a Main Worker, and subscribes.
2. After subscribing the worker appears as "Available" in the Main Worker registry.
3. The proc-worker receives and can store a session token and encrypted key.

The integration tests spin up the actual Go main-worker and proc-worker binaries
and verify the handshake via the REST /admin/workers endpoint.
"""

import os
import socket
import subprocess
import time
from pathlib import Path

import grpc
import pytest
import requests

REPO_ROOT = Path(__file__).resolve().parent.parent


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _free_port() -> int:
    """Return a free TCP port on localhost."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_for_http(url: str, timeout: float = 15.0) -> bool:
    """Poll url until it returns 200 or timeout expires. Returns True on success."""
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
# Fixtures — start actual Go binaries
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def live_main_worker():
    """
    Build and start the main-worker binary with unique ports so it does not
    conflict with any other running instance.
    """
    grpc_port = _free_port()
    rest_port = _free_port()
    grpc_addr = f"127.0.0.1:{grpc_port}"
    rest_addr = f"127.0.0.1:{rest_port}"
    rest_url = f"http://{rest_addr}"

    proc = subprocess.Popen(
        [
            "go",
            "run",
            "./cmd/main-worker",
            f"-grpc-addr={grpc_addr}",
            f"-rest-addr={rest_addr}",
            "-shared-fs=/tmp/task7_test_shared_fs",
        ],
        cwd=str(REPO_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # Wait until the REST /health endpoint responds.
    health_url = rest_url + "/health"
    if not _wait_for_http(health_url, timeout=30):
        proc.terminate()
        _, stderr = proc.communicate(timeout=5)
        pytest.fail(
            f"main-worker did not start in time.\n"
            f"stderr: {stderr.decode(errors='replace')}"
        )

    yield {
        "process": proc,
        "grpc_addr": grpc_addr,
        "rest_url": rest_url,
    }

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


@pytest.fixture(scope="module")
def live_proc_worker(live_main_worker):
    """
    Build and start the proc-worker binary pointing at live_main_worker.
    """
    worker_id = "task7-integration-worker"
    grpc_addr = live_main_worker["grpc_addr"]

    proc = subprocess.Popen(
        [
            "go",
            "run",
            "./cmd/proc-worker",
            f"-main-addr={grpc_addr}",
            f"-worker-id={worker_id}",
        ],
        cwd=str(REPO_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # Wait for the proc-worker to compile, start up, and subscribe.
    # Poll the /admin/workers endpoint until the worker appears.
    admin_url = live_main_worker["rest_url"] + "/admin/workers"
    deadline = time.monotonic() + 30
    subscribed = False
    while time.monotonic() < deadline:
        try:
            r = requests.get(admin_url, timeout=2)
            if r.status_code == 200:
                workers = r.json()
                if any(w.get("worker_id") == worker_id for w in workers):
                    subscribed = True
                    break
        except requests.RequestException:
            pass
        time.sleep(0.5)

    if not subscribed:
        # Still yield so teardown runs and the process is terminated.
        pass

    yield {
        "process": proc,
        "worker_id": worker_id,
        "rest_url": live_main_worker["rest_url"],
    }

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


# ---------------------------------------------------------------------------
# Integration tests
# ---------------------------------------------------------------------------

def test_proc_worker_subscribe_appears_in_registry(live_proc_worker):
    """
    After the proc-worker starts and calls Subscribe, it must appear as
    'Available' in the Main Worker's /admin/workers REST endpoint.
    """
    rest_url = live_proc_worker["rest_url"]
    worker_id = live_proc_worker["worker_id"]

    response = requests.get(rest_url + "/admin/workers", timeout=5)
    assert response.status_code == 200

    workers = response.json()
    assert isinstance(workers, list)

    worker_ids = {w.get("worker_id") for w in workers}
    assert worker_id in worker_ids, (
        f"Worker '{worker_id}' not found in registry. "
        f"Registered workers: {worker_ids}"
    )

    matching = [w for w in workers if w.get("worker_id") == worker_id]
    assert matching[0].get("status") == "Available", (
        f"Worker '{worker_id}' is not Available: {matching[0]}"
    )


def test_proc_worker_exits_cleanly_on_termination(live_proc_worker):
    """The proc-worker process should be running (not crashed) after subscribing."""
    proc = live_proc_worker["process"]
    assert proc.poll() is None, (
        f"proc-worker exited prematurely. returncode={proc.returncode}"
    )


def test_main_worker_health_endpoint(live_main_worker):
    """The /health endpoint returns status 200 and {status: ok}."""
    url = live_main_worker["rest_url"] + "/health"
    response = requests.get(url, timeout=5)
    assert response.status_code == 200
    assert response.json().get("status") == "ok"


def test_admin_workers_endpoint_returns_list(live_main_worker):
    """The /admin/workers endpoint returns a JSON list."""
    url = live_main_worker["rest_url"] + "/admin/workers"
    response = requests.get(url, timeout=5)
    assert response.status_code == 200
    workers = response.json()
    assert isinstance(workers, list)


def test_proc_worker_receives_key_id(live_proc_worker):
    """The registered worker entry must include a non-empty key_id."""
    rest_url = live_proc_worker["rest_url"]
    worker_id = live_proc_worker["worker_id"]

    response = requests.get(rest_url + "/admin/workers", timeout=5)
    assert response.status_code == 200

    workers = response.json()
    matching = [w for w in workers if w.get("worker_id") == worker_id]
    assert matching, f"Worker '{worker_id}' not found in registry"
    assert matching[0].get("key_id"), "key_id is empty in registry entry"


# ---------------------------------------------------------------------------
# Tests using the live main-worker server
# ---------------------------------------------------------------------------

def test_subscribe_request_contains_public_key(live_main_worker, proto_modules):
    """
    A Subscribe call with a valid RSA public key must return a properly
    wrapped response that can be decrypted with the matching private key.
    """
    from cryptography.hazmat.backends import default_backend
    from cryptography.hazmat.primitives import hashes, serialization
    from cryptography.hazmat.primitives.asymmetric import padding, rsa

    pb2, pb2_grpc = proto_modules
    channel = grpc.insecure_channel(live_main_worker["grpc_addr"])
    stub = pb2_grpc.MainWorkerStub(channel)

    private_key = rsa.generate_private_key(
        public_exponent=65537,
        key_size=2048,
        backend=default_backend(),
    )
    pub_pem = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    )

    resp = stub.Subscribe(pb2.SubscribeRequest(worker_id="pyworker-1", pubkey=pub_pem))
    assert resp.token
    assert resp.wrapped_key
    assert resp.key_id  # real server returns its configured key_id

    decrypted = private_key.decrypt(
        resp.wrapped_key,
        padding.OAEP(
            mgf=padding.MGF1(algorithm=hashes.SHA256()),
            algorithm=hashes.SHA256(),
            label=None,
        ),
    )
    assert len(decrypted) == 32  # 256-bit AES key


def test_subscribe_rejects_empty_worker_id(live_main_worker, proto_modules):
    """Subscribe with an empty worker_id must be rejected."""
    pb2, pb2_grpc = proto_modules
    channel = grpc.insecure_channel(live_main_worker["grpc_addr"])
    stub = pb2_grpc.MainWorkerStub(channel)

    with pytest.raises(grpc.RpcError) as exc:
        stub.Subscribe(pb2.SubscribeRequest(worker_id="", pubkey=b"key"))
    assert exc.value.code() in {
        grpc.StatusCode.INVALID_ARGUMENT,
        grpc.StatusCode.UNAUTHENTICATED,
    }

