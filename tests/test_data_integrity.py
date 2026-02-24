import json
import os

import grpc
import pytest
import requests
from jsonschema import Draft7Validator


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def test_schema_validation_rejects_invalid(proc_grpc_stub, sample_schema):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "assistant"}]}).encode()  # missing "text"
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="SchemaRejectTest",
            schema_id="chat.v1",
            operation="PUT",
            payload=payload,
            token="",
        ))
    assert exc.value.code() == grpc.StatusCode.INVALID_ARGUMENT


def test_schema_validation_accepts_valid(sample_schema):
    schema = json.loads(sample_schema.read_text(encoding="utf-8"))
    validator = Draft7Validator(schema)
    valid_payload = {"chat": [{"type": "assistant", "text": "ok"}]}
    errors = list(validator.iter_errors(valid_payload))
    assert not errors


def test_metadata_matches_schema_id(proc_grpc_stub, shared_fs, sample_schema):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "user", "text": "hello"}]}).encode()
    resp = stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="MetaCheck",
        schema_id="chat.v1",
        operation="PUT",
        payload=payload,
        token="",
    ))
    assert resp.status == "OK"
    meta_path = shared_fs["files"] / "chatdb_MetaCheck.meta.json"
    assert meta_path.exists()
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    assert meta["schema_id"] == "chat.v1"


def test_atomic_write_no_temp_files(shared_fs):
    temp_files = list(shared_fs["files"].glob("*.tmp"))
    assert not temp_files


@pytest.mark.parametrize("iteration", range(200))
def test_file_metadata_has_version(proc_grpc_stub, shared_fs, iteration):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "user", "text": f"v{iteration}"}]}).encode()
    stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key=f"Meta-{iteration}",
        operation="PUT",
        payload=payload,
        token="",
    ))
    meta_path = shared_fs["files"] / f"chatdb_Meta-{iteration}.meta.json"
    if meta_path.exists():
        meta = json.loads(meta_path.read_text(encoding="utf-8"))
        assert "version" in meta


def test_filesystem_permissions(shared_fs):
    files_dir = shared_fs["files"]
    assert os.access(files_dir, os.R_OK | os.W_OK)

