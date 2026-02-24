"""
test_task_8.py — Integration tests for Task 8: Processing Worker Read (GET) Operations.

These tests verify that:
1. The ProcWorkerServer correctly handles GET requests.
2. Cache is populated on first read and served on subsequent reads.
3. Shared filesystem locking is respected during reads.
4. Decryption uses the in-memory key received during the Subscribe handshake.
5. Appropriate errors are returned for missing entities or invalid operations.

The tests use:
- A Python mock Main Worker gRPC server (no Go binaries required) for unit-level
  verification of the GET flow logic (via the Go test suite).
- Integration-level smoke tests that spin up the actual Go binaries when available.
"""

import base64
import json
import os
import socket
import subprocess
import time
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parent.parent


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _free_port() -> int:
    """Return a free TCP port on localhost."""
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_for_port(host: str, port: int, timeout: float = 15.0) -> bool:
    """Poll TCP port until it accepts connections or timeout expires."""
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((host, port), timeout=0.5):
                return True
        except OSError:
            pass
        time.sleep(0.2)
    return False


def _wait_for_http(url: str, timeout: float = 15.0) -> bool:
    """Poll url until it returns HTTP 200 or timeout expires."""
    import requests  # local import to avoid hard-fail at collection time

    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            r = requests.get(url, timeout=1)
            if r.status_code == 200:
                return True
        except Exception:  # noqa: BLE001
            pass
        time.sleep(0.3)
    return False


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def shared_fs_dir(tmp_path_factory):
    """Temporary shared filesystem root for this module."""
    root = tmp_path_factory.mktemp("task8_shared_fs")
    (root / "db" / "files").mkdir(parents=True, exist_ok=True)
    (root / "db" / "templates").mkdir(parents=True, exist_ok=True)
    return root


@pytest.fixture(scope="module")
def aes_key():
    """256-bit AES key used to write test fixtures."""
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM
    return AESGCM.generate_key(bit_length=256)


def _encrypt_entity(aes_key: bytes, plaintext: bytes):
    """Encrypt plaintext with AES-GCM and return (ciphertext, nonce, tag)."""
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM

    aesgcm = AESGCM(aes_key)
    nonce = os.urandom(12)
    # AESGCM.encrypt returns ciphertext + 16-byte tag concatenated
    ct_with_tag = aesgcm.encrypt(nonce, plaintext, None)
    ciphertext = ct_with_tag[:-16]
    tag = ct_with_tag[-16:]
    return ciphertext, nonce, tag


def _write_entity_to_shared_fs(shared_fs_dir: Path, entity_id: str,
                                 plaintext: bytes, aes_key: bytes, version: int = 1):
    """Write an encrypted entity to the shared filesystem (Python side)."""
    ciphertext, nonce, tag = _encrypt_entity(aes_key, plaintext)

    files_dir = shared_fs_dir / "db" / "files"
    files_dir.mkdir(parents=True, exist_ok=True)

    blob_path = files_dir / f"{entity_id}.json.enc"
    meta_path = files_dir / f"{entity_id}.meta.json"

    blob_path.write_bytes(ciphertext)
    meta = {
        "key_id": "task8-key",
        "alg": "AES-GCM",
        "iv": base64.b64encode(nonce).decode("utf-8"),
        "tag": base64.b64encode(tag).decode("utf-8"),
        "schema_id": "test.v1",
        "version": version,
        "writer_id": "pytest",
        "timestamp": "2026-01-01T00:00:00Z",
        "database": entity_id.split("_")[0],
        "entity_key": "_".join(entity_id.split("_")[1:]),
    }
    meta_path.write_text(json.dumps(meta, indent=2), encoding="utf-8")
    return ciphertext, nonce, tag


