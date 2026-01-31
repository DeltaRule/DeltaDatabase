import json
import os

import pytest
import requests
from jsonschema import Draft7Validator


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def test_schema_validation_rejects_invalid(settings, sample_schema):
    url = _rest_url(settings, "/entity/chatdb")
    payload = {"Chat_id": {"chat": [{"type": "assistant"}]}}
    response = requests.put(url, headers=_auth_header("valid-token"), json=payload, timeout=2)
    assert response.status_code == 400


def test_schema_validation_accepts_valid(sample_schema):
    schema = json.loads(sample_schema.read_text(encoding="utf-8"))
    validator = Draft7Validator(schema)
    valid_payload = {"chat": [{"type": "assistant", "text": "ok"}]}
    errors = list(validator.iter_errors(valid_payload))
    assert not errors


def test_metadata_matches_schema_id(shared_fs):
    meta_path = shared_fs["files"] / "Chat_id.meta.json"
    assert meta_path.exists()
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    assert meta["schema_id"] == "chat.v1"


def test_atomic_write_no_temp_files(shared_fs):
    temp_files = list(shared_fs["files"].glob("*.tmp"))
    assert not temp_files


@pytest.mark.parametrize("iteration", range(200))
def test_file_metadata_has_version(shared_fs, iteration):
    meta_path = shared_fs["files"] / f"Meta-{iteration}.meta.json"
    if meta_path.exists():
        meta = json.loads(meta_path.read_text(encoding="utf-8"))
        assert "version" in meta


def test_filesystem_permissions(shared_fs):
    files_dir = shared_fs["files"]
    assert os.access(files_dir, os.R_OK | os.W_OK)
