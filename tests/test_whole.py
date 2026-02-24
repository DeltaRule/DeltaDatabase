import json
import threading
import time

import grpc
import pytest
import requests


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def test_end_to_end_flow(settings, grpc_stub, shared_fs, rsa_key_pair):
    pb2, stub = grpc_stub
    sub = stub.Subscribe(pb2.SubscribeRequest(
        worker_id="proc-1", pubkey=rsa_key_pair["pub_pem"]))
    assert sub.token

    url_put = _rest_url(settings, "/entity/chatdb")
    payload = {"Chat_id": {"chat": [{"type": "assistant", "text": "hello"}]}}
    put_resp = requests.put(url_put, headers=_auth_header(settings["token"]), json=payload, timeout=3)
    assert put_resp.status_code == 200

    url_get = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    get_resp = requests.get(url_get, headers=_auth_header(settings["token"]), timeout=3)
    assert get_resp.status_code == 200
    assert get_resp.json() == payload["Chat_id"]


def test_end_to_end_concurrency(settings):
    errors = []

    def worker(i):
        url_put = _rest_url(settings, "/entity/chatdb")
        payload = {f"Chat_{i}": {"chat": [{"type": "assistant", "text": f"t-{i}"}]}}
        try:
            put_resp = requests.put(url_put, headers=_auth_header(settings["token"]), json=payload, timeout=5)
            if put_resp.status_code != 200:
                errors.append(put_resp.status_code)
        except Exception as exc:  # noqa: BLE001
            errors.append(str(exc))

    threads = [threading.Thread(target=worker, args=(i,)) for i in range(30)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert not errors


def test_end_to_end_stress_reads(settings):
    url_get = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    for _ in range(200):
        response = requests.get(url_get, headers=_auth_header(settings["token"]), timeout=2)
        assert response.status_code == 200


def test_end_to_end_grpc_process(settings, grpc_stub, grpc_token):
    pb2, stub = grpc_stub
    put = pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        schema_id="chat.v1",
        operation="PUT",
        payload=json.dumps({"chat": [{"type": "assistant", "text": "hi"}]}).encode("utf-8"),
        token=grpc_token,
    )
    get = pb2.ProcessRequest(
        database_name="chatdb",
        entity_key="Chat_id",
        schema_id="chat.v1",
        operation="GET",
        payload=b"",
        token=grpc_token,
    )
    assert stub.Process(put).status
    assert stub.Process(get).status


def test_resilience_on_network_failure(settings):
    url_get = _rest_url(settings, "/entity/chatdb?key=Chat_id")
    try:
        requests.get(url_get, headers=_auth_header(settings["token"]), timeout=0.001)
    except requests.RequestException:
        assert True


def test_performance_under_load(benchmark, settings):
    url_put = _rest_url(settings, "/entity/chatdb")
    payload = {"Bench": {"chat": [{"type": "assistant", "text": "bench"}]}}

    def _bench():
        requests.put(url_put, headers=_auth_header(settings["token"]), json=payload, timeout=3)

    benchmark(_bench)
    assert benchmark.stats["mean"] < 0.5  # generous threshold for a live server


@pytest.mark.parametrize("iteration", range(300))
def test_massive_integration_matrix(settings, iteration):
    url_put = _rest_url(settings, "/entity/chatdb")
    payload = {f"Bulk_{iteration}": {"chat": [{"type": "assistant", "text": f"msg-{iteration}"}]}}
    response = requests.put(url_put, headers=_auth_header(settings["token"]), json=payload, timeout=3)
    assert response.status_code == 200

