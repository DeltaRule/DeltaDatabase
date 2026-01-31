import base64
import json
import os
import random
import socket
import string
import sys
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from importlib import util as importlib_util
from pathlib import Path

from concurrent import futures

import grpc
import pytest
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from grpc_tools import protoc


def pytest_addoption(parser):
    parser.addoption(
        "--rest-url",
        action="store",
        default=os.getenv("DELTADB_REST_URL", "http://127.0.0.1:8080"),
    )
    parser.addoption(
        "--grpc-addr",
        action="store",
        default=os.getenv("DELTADB_GRPC_ADDR", "127.0.0.1:50051"),
    )
    parser.addoption(
        "--shared-fs",
        action="store",
        default=os.getenv("DELTADB_SHARED_FS", ""),
    )
    parser.addoption(
        "--log-path",
        action="store",
        default=os.getenv("DELTADB_LOG_PATH", ""),
    )


@pytest.fixture(scope="session")
def settings(pytestconfig):
    return {
        "rest_url": pytestconfig.getoption("--rest-url"),
        "grpc_addr": pytestconfig.getoption("--grpc-addr"),
        "shared_fs": pytestconfig.getoption("--shared-fs"),
        "log_path": pytestconfig.getoption("--log-path"),
    }


@pytest.fixture(scope="session")
def shared_fs(settings, tmp_path_factory):
    if settings["shared_fs"]:
        root = Path(settings["shared_fs"]).resolve()
        root.mkdir(parents=True, exist_ok=True)
    else:
        root = tmp_path_factory.mktemp("shared_fs")
    files_dir = root / "db" / "files"
    templates_dir = root / "db" / "templates"
    files_dir.mkdir(parents=True, exist_ok=True)
    templates_dir.mkdir(parents=True, exist_ok=True)
    os.environ["DELTADB_SHARED_FS"] = str(root)
    return {
        "root": root,
        "files": files_dir,
        "templates": templates_dir,
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


@pytest.fixture(scope="session")
def proto_modules(tmp_path_factory):
    proto_dir = tmp_path_factory.mktemp("proto")
    proto_path = proto_dir / "worker.proto"
    proto_path.write_text(
        """
        syntax = \"proto3\";
        package deltadb;

        service MainWorker {
          rpc Subscribe(SubscribeRequest) returns (SubscribeResponse);
          rpc Process(ProcessRequest) returns (ProcessResponse);
        }

        message SubscribeRequest {
          string worker_id = 1;
          bytes pubkey = 2;
          map<string, string> tags = 3;
        }

        message SubscribeResponse {
          string token = 1;
          bytes wrapped_key = 2;
          string key_id = 3;
        }

        message ProcessRequest {
          string database_name = 1;
          string entity_key = 2;
          string schema_id = 3;
          string operation = 4; // GET or PUT
          bytes payload = 5;
          string token = 6;
        }

        message ProcessResponse {
          string status = 1;
          bytes result = 2;
          string version = 3;
          string error = 4;
        }
        """.strip(),
        encoding="utf-8",
    )

    out_dir = proto_dir / "gen"
    out_dir.mkdir(parents=True, exist_ok=True)

    protoc.main(
        [
            "grpc_tools.protoc",
            f"-I{proto_dir}",
            f"--python_out={out_dir}",
            f"--grpc_python_out={out_dir}",
            str(proto_path),
        ]
    )

    sys.path.insert(0, str(out_dir))

    pb2 = importlib_util.spec_from_file_location(
        "worker_pb2", out_dir / "worker_pb2.py"
    )
    pb2_mod = importlib_util.module_from_spec(pb2)
    pb2.loader.exec_module(pb2_mod)

    pb2_grpc = importlib_util.spec_from_file_location(
        "worker_pb2_grpc", out_dir / "worker_pb2_grpc.py"
    )
    pb2_grpc_mod = importlib_util.module_from_spec(pb2_grpc)
    pb2_grpc.loader.exec_module(pb2_grpc_mod)

    return pb2_mod, pb2_grpc_mod


@pytest.fixture(scope="session")
def grpc_stub(proto_modules, settings):
    pb2, pb2_grpc = proto_modules
    channel = grpc.insecure_channel(settings["grpc_addr"])
    return pb2, pb2_grpc.MainWorkerStub(channel)


@pytest.fixture(scope="session")
def grpc_mock_server(proto_modules):
    pb2, pb2_grpc = proto_modules

    class MockMainWorker(pb2_grpc.MainWorkerServicer):
        def __init__(self):
            self._key_id = "key-1"

        def Subscribe(self, request, context):
            if not request.worker_id:
                context.abort(grpc.StatusCode.UNAUTHENTICATED, "missing worker_id")
            token = f"token-{request.worker_id}".encode("utf-8")
            wrapped = base64.b64encode(token)
            return pb2.SubscribeResponse(
                token=token.decode("utf-8"),
                wrapped_key=wrapped,
                key_id=self._key_id,
            )

        def Process(self, request, context):
            if request.operation not in {"GET", "PUT"}:
                context.abort(grpc.StatusCode.INVALID_ARGUMENT, "bad operation")
            return pb2.ProcessResponse(status="OK", result=b"{}", version="1")

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=4))
    pb2_grpc.add_MainWorkerServicer_to_server(MockMainWorker(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    yield {
        "address": f"127.0.0.1:{port}",
        "pb2": pb2,
        "pb2_grpc": pb2_grpc,
        "server": server,
    }
    server.stop(grace=None)


def _free_port():
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


class _MockRestHandler(BaseHTTPRequestHandler):
    store = {}
    require_token = True
    valid_token = "valid-token"

    def _send(self, code, body=None):
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        if body is not None:
            self.wfile.write(json.dumps(body).encode("utf-8"))

    def _authorized(self):
        if not self.require_token:
            return True
        auth = self.headers.get("Authorization", "")
        return auth == f"Bearer {self.valid_token}"

    def do_GET(self):
        if not self._authorized():
            self._send(401, {"error": "unauthorized"})
            return
        if self.path.startswith("/entity/"):
            _, _, rest = self.path.partition("/entity/")
            db, _, qs = rest.partition("?")
            key = qs.split("key=")[-1] if "key=" in qs else ""
            value = self.store.get((db, key))
            if value is None:
                self._send(404, {"error": "not_found"})
                return
            self._send(200, value)
            return
        self._send(404, {"error": "unknown"})

    def do_PUT(self):
        if not self._authorized():
            self._send(401, {"error": "unauthorized"})
            return
        content_len = int(self.headers.get("Content-Length", "0"))
        payload = self.rfile.read(content_len) if content_len else b"{}"
        try:
            data = json.loads(payload.decode("utf-8"))
        except json.JSONDecodeError:
            self._send(400, {"error": "bad_json"})
            return
        if self.path.startswith("/entity/"):
            _, _, rest = self.path.partition("/entity/")
            db = rest.strip("/")
            if not data:
                self._send(400, {"error": "empty"})
                return
            key, value = next(iter(data.items()))
            self.store[(db, key)] = value
            self._send(200, {"status": "ok"})
            return
        self._send(404, {"error": "unknown"})

    def log_message(self, format, *args):
        return


@pytest.fixture(scope="session")
def rest_mock_server():
    port = _free_port()
    server = HTTPServer(("127.0.0.1", port), _MockRestHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    yield {
        "base_url": f"http://127.0.0.1:{port}",
        "server": server,
        "store": _MockRestHandler.store,
        "valid_token": _MockRestHandler.valid_token,
    }
    server.shutdown()
    server.server_close()


def random_token(length=32):
    alphabet = string.ascii_letters + string.digits
    return "".join(random.choice(alphabet) for _ in range(length))


def backoff_sleep(attempt):
    time.sleep(min(0.05 * (2**attempt), 1.0))
