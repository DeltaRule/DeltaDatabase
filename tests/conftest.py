import json
import os
import socket
import subprocess
import sys
import time
from importlib import util as importlib_util
from pathlib import Path

import grpc
import pytest
import requests as _requests
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from grpc_tools import protoc

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

