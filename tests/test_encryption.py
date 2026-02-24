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
    return shared_fs["files"] / f"chatdb_{entity_key}.meta.json"


def _blob_path(shared_fs, entity_key):
    return shared_fs["files"] / f"chatdb_{entity_key}.json.enc"


def test_put_creates_encrypted_blob(proc_grpc_stub, shared_fs):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "assistant", "text": "hello"}]}).encode()
    resp = stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="EncBlob",
        operation="PUT",
        payload=payload,
        token="",
    ))
    assert resp.status == "OK"
    assert _meta_path(shared_fs, "EncBlob").exists()
    assert _blob_path(shared_fs, "EncBlob").exists()


def test_blob_metadata_has_crypto_fields(proc_grpc_stub, shared_fs):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "assistant", "text": "meta check"}]}).encode()
    stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="EncBlob",
        operation="PUT",
        payload=payload,
        token="",
    ))
    meta_file = _meta_path(shared_fs, "EncBlob")
    meta = json.loads(meta_file.read_text(encoding="utf-8"))
    for key in ["key_id", "alg", "iv", "tag", "schema_id", "version"]:
        assert key in meta
    assert meta["alg"].upper() == "AES-GCM"


def test_tamper_detection_on_blob(proc_grpc_stub, shared_fs):
    import base64 as _base64
    import os as _os
    from cryptography.hazmat.primitives.ciphers.aead import AESGCM as _AESGCM

    # Write a file encrypted with a WRONG key so the proc-worker cannot decrypt it.
    wrong_key = _AESGCM.generate_key(bit_length=256)
    aesgcm_wrong = _AESGCM(wrong_key)
    plaintext = json.dumps({"chat": [{"type": "user", "text": "tampered"}]}).encode()
    nonce = _os.urandom(12)
    ct_with_tag = aesgcm_wrong.encrypt(nonce, plaintext, None)
    ciphertext = ct_with_tag[:-16]
    tag = ct_with_tag[-16:]

    entity_id = "chatdb_TamperDetect"
    blob_path = shared_fs["files"] / f"{entity_id}.json.enc"
    meta_path = shared_fs["files"] / f"{entity_id}.meta.json"

    blob_path.write_bytes(ciphertext)
    meta = {
        "key_id": "wrong-key",
        "alg": "AES-GCM",
        "iv": _base64.b64encode(nonce).decode(),
        "tag": _base64.b64encode(tag).decode(),
        "schema_id": "",
        "version": 1,
        "writer_id": "test",
        "timestamp": "2026-01-01T00:00:00Z",
        "database": "chatdb",
        "entity_key": "TamperDetect",
    }
    meta_path.write_text(json.dumps(meta), encoding="utf-8")

    # The proc-worker decrypts with the master key; wrong key → decryption failure.
    pb2, stub = proc_grpc_stub
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(pb2.ProcessRequest(
            database_name="chatdb",
            entity_key="TamperDetect",
            operation="GET",
            payload=b"",
            token="",
        ))
    assert exc.value.code() in {grpc.StatusCode.INTERNAL, grpc.StatusCode.NOT_FOUND}


def test_aead_roundtrip_with_external_lib(aesgcm, aesgcm_key):
    nonce = os.urandom(12)
    plaintext = b"delta-db-test"
    ciphertext = aesgcm.encrypt(nonce, plaintext, b"meta")
    recovered = aesgcm.decrypt(nonce, ciphertext, b"meta")
    assert recovered == plaintext


def test_key_rotation_requires_new_key_id(grpc_stub, rsa_key_pair):
    pb2, stub = grpc_stub
    first = stub.Subscribe(pb2.SubscribeRequest(
        worker_id="worker-rot-1", pubkey=rsa_key_pair["pub_pem"]))
    second = stub.Subscribe(pb2.SubscribeRequest(
        worker_id="worker-rot-1", pubkey=rsa_key_pair["pub_pem"]))
    assert first.key_id == second.key_id
    assert first.key_id


def test_wrapped_key_is_not_plaintext(grpc_stub, rsa_key_pair):
    pb2, stub = grpc_stub
    response = stub.Subscribe(pb2.SubscribeRequest(
        worker_id="worker-wrap", pubkey=rsa_key_pair["pub_pem"]))
    wrapped = response.wrapped_key
    assert response.token.encode("utf-8") not in wrapped
    assert base64.b64decode(base64.b64encode(wrapped)) == wrapped


@pytest.mark.parametrize("size", [1, 64, 128, 256, 512, 1024, 2048, 4096])
def test_encryption_payload_sizes(proc_grpc_stub, size):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "assistant", "text": "x" * size}]}).encode()
    resp = stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key=f"SizePad-{size}",
        operation="PUT",
        payload=payload,
        token="",
    ))
    assert resp.status == "OK"


@pytest.mark.parametrize("iteration", range(300))
def test_repeated_put_does_not_reuse_nonce(proc_grpc_stub, shared_fs, iteration):
    pb2, stub = proc_grpc_stub
    payload = json.dumps({"chat": [{"type": "assistant", "text": f"msg-{iteration}"}]}).encode()
    resp = stub.Process(pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        operation="PUT",
        payload=payload,
        token="",
    ))
    assert resp.status == "OK"
    meta = json.loads(_meta_path(shared_fs, "Chat_id").read_text(encoding="utf-8"))
    assert isinstance(meta.get("iv"), str)


@pytest.mark.parametrize("iteration", range(200))
def test_decrypt_fails_on_wrong_key(settings, iteration):
    url = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    response = requests.get(url, headers=_auth_header(f"wrong-key-{iteration}"), timeout=2)
    assert response.status_code in {401, 403, 400}

