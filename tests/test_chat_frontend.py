"""
test_chat_frontend.py — Tests for the chat frontend backend workflow.

Verifies the three-step lookup chain required by the chat frontend:

  1. email  → {password_hash, id}   (``users`` database, keyed by email)
  2. user_id → {chat_ids: [...]}     (``user_chats`` database, keyed by user ID)
  3. chat_id → {chat: [...messages]}  (``chats`` database, keyed by chat ID)

All tests run against the in-process Python mock REST server defined in
conftest.py, so no Go binary is required.

Data model summary
------------------
Database     Key          Value schema
-----------  -----------  ------------------------------------
users        <email>      {"id": str, "password_hash": str}
user_chats   <user_id>    {"chat_ids": [str, ...]}
chats        <chat_id>    {"chat": [{"type": str, "text": str}, ...]}
"""

import hashlib

import pytest
import requests


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _url(base_url: str, path: str) -> str:
    return base_url.rstrip("/") + path


def _auth(token: str) -> dict:
    return {"Authorization": f"Bearer {token}"}


def _hash_password(password: str) -> str:
    """Return a PBKDF2-HMAC-SHA256 hex digest of *password* for use in tests.

    WARNING: This is intentionally simplified for testing purposes only.
    Production code must use a dedicated password hashing library such as
    bcrypt, scrypt, or Argon2 with a random per-user salt.
    """
    return hashlib.pbkdf2_hmac("sha256", password.encode(), b"test-salt", 100_000).hex()


# ---------------------------------------------------------------------------
# Step 1 — email → {id, password_hash}
# ---------------------------------------------------------------------------

def test_store_user_by_email(rest_mock_server):
    """Storing a user record keyed by email must succeed with HTTP 200."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]

    response = requests.put(
        _url(base_url, "/entity/users"),
        headers=_auth(token),
        json={"alice@example.com": {"id": "user-alice-001", "password_hash": _hash_password("alice123")}},
        timeout=2,
    )
    assert response.status_code == 200


def test_lookup_user_by_email(rest_mock_server):
    """After storing a user, retrieving by email must return id and password_hash."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]
    email = "bob@example.com"
    user_id = "user-bob-002"
    password_hash = _hash_password("bobspassword")

    # Store
    requests.put(
        _url(base_url, "/entity/users"),
        headers=_auth(token),
        json={email: {"id": user_id, "password_hash": password_hash}},
        timeout=2,
    )

    # Retrieve by email
    response = requests.get(
        _url(base_url, f"/entity/users?key={email}"),
        headers=_auth(token),
        timeout=2,
    )
    assert response.status_code == 200
    data = response.json()
    assert data["id"] == user_id
    assert data["password_hash"] == password_hash


def test_lookup_nonexistent_user_returns_404(rest_mock_server):
    """Looking up an email that was never stored must return 404."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]

    response = requests.get(
        _url(base_url, "/entity/users?key=nobody@example.com"),
        headers=_auth(token),
        timeout=2,
    )
    assert response.status_code == 404


def test_user_lookup_requires_auth(rest_mock_server):
    """GET /entity/users without a valid Bearer token must be rejected."""
    base_url = rest_mock_server["base_url"]

    response = requests.get(
        _url(base_url, "/entity/users?key=alice@example.com"),
        timeout=2,
    )
    assert response.status_code in {401, 403}


# ---------------------------------------------------------------------------
# Step 2 — user_id → {chat_ids: [...]}
# ---------------------------------------------------------------------------

def test_store_user_chats(rest_mock_server):
    """Storing the chat-ID list for a user must succeed with HTTP 200."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]
    user_id = "user-charlie-003"
    chat_ids = ["chat-001", "chat-002", "chat-003"]

    response = requests.put(
        _url(base_url, "/entity/user_chats"),
        headers=_auth(token),
        json={user_id: {"chat_ids": chat_ids}},
        timeout=2,
    )
    assert response.status_code == 200


def test_lookup_chats_by_user_id(rest_mock_server):
    """After storing a user's chats, retrieving by user_id must return all chat IDs."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]
    user_id = "user-dana-004"
    chat_ids = ["chat-aaa", "chat-bbb"]

    # Store
    requests.put(
        _url(base_url, "/entity/user_chats"),
        headers=_auth(token),
        json={user_id: {"chat_ids": chat_ids}},
        timeout=2,
    )

    # Retrieve
    response = requests.get(
        _url(base_url, f"/entity/user_chats?key={user_id}"),
        headers=_auth(token),
        timeout=2,
    )
    assert response.status_code == 200
    data = response.json()
    assert data["chat_ids"] == chat_ids


def test_lookup_chats_for_unknown_user_returns_404(rest_mock_server):
    """Looking up chat IDs for a user that was never stored must return 404."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]

    response = requests.get(
        _url(base_url, "/entity/user_chats?key=user-does-not-exist"),
        headers=_auth(token),
        timeout=2,
    )
    assert response.status_code == 404


