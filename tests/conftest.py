import base64
import json
import os
import socket
import subprocess
import time
from pathlib import Path

import grpc
import pytest
import requests as _requests
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

REPO_ROOT = Path(__file__).resolve().parent.parent


def pytest_addoption(parser):
    parser.addoption(
        "--log-path",
        action="store",
        default=os.getenv("DELTADB_LOG_PATH", ""),
    )
    # Informational options (live_server provides actual addresses)
    parser.addoption(
        "--rest-url",
        action="store",
        default=os.getenv("DELTADB_REST_URL", ""),
    )
    parser.addoption(
        "--grpc-addr",
        action="store",
        default=os.getenv("DELTADB_GRPC_ADDR", ""),
    )
    parser.addoption(
        "--shared-fs",
        action="store",
        default=os.getenv("DELTADB_SHARED_FS", ""),
    )


def _free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _wait_for_http(url, timeout=60.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            r = _requests.get(url, timeout=1)
            if r.status_code == 200:
                return True
        except _requests.RequestException:
            pass
        time.sleep(0.3)
    return False


def _wait_for_port(host, port, timeout=30.0):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((host, port), timeout=0.5):
                return True
        except OSError:
            pass
        time.sleep(0.2)
    return False


@pytest.fixture(scope="session")
def live_server(tmp_path_factory):
    root = tmp_path_factory.mktemp("live_shared_fs")
    db_dir = root / "db"
    (db_dir / "files").mkdir(parents=True, exist_ok=True)
    (db_dir / "templates").mkdir(parents=True, exist_ok=True)

    main_grpc_port = _free_port()
    main_rest_port = _free_port()
    proc_grpc_port = _free_port()

    main_grpc_addr = f"127.0.0.1:{main_grpc_port}"
    main_rest_addr = f"127.0.0.1:{main_rest_port}"
    rest_url = f"http://{main_rest_addr}"
    proc_grpc_addr = f"127.0.0.1:{proc_grpc_port}"

    log_file = root / "server.log"
    log_fp = open(log_file, "w")  # noqa: SIM115

    main_proc = subprocess.Popen(
        [
            "go", "run", "./cmd/main-worker",
            f"-grpc-addr={main_grpc_addr}",
            f"-rest-addr={main_rest_addr}",
            f"-shared-fs={db_dir}",
        ],
        cwd=str(REPO_ROOT),
        stdout=log_fp,
        stderr=log_fp,
    )

    if not _wait_for_http(rest_url + "/health", timeout=120):
        main_proc.terminate()
        log_fp.close()
        pytest.fail(f"main-worker did not start in time. See {log_file}")

    proc_proc = subprocess.Popen(
        [
            "go", "run", "./cmd/proc-worker",
            f"-main-addr={main_grpc_addr}",
            "-worker-id=session-proc-1",
            f"-grpc-addr={proc_grpc_addr}",
            f"-shared-fs={db_dir}",
        ],
        cwd=str(REPO_ROOT),
        stdout=log_fp,
        stderr=log_fp,
    )

    host, port_str = proc_grpc_addr.split(":")
    if not _wait_for_port(host, int(port_str), timeout=60):
        proc_proc.terminate()
        main_proc.terminate()
        log_fp.close()
        pytest.fail(f"proc-worker did not start in time. See {log_file}")

    try:
        r = _requests.post(
            rest_url + "/api/login",
            json={"client_id": "test-session"},
            timeout=5,
        )
        token = r.json()["token"]
    except Exception as exc:  # noqa: BLE001
        proc_proc.terminate()
        main_proc.terminate()
        log_fp.close()
        pytest.fail(f"Failed to obtain client token: {exc}")

    yield {
        "rest_url": rest_url,
        "grpc_addr": main_grpc_addr,
        "proc_grpc_addr": proc_grpc_addr,
        "shared_root": root,
        "db_dir": db_dir,
        "token": token,
        "log_path": str(log_file),
    }

    proc_proc.terminate()
    main_proc.terminate()
    try:
        proc_proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc_proc.kill()
    try:
        main_proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        main_proc.kill()
    log_fp.close()


@pytest.fixture(scope="session")
def settings(pytestconfig, live_server):
    return {
        "rest_url": live_server["rest_url"],
        "grpc_addr": live_server["grpc_addr"],
        "shared_fs": str(live_server["shared_root"]),
        "log_path": live_server["log_path"],
        "token": live_server["token"],
    }


@pytest.fixture(scope="session")
def shared_fs(live_server):
    root = live_server["shared_root"]
    db_dir = live_server["db_dir"]
    os.environ["DELTADB_SHARED_FS"] = str(root)
    return {
        "root": root,
        "files": db_dir / "files",
        "templates": db_dir / "templates",
    }


@pytest.fixture(scope="session")
def sample_schema(shared_fs):
    schema = {
        "$id": "chat.v1",
        "type": "object",
        "properties": {
            "chat": {
                "type": "array",
                "items": {
                    "type": "object",
                    "properties": {
                        "type": {"type": "string"},
                        "text": {"type": "string"},
                    },
                    "required": ["type", "text"],
                },
            }
        },
        "required": ["chat"],
        "additionalProperties": False,
    }
    schema_path = shared_fs["templates"] / "chat.v1.json"
    schema_path.write_text(json.dumps(schema, indent=2), encoding="utf-8")
    return schema_path


@pytest.fixture(scope="session")
def aesgcm_key():
    return AESGCM.generate_key(bit_length=256)


@pytest.fixture(scope="session")
def aesgcm(aesgcm_key):
    return AESGCM(aesgcm_key)


# ---------------------------------------------------------------------------
# Custom JSON-over-gRPC message types and stub
#
# The Go gRPC server uses a custom JSON codec (see api/proto/codec.go).  Go's
# encoding/json represents []byte fields as standard base64.  We mirror that
# here so Python and Go agree on the wire format.
# ---------------------------------------------------------------------------

class _Msg:
    """Minimal gRPC message base: keyword-constructor + attribute access."""

    _bytes_fields: frozenset = frozenset()

    def __init__(self, **kwargs):
        for k, v in kwargs.items():
            setattr(self, k, v)

    def _to_wire(self) -> dict:
        """Convert to a JSON-serialisable dict (base64-encodes bytes fields)."""
        d = {}
        for k, v in self.__dict__.items():
            if v is None:
                continue
            if k in self._bytes_fields and isinstance(v, (bytes, bytearray)):
                if v:  # omit empty bytes (omitempty)
                    d[k] = base64.b64encode(v).decode("ascii")
            elif isinstance(v, str) and v:
                d[k] = v
            elif isinstance(v, dict) and v:
                d[k] = v
            elif not isinstance(v, (str, bytes, bytearray, dict)):
                d[k] = v
        return d

    @classmethod
    def _from_wire(cls, d: dict):
        obj = cls.__new__(cls)
        for k, v in d.items():
            if k in cls._bytes_fields and isinstance(v, str):
                setattr(obj, k, base64.b64decode(v))
            else:
                setattr(obj, k, v)
        # Ensure bytes fields default to b"" if missing
        for f in cls._bytes_fields:
            if not hasattr(obj, f):
                setattr(obj, f, b"")
        return obj


class SubscribeRequest(_Msg):
    _bytes_fields = frozenset(["pubkey"])

    def __init__(self, worker_id: str = "", pubkey: bytes = b"",
                 tags: dict = None):
        self.worker_id = worker_id
        self.pubkey = pubkey
        self.tags = tags or {}


class SubscribeResponse(_Msg):
    _bytes_fields = frozenset(["wrapped_key"])

    def __init__(self, token: str = "", wrapped_key: bytes = b"",
                 key_id: str = ""):
        self.token = token
        self.wrapped_key = wrapped_key
        self.key_id = key_id


class ProcessRequest(_Msg):
    _bytes_fields = frozenset(["payload"])

    def __init__(self, database_name: str = "", entity_key: str = "",
                 schema_id: str = "", operation: str = "",
                 payload: bytes = b"", token: str = ""):
        self.database_name = database_name
        self.entity_key = entity_key
        self.schema_id = schema_id
        self.operation = operation
        self.payload = payload
        self.token = token


class ProcessResponse(_Msg):
    _bytes_fields = frozenset(["result"])

    def __init__(self, status: str = "", result: bytes = b"",
                 version: str = "", error: str = ""):
        self.status = status
        self.result = result
        self.version = version
        self.error = error


def _serialize(msg: _Msg) -> bytes:
    return json.dumps(msg._to_wire()).encode("utf-8")


def _deserialize_subscribe(data: bytes) -> SubscribeResponse:
    return SubscribeResponse._from_wire(json.loads(data.decode("utf-8")))


def _deserialize_process(data: bytes) -> ProcessResponse:
    return ProcessResponse._from_wire(json.loads(data.decode("utf-8")))


class _DeltaDBStub:
    """Python gRPC stub for the DeltaDB MainWorker service.

    Uses JSON serialisation so it is compatible with the Go server's
    custom JSON codec (api/proto/codec.go).
    """

    def __init__(self, channel):
        self.Subscribe = channel.unary_unary(
            "/deltadb.MainWorker/Subscribe",
            request_serializer=_serialize,
            response_deserializer=_deserialize_subscribe,
        )
        self.Process = channel.unary_unary(
            "/deltadb.MainWorker/Process",
            request_serializer=_serialize,
            response_deserializer=_deserialize_process,
        )


class _PseudoPb2:
    """Namespace that looks like a grpc_tools-generated *_pb2 module."""
    SubscribeRequest = SubscribeRequest
    SubscribeResponse = SubscribeResponse
    ProcessRequest = ProcessRequest
    ProcessResponse = ProcessResponse


class _PseudoPb2Grpc:
    """Namespace that looks like a grpc_tools-generated *_pb2_grpc module."""
    MainWorkerStub = _DeltaDBStub


@pytest.fixture(scope="session")
def proto_modules():
    """Return (pb2, pb2_grpc) compatible objects backed by JSON serialisation.

    This replaces the grpc_tools.protoc-generated modules.  The Go gRPC server
    uses a custom JSON codec, so we send/receive JSON instead of binary
    protobuf.
    """
    return _PseudoPb2(), _PseudoPb2Grpc()


@pytest.fixture(scope="session")
def rsa_key_pair():
    from cryptography.hazmat.backends import default_backend
    from cryptography.hazmat.primitives import serialization
    from cryptography.hazmat.primitives.asymmetric import rsa

    private_key = rsa.generate_private_key(65537, 2048, default_backend())
    pub_pem = private_key.public_key().public_bytes(
        serialization.Encoding.PEM,
        serialization.PublicFormat.SubjectPublicKeyInfo,
    )
    return {"private_key": private_key, "pub_pem": pub_pem}


@pytest.fixture(scope="session")
def grpc_token(proto_modules, live_server, rsa_key_pair):
    pb2, pb2_grpc = proto_modules
    channel = grpc.insecure_channel(live_server["grpc_addr"])
    stub = pb2_grpc.MainWorkerStub(channel)
    resp = stub.Subscribe(pb2.SubscribeRequest(
        worker_id="token-provider-fixture",
        pubkey=rsa_key_pair["pub_pem"],
    ))
    return resp.token


@pytest.fixture(scope="session")
def proc_grpc_stub(proto_modules, live_server):
    pb2, pb2_grpc = proto_modules
    channel = grpc.insecure_channel(live_server["proc_grpc_addr"])
    return pb2, pb2_grpc.MainWorkerStub(channel)


@pytest.fixture(scope="session")
def grpc_stub(proto_modules, live_server):
    pb2, pb2_grpc = proto_modules
    channel = grpc.insecure_channel(live_server["grpc_addr"])
    return pb2, pb2_grpc.MainWorkerStub(channel)

