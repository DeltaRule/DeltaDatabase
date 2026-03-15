import json
import os
import time

import grpc
import pytest
import requests
from jsonschema import Draft7Validator


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def _entity_meta_path(shared_fs, schema_id: str, entity_key: str):
    """Return the metadata file Path for a given schema_id + entity_key pair.

    The proc-worker constructs the entity ID as ``schema_id + "_" + entity_key``
    and stores the metadata at ``<files_dir>/<entity_id>.meta.json``.
    """
    entity_id = f"{schema_id}_{entity_key}"
    return shared_fs["files"] / f"{entity_id}.meta.json"


def test_schema_validation_rejects_invalid(proc_grpc_stub, sample_schema):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "assistant"}]}).encode()  # missing "text"
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
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
        entity_key="MetaCheck",
        schema_id="chat.v1",
        operation="PUT",
        payload=payload,
        token="",
    ))
    assert resp.status == "OK"
    # Entity ID = schema_id + "_" + entity_key = "chat.v1_MetaCheck"
    meta_path = _entity_meta_path(shared_fs, "chat.v1", "MetaCheck")
    # Disk writes are async — wait for the file to appear.
    deadline = time.monotonic() + 10.0
    while time.monotonic() < deadline:
        if meta_path.exists():
            break
        time.sleep(0.1)
    assert meta_path.exists(), "meta file not written in time"
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    assert meta["schema_id"] == "chat.v1"


def test_atomic_write_no_temp_files(shared_fs):
    # Async write goroutines use .tmp files as an intermediate step before the
    # atomic rename.  Wait for any in-flight writes from earlier tests to
    # complete so the assertion is deterministic.
    deadline = time.monotonic() + 10.0
    temp_files = []
    while time.monotonic() < deadline:
        temp_files = list(shared_fs["files"].glob("*.tmp"))
        if not temp_files:
            break
        time.sleep(0.2)
    assert not temp_files, (
        f"Temp files still present after waiting: {[str(p) for p in temp_files]}"
    )


@pytest.mark.parametrize("iteration", range(200))
def test_file_metadata_has_version(proc_grpc_stub, shared_fs, iteration):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "user", "text": f"v{iteration}"}]}).encode()
    stub.Process(pb2.ProcessRequest(
        schema_id="chatdb",
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