# ---------------------------------------------------------------------------
# Step 3 — chat_id → {chat: [...messages]}
# ---------------------------------------------------------------------------

def test_store_chat_history(rest_mock_server):
    """Storing a chat's message history must succeed with HTTP 200."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]
    chat_id = "chat-xyz-005"
    history = [
        {"type": "user", "text": "Hello!"},
        {"type": "assistant", "text": "Hi there! How can I help?"},
        {"type": "user", "text": "Tell me a joke."},
    ]

    response = requests.put(
        _url(base_url, "/entity/chats"),
        headers=_auth(token),
        json={chat_id: {"chat": history}},
        timeout=2,
    )
    assert response.status_code == 200


def test_lookup_chat_history(rest_mock_server):
    """After storing a chat, retrieving by chat_id must return the full message history."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]
    chat_id = "chat-xyz-006"
    history = [
        {"type": "user", "text": "What is 2+2?"},
        {"type": "assistant", "text": "4"},
    ]

    # Store
    requests.put(
        _url(base_url, "/entity/chats"),
        headers=_auth(token),
        json={chat_id: {"chat": history}},
        timeout=2,
    )

    # Retrieve
    response = requests.get(
        _url(base_url, f"/entity/chats?key={chat_id}"),
        headers=_auth(token),
        timeout=2,
    )
    assert response.status_code == 200
    data = response.json()
    assert data["chat"] == history


def test_lookup_nonexistent_chat_returns_404(rest_mock_server):
    """Looking up a chat_id that was never stored must return 404."""
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]

    response = requests.get(
        _url(base_url, "/entity/chats?key=chat-does-not-exist"),
        headers=_auth(token),
        timeout=2,
    )
    assert response.status_code == 404


# ---------------------------------------------------------------------------
# Full three-step workflow: email → user_id → chat_ids → chat history
# ---------------------------------------------------------------------------

def test_full_chat_frontend_workflow(rest_mock_server):
    """
    End-to-end three-step lookup chain for the chat frontend:

      1. Use email to retrieve the user's id and hashed password.
      2. Use the user id to retrieve the user's list of chat IDs.
      3. Use each chat ID to retrieve the full message history.
    """
    base_url = rest_mock_server["base_url"]
    token = rest_mock_server["valid_token"]

    # --- Test data ---
    email = "eve@example.com"
    user_id = "user-eve-007"
    password_hash = _hash_password("evepass")
    chat_id_1 = "chat-eve-007a"
    chat_id_2 = "chat-eve-007b"
    history_1 = [
        {"type": "user", "text": "Hey Eve!"},
        {"type": "assistant", "text": "Hello!"},
    ]
    history_2 = [
        {"type": "user", "text": "Second chat message"},
        {"type": "assistant", "text": "Got it!"},
    ]

    # --- Seed the databases ---
    requests.put(
        _url(base_url, "/entity/users"),
        headers=_auth(token),
        json={email: {"id": user_id, "password_hash": password_hash}},
        timeout=2,
    )
    requests.put(
        _url(base_url, "/entity/user_chats"),
        headers=_auth(token),
        json={user_id: {"chat_ids": [chat_id_1, chat_id_2]}},
        timeout=2,
    )
    requests.put(
        _url(base_url, "/entity/chats"),
        headers=_auth(token),
        json={chat_id_1: {"chat": history_1}},
        timeout=2,
    )
    requests.put(
        _url(base_url, "/entity/chats"),
        headers=_auth(token),
        json={chat_id_2: {"chat": history_2}},
        timeout=2,
    )

    # --- Step 1: email → {id, password_hash} ---
    r = requests.get(
        _url(base_url, f"/entity/users?key={email}"),
        headers=_auth(token),
        timeout=2,
    )
    assert r.status_code == 200
    user_data = r.json()
    assert user_data["id"] == user_id
    assert user_data["password_hash"] == password_hash

    retrieved_user_id = user_data["id"]

    # --- Step 2: user_id → {chat_ids} ---
    r = requests.get(
        _url(base_url, f"/entity/user_chats?key={retrieved_user_id}"),
        headers=_auth(token),
        timeout=2,
    )
    assert r.status_code == 200
    chats_data = r.json()
    assert set(chats_data["chat_ids"]) == {chat_id_1, chat_id_2}

    retrieved_chat_ids = chats_data["chat_ids"]

    # --- Step 3: chat_id → message history ---
    expected_histories = {chat_id_1: history_1, chat_id_2: history_2}
    for cid in retrieved_chat_ids:
        r = requests.get(
            _url(base_url, f"/entity/chats?key={cid}"),
            headers=_auth(token),
            timeout=2,
        )
        assert r.status_code == 200, f"Expected 200 for chat_id={cid!r}, got {r.status_code}"
        chat_data = r.json()
        assert "chat" in chat_data
        assert isinstance(chat_data["chat"], list)
        assert len(chat_data["chat"]) > 0
        assert chat_data["chat"] == expected_histories[cid]
