import re

import grpc
import pytest
import requests


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def test_no_plaintext_keys_on_disk(shared_fs):
    suspicious = []
    for path in shared_fs["root"].rglob("*"):
        if path.is_file() and path.suffix in {".json", ".enc"}:
            content = path.read_bytes()
            if b"PRIVATE KEY" in content or b"BEGIN" in content:
                suspicious.append(path)
    assert not suspicious


def test_replay_token_rejected(settings):
    url = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    token = "replayed-token"
    first = requests.get(url, headers=_auth_header(token), timeout=2)
    second = requests.get(url, headers=_auth_header(token), timeout=2)
    assert first.status_code in {401, 403}
    assert second.status_code in {401, 403}


def test_grpc_requires_mtls_or_token(grpc_stub):
    pb2, stub = grpc_stub
    req = pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        schema_id="chat.v1",
        operation="GET",
        payload=b"",
        token="",
    )
    with pytest.raises(grpc.RpcError) as exc:
        stub.Process(req)
    assert exc.value.code() in {grpc.StatusCode.UNAUTHENTICATED, grpc.StatusCode.PERMISSION_DENIED}


def test_log_redaction(settings):
    if not settings["log_path"]:
        pytest.skip("DELTADB_LOG_PATH not configured")
    log_content = open(settings["log_path"], "r", encoding="utf-8").read()
    assert "PRIVATE KEY" not in log_content
    assert re.search(r"token-[A-Za-z0-9]+", log_content) is None


@pytest.mark.parametrize("payload", ["' OR 1=1 --", "../..", "${jndi:ldap://x}"])
def test_injection_payloads_rejected(settings, payload):
    url = _rest_url(settings, f"/entity/chatdb?key={payload}")
    response = requests.get(url, headers=_auth_header("valid-token"), timeout=2)
    assert response.status_code in {400, 401, 403}
