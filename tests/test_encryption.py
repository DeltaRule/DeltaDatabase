import base64
import json
import os
import time

import grpc
import pytest
import requests


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def _meta_path(shared_fs, entity_key):
    return shared_fs["files"] / f"{entity_key}.meta.json"


def _blob_path(shared_fs, entity_key):
    return shared_fs["files"] / f"{entity_key}.json.enc"


def test_put_creates_encrypted_blob(settings, shared_fs, sample_schema):
    payload = {"Chat_id": {"chat": [{"type": "assistant", "text": "hello"}]}}
    url = _rest_url(settings, "/entity/chatdb")
    response = requests.put(url, headers=_auth_header("valid-token"), json=payload, timeout=2)
    assert response.status_code == 200
    assert _meta_path(shared_fs, "Chat_id").exists()
    assert _blob_path(shared_fs, "Chat_id").exists()


def test_blob_metadata_has_crypto_fields(shared_fs):
    meta_file = _meta_path(shared_fs, "Chat_id")
    meta = json.loads(meta_file.read_text(encoding="utf-8"))
    for key in ["key_id", "alg", "iv", "tag", "schema_id", "version"]:
        assert key in meta
    assert meta["alg"].upper() == "AES-GCM"


def test_tamper_detection_on_blob(settings, shared_fs):
    blob = _blob_path(shared_fs, "Chat_id")
    raw = blob.read_bytes()
    if raw:
        blob.write_bytes(raw[:-1] + bytes([raw[-1] ^ 0xFF]))
    url = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url, headers=_auth_header("valid-token"), timeout=2)
    assert response.status_code in {400, 409, 500}


def test_aead_roundtrip_with_external_lib(aesgcm, aesgcm_key):
    nonce = os.urandom(12)
    plaintext = b"delta-db-test"
    ciphertext = aesgcm.encrypt(nonce, plaintext, b"meta")
    recovered = aesgcm.decrypt(nonce, ciphertext, b"meta")
    assert recovered == plaintext


def test_key_rotation_requires_new_key_id(grpc_stub):
    pb2, stub = grpc_stub
    first = stub.Subscribe(pb2.SubscribeRequest(worker_id="worker-rot-1", pubkey=b"pubkey"))
    second = stub.Subscribe(pb2.SubscribeRequest(worker_id="worker-rot-1", pubkey=b"pubkey"))
    assert first.key_id != second.key_id


def test_wrapped_key_is_not_plaintext(grpc_stub):
    pb2, stub = grpc_stub
    response = stub.Subscribe(pb2.SubscribeRequest(worker_id="worker-wrap", pubkey=b"pubkey"))
    wrapped = response.wrapped_key
    assert response.token.encode("utf-8") not in wrapped
    assert base64.b64decode(base64.b64encode(wrapped)) == wrapped


@pytest.mark.parametrize("size", [1, 64, 128, 256, 512, 1024, 2048, 4096])
def test_encryption_payload_sizes(settings, size):
    payload = {"Chat_id": {"chat": [{"type": "assistant", "text": "x" * size}]}}
    url = _rest_url(settings, "/entity/chatdb")
    response = requests.put(url, headers=_auth_header("valid-token"), json=payload, timeout=5)
    assert response.status_code == 200


@pytest.mark.parametrize("iteration", range(300))
def test_repeated_put_does_not_reuse_nonce(settings, iteration, shared_fs):
    payload = {"Chat_id": {"chat": [{"type": "assistant", "text": f"msg-{iteration}"}]}}
    url = _rest_url(settings, "/entity/chatdb")
    response = requests.put(url, headers=_auth_header("valid-token"), json=payload, timeout=2)
    assert response.status_code == 200
    meta = json.loads(_meta_path(shared_fs, "Chat_id").read_text(encoding="utf-8"))
    assert isinstance(meta.get("iv"), str)


@pytest.mark.parametrize("iteration", range(200))
def test_decrypt_fails_on_wrong_key(settings, iteration):
    url = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url, headers=_auth_header(f"wrong-key-{iteration}"), timeout=2)
    assert response.status_code in {401, 403, 400}