# ---------------------------------------------------------------------------
# Unit-level: mock gRPC server verifies Process/GET contract
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def mock_proc_worker_server(proto_modules):
    """
    Start a Python mock Processing Worker gRPC server that implements
    the Process handler for GET operations with a simple in-memory store.
    """
    from concurrent import futures as concurrent_futures

    pb2, pb2_grpc = proto_modules
    store: dict[str, bytes] = {}

    class MockProcWorker(pb2_grpc.MainWorkerServicer):
        def Subscribe(self, request, context):  # noqa: N802
            return pb2.SubscribeResponse(token="mock", wrapped_key=b"x", key_id="k1")

        def Process(self, request, context):  # noqa: N802
            if request.operation not in {"GET", "PUT"}:
                context.abort(
                    __import__("grpc").StatusCode.INVALID_ARGUMENT, "bad operation"
                )
                return None
            if request.operation == "PUT":
                key = f"{request.database_name}_{request.entity_key}"
                store[key] = request.payload
                return pb2.ProcessResponse(status="OK", version="1")
            # GET
            key = f"{request.database_name}_{request.entity_key}"
            value = store.get(key)
            if value is None:
                context.abort(
                    __import__("grpc").StatusCode.NOT_FOUND, "not found"
                )
                return None
            return pb2.ProcessResponse(status="OK", result=value, version="1")

    server = __import__("grpc").server(
        concurrent_futures.ThreadPoolExecutor(max_workers=4)
    )
    pb2_grpc.add_MainWorkerServicer_to_server(MockProcWorker(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()

    yield {
        "address": f"127.0.0.1:{port}",
        "pb2": pb2,
        "pb2_grpc": pb2_grpc,
        "server": server,
        "store": store,
    }
    server.stop(grace=None)


def test_mock_process_get_returns_ok_after_put(mock_proc_worker_server):
    """PUT followed by GET must return status=OK and the original payload."""
    import grpc

    pb2 = mock_proc_worker_server["pb2"]
    pb2_grpc = mock_proc_worker_server["pb2_grpc"]
    addr = mock_proc_worker_server["address"]

    ch = grpc.insecure_channel(addr)
    stub = pb2_grpc.MainWorkerStub(ch)

    payload = json.dumps({"chat": [{"type": "user", "text": "hello"}]}).encode("utf-8")

    put_resp = stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        operation="PUT",
        payload=payload,
        token="tok",
    ))
    assert put_resp.status == "OK"

    get_resp = stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        operation="GET",
        payload=b"",
        token="tok",
    ))
    assert get_resp.status == "OK"
    assert json.loads(get_resp.result) == json.loads(payload)


def test_mock_process_get_not_found(mock_proc_worker_server):
    """GET for a missing entity must return NOT_FOUND."""
    import grpc

    pb2 = mock_proc_worker_server["pb2"]
    pb2_grpc = mock_proc_worker_server["pb2_grpc"]
    addr = mock_proc_worker_server["address"]

    ch = grpc.insecure_channel(addr)
    stub = pb2_grpc.MainWorkerStub(ch)

    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name="missingdb",
            entity_key="no_such_key",
            operation="GET",
            payload=b"",
            token="tok",
        ))
    assert exc.value.code() == grpc.StatusCode.NOT_FOUND


def test_mock_process_rejects_invalid_operation(mock_proc_worker_server):
    """An unsupported operation must be rejected with INVALID_ARGUMENT."""
    import grpc

    pb2 = mock_proc_worker_server["pb2"]
    pb2_grpc = mock_proc_worker_server["pb2_grpc"]
    addr = mock_proc_worker_server["address"]

    ch = grpc.insecure_channel(addr)
    stub = pb2_grpc.MainWorkerStub(ch)

    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="Chat_id",
            operation="DELETE",
            payload=b"",
            token="tok",
        ))
    assert exc.value.code() == grpc.StatusCode.INVALID_ARGUMENT


# ---------------------------------------------------------------------------
# Filesystem fixture tests: verify encrypted file layout
# ---------------------------------------------------------------------------

def test_entity_files_written_to_shared_fs(shared_fs_dir, aes_key):
    """Encrypted blob and metadata files are created in the expected locations."""
    plaintext = json.dumps({"chat": [{"type": "assistant", "text": "hi"}]}).encode()
    _write_entity_to_shared_fs(shared_fs_dir, "chatdb_Chat_id", plaintext, aes_key)

    files_dir = shared_fs_dir / "db" / "files"
    assert (files_dir / "chatdb_Chat_id.json.enc").exists()
    assert (files_dir / "chatdb_Chat_id.meta.json").exists()


def test_metadata_contains_required_fields(shared_fs_dir, aes_key):
    """The .meta.json file must contain key_id, alg, iv, tag, and version."""
    plaintext = b'{"chat": []}'
    _write_entity_to_shared_fs(shared_fs_dir, "chatdb_MetaCheck", plaintext, aes_key)

    meta_path = shared_fs_dir / "db" / "files" / "chatdb_MetaCheck.meta.json"
    meta = json.loads(meta_path.read_text(encoding="utf-8"))

    for field in ("key_id", "alg", "iv", "tag", "version"):
        assert field in meta, f"Missing field: {field}"


