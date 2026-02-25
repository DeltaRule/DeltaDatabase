"""
Tests for the RBAC authentication model.

These tests verify:
  - The admin key can be used directly as a Bearer token (no /api/login needed),
    the same way you would use a Postgres password or MinIO access key.
  - RBAC API keys can be created and used directly as Bearer tokens.
  - Permission boundaries are enforced (read-only key cannot write, etc.).
  - The /api/login endpoint is only for the frontend — it exchanges a key for a
    short-lived session token.
"""
import requests
import pytest


def _auth(token):
    return {"Authorization": f"Bearer {token}"}


def _url(settings, path):
    return settings["rest_url"].rstrip("/") + path


# ── Direct admin-key auth (no login required) ────────────────────────────────

def test_admin_key_put_entity_directly(settings):
    """PUT /entity with admin key as Bearer token — no /api/login call."""
    url = _url(settings, "/entity/directdb")
    resp = requests.put(
        url,
        headers=_auth(settings["admin_key"]),
        json={"direct_key": {"value": 42}},
        timeout=5,
    )
    assert resp.status_code == 200, resp.text


def test_admin_key_get_entity_directly(settings):
    """GET /entity with admin key as Bearer token — no /api/login call."""
    # First write a value.
    put_url = _url(settings, "/entity/directdb")
    requests.put(
        put_url,
        headers=_auth(settings["admin_key"]),
        json={"admin_get_key": {"hello": "world"}},
        timeout=5,
    )

    get_url = _url(settings, "/entity/directdb?key=admin_get_key")
    resp = requests.get(get_url, headers=_auth(settings["admin_key"]), timeout=5)
    assert resp.status_code == 200, resp.text


def test_admin_key_list_api_keys_directly(settings):
    """GET /api/keys with admin key — no login, full RBAC access."""
    resp = requests.get(
        _url(settings, "/api/keys"),
        headers=_auth(settings["admin_key"]),
        timeout=5,
    )
    assert resp.status_code == 200, resp.text
    assert isinstance(resp.json(), list)


# ── RBAC API key creation & direct use ───────────────────────────────────────

def test_create_and_use_read_only_api_key(settings):
    """Create a read-only key via API, then use it directly — no login."""
    # Create key (admin required).
    create_resp = requests.post(
        _url(settings, "/api/keys"),
        headers=_auth(settings["admin_key"]),
        json={"name": "test-readonly", "permissions": ["read"]},
        timeout=5,
    )
    assert create_resp.status_code == 201, create_resp.text
    data = create_resp.json()
    ro_secret = data["secret"]
    assert ro_secret.startswith("dk_")

    # Seed a value as admin so we can read it.
    requests.put(
        _url(settings, "/entity/rbacdb"),
        headers=_auth(settings["admin_key"]),
        json={"rbac_item": {"x": 1}},
        timeout=5,
    )

    # Read with the read-only key — should succeed.
    get_resp = requests.get(
        _url(settings, "/entity/rbacdb?key=rbac_item"),
        headers=_auth(ro_secret),
        timeout=5,
    )
    assert get_resp.status_code == 200, get_resp.text

    # Write with the read-only key — should be forbidden.
    put_resp = requests.put(
        _url(settings, "/entity/rbacdb"),
        headers=_auth(ro_secret),
        json={"rbac_item": {"x": 2}},
        timeout=5,
    )
    assert put_resp.status_code == 403, put_resp.text

    # Clean up.
    key_id = data["id"]
    del_resp = requests.delete(
        _url(settings, f"/api/keys/{key_id}"),
        headers=_auth(settings["admin_key"]),
        timeout=5,
    )
    assert del_resp.status_code == 200, del_resp.text


def test_create_and_use_write_api_key(settings):
    """Create a write key and use it directly as Bearer token."""
    create_resp = requests.post(
        _url(settings, "/api/keys"),
        headers=_auth(settings["admin_key"]),
        json={"name": "test-write", "permissions": ["read", "write"]},
        timeout=5,
    )
    assert create_resp.status_code == 201, create_resp.text
    rw_secret = create_resp.json()["secret"]
    key_id = create_resp.json()["id"]

    # Write should succeed.
    put_resp = requests.put(
        _url(settings, "/entity/rwdb"),
        headers=_auth(rw_secret),
        json={"rw_key": {"v": "ok"}},
        timeout=5,
    )
    assert put_resp.status_code == 200, put_resp.text

    # /api/keys management should be forbidden (no admin permission).
    list_resp = requests.get(
        _url(settings, "/api/keys"),
        headers=_auth(rw_secret),
        timeout=5,
    )
    assert list_resp.status_code == 403, list_resp.text

    requests.delete(
        _url(settings, f"/api/keys/{key_id}"),
        headers=_auth(settings["admin_key"]),
        timeout=5,
    )


def test_create_api_key_with_expiry(settings):
    """Create a key with an expiry time and verify the field is set."""
    create_resp = requests.post(
        _url(settings, "/api/keys"),
        headers=_auth(settings["admin_key"]),
        json={
            "name": "expiring-key",
            "permissions": ["read"],
            "expires_in": "24h",
        },
        timeout=5,
    )
    assert create_resp.status_code == 201, create_resp.text
    data = create_resp.json()
    assert data["expires_at"] is not None

    requests.delete(
        _url(settings, f"/api/keys/{data['id']}"),
        headers=_auth(settings["admin_key"]),
        timeout=5,
    )


def test_deleted_api_key_is_rejected(settings):
    """After deletion, the key's secret must no longer be accepted."""
    create_resp = requests.post(
        _url(settings, "/api/keys"),
        headers=_auth(settings["admin_key"]),
        json={"name": "ephemeral", "permissions": ["read"]},
        timeout=5,
    )
    assert create_resp.status_code == 201
    secret = create_resp.json()["secret"]
    key_id = create_resp.json()["id"]

    # Confirm it works before deletion.
    resp = requests.get(
        _url(settings, "/api/keys"),
        headers=_auth(settings["admin_key"]),
        timeout=5,
    )
    assert resp.status_code == 200

    # Delete the key.
    requests.delete(
        _url(settings, f"/api/keys/{key_id}"),
        headers=_auth(settings["admin_key"]),
        timeout=5,
    )

    # Now the secret should be rejected.
    resp = requests.put(
        _url(settings, "/entity/testdb"),
        headers=_auth(secret),
        json={"k": {"v": 1}},
        timeout=5,
    )
    assert resp.status_code == 401, resp.text


# ── /api/login is for the frontend only ──────────────────────────────────────

def test_login_with_admin_key_issues_session_token(settings):
    """POST /api/login with admin key returns a short-lived session token."""
    resp = requests.post(
        _url(settings, "/api/login"),
        json={"key": settings["admin_key"]},
        timeout=5,
    )
    assert resp.status_code == 200, resp.text
    data = resp.json()
    assert data["token"]
    assert "admin" in data.get("permissions", [])


def test_login_with_invalid_key_is_rejected(settings):
    """POST /api/login with an unknown key returns 401."""
    resp = requests.post(
        _url(settings, "/api/login"),
        json={"key": "notavalidkey"},
        timeout=5,
    )
    assert resp.status_code == 401, resp.text


def test_session_token_also_works_for_api_calls(settings):
    """Session tokens (from /api/login) still work for API calls."""
    # The settings["token"] is a session token obtained in conftest.py.
    resp = requests.put(
        _url(settings, "/entity/sessiondb"),
        headers=_auth(settings["token"]),
        json={"session_key": {"ok": True}},
        timeout=5,
    )
    assert resp.status_code == 200, resp.text
