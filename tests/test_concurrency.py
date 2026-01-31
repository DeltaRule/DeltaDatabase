import json
import threading
import time

import pytest
import requests


def _rest_url(settings, path):
    return settings["rest_url"].rstrip("/") + path


def _auth_header(token):
    return {"Authorization": f"Bearer {token}"}


def _put(settings, key, value):
    url = _rest_url(settings, "/entity/chatdb")
    payload = {key: {"chat": [{"type": "assistant", "text": value}]}}
    return requests.put(url, headers=_auth_header("valid-token"), json=payload, timeout=5)


def _get(settings, key):
    url = _rest_url(settings, f"/entity/chatdb?key={key}")
    return requests.get(url, headers=_auth_header("valid-token"), timeout=5)


def test_concurrent_puts_no_corruption(settings, shared_fs):
    key = "RaceKey"
    errors = []

    def writer(i):
        try:
            resp = _put(settings, key, f"value-{i}")
            if resp.status_code != 200:
                errors.append(resp.status_code)
        except Exception as exc:  # noqa: BLE001
            errors.append(str(exc))

    threads = [threading.Thread(target=writer, args=(i,)) for i in range(20)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert not errors
    meta_path = shared_fs["files"] / f"{key}.meta.json"
    assert meta_path.exists()
    meta = json.loads(meta_path.read_text(encoding="utf-8"))
    assert int(meta.get("version", 0)) >= 1


def test_concurrent_gets_are_consistent(settings):
    _put(settings, "HotKey", "seed")
    responses = []

    def reader():
        responses.append(_get(settings, "HotKey"))

    threads = [threading.Thread(target=reader) for _ in range(50)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert all(r.status_code == 200 for r in responses)


def test_lock_contention(settings):
    _put(settings, "LockKey", "seed")
    start = time.time()

    def writer():
        _put(settings, "LockKey", "update")

    t1 = threading.Thread(target=writer)
    t2 = threading.Thread(target=writer)
    t1.start()
    t2.start()
    t1.join()
    t2.join()

    assert time.time() - start < 5


@pytest.mark.parametrize("iteration", range(100))
def test_file_lock_stress(settings, iteration):
    key = f"LockStress-{iteration}"
    response = _put(settings, key, f"v-{iteration}")
    assert response.status_code == 200