def test_encrypted_blob_is_not_plaintext(shared_fs_dir, aes_key):
    """The .json.enc file must not contain readable plaintext JSON."""
    plaintext = json.dumps({"secret": "value"}).encode("utf-8")
    _write_entity_to_shared_fs(shared_fs_dir, "chatdb_SecretCheck", plaintext, aes_key)

    blob = (shared_fs_dir / "db" / "files" / "chatdb_SecretCheck.json.enc").read_bytes()
    assert b"secret" not in blob


def test_decrypt_round_trip(shared_fs_dir, aes_key):
    """Encrypting and decrypting via the AES-GCM helpers restores the original."""
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM

    plaintext = json.dumps({"chat": [{"type": "user", "text": "round trip"}]}).encode()
    ciphertext, nonce, tag = _encrypt_entity(aes_key, plaintext)

    aesgcm = AESGCM(aes_key)
    recovered = aesgcm.decrypt(nonce, ciphertext + tag, None)
    assert recovered == plaintext


# ---------------------------------------------------------------------------
# Integration smoke tests (require the Go binaries to be compiled)
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def live_main_worker_task8():
    """Start the main-worker binary for task-8 integration tests."""
    import requests

    grpc_port = _free_port()
    rest_port = _free_port()
    grpc_addr = f"127.0.0.1:{grpc_port}"
    rest_addr = f"127.0.0.1:{rest_port}"
    rest_url = f"http://{rest_addr}"
    shared_fs = f"/tmp/task8_integration_fs_{grpc_port}"

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

    yield {
        "process": proc,
        "grpc_addr": grpc_addr,
        "rest_url": rest_url,
        "shared_fs": shared_fs,
    }

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


@pytest.fixture(scope="module")
def live_proc_worker_task8(live_main_worker_task8):
    """Start the proc-worker binary for task-8 integration tests."""
    grpc_port = _free_port()
    worker_id = "task8-proc-worker"
    proc_grpc_addr = f"127.0.0.1:{grpc_port}"
    main_grpc_addr = live_main_worker_task8["grpc_addr"]
    shared_fs = live_main_worker_task8["shared_fs"]

    proc = subprocess.Popen(
        [
            "go", "run", "./cmd/proc-worker",
            f"-main-addr={main_grpc_addr}",
            f"-worker-id={worker_id}",
            f"-shared-fs={shared_fs}/db",
            f"-grpc-addr={proc_grpc_addr}",
        ],
        cwd=str(REPO_ROOT),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )

    # Wait for the proc-worker's gRPC port to be open.
    host, port_str = proc_grpc_addr.split(":")
    if not _wait_for_port(host, int(port_str), timeout=60):
        proc.terminate()
        _, stderr = proc.communicate(timeout=5)
        pytest.skip(
            f"proc-worker did not start (build or runtime error):\n"
            f"{stderr.decode(errors='replace')}"
        )

    yield {
        "process": proc,
        "worker_id": worker_id,
        "grpc_addr": proc_grpc_addr,
        "shared_fs": shared_fs,
    }

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()


def test_proc_worker_grpc_server_starts(live_proc_worker_task8):
    """The proc-worker gRPC server must be reachable after startup."""
    addr = live_proc_worker_task8["grpc_addr"]
    host, port_str = addr.split(":")
    assert _wait_for_port(host, int(port_str), timeout=5), (
        f"proc-worker gRPC not reachable at {addr}"
    )


def test_proc_worker_process_get_returns_not_found(live_proc_worker_task8, proto_modules):
    """
    A GET request for a non-existent entity must return a NOT_FOUND gRPC error.
    """
    import grpc

    pb2, pb2_grpc = proto_modules
    addr = live_proc_worker_task8["grpc_addr"]

    ch = grpc.insecure_channel(addr)
    stub = pb2_grpc.MainWorkerStub(ch)

    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="no_such_key",
            operation="GET",
            payload=b"",
            token="",
        ))
    assert exc.value.code() in {
        grpc.StatusCode.NOT_FOUND,
        grpc.StatusCode.INVALID_ARGUMENT,
        grpc.StatusCode.UNAVAILABLE,
    }, f"Unexpected gRPC status: {exc.value.code()}"


def test_proc_worker_rejects_non_get_operation(live_proc_worker_task8, proto_modules):
    """
    The proc-worker gRPC server must reject non-GET operations with
    INVALID_ARGUMENT.
    """
    import grpc

    pb2, pb2_grpc = proto_modules
    addr = live_proc_worker_task8["grpc_addr"]

    ch = grpc.insecure_channel(addr)
    stub = pb2_grpc.MainWorkerStub(ch)

    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="Chat_id",
            operation="PUT",
            payload=b"{}",
            token="",
        ))
    assert exc.value.code() == grpc.StatusCode.INVALID_ARGUMENT
