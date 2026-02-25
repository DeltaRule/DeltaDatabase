
# Example: User Profiles

Store and manage user accounts, profile data, and per-user settings. Each user is stored as a separate entity with a unique user ID as the key.

**Use case:** User registration system, profile service, preferences store.

**Database:** `userdb`  
**Entity key:** user ID (e.g., `user_42`, `alice@example.com`)  
**Entity value:** `{"id": "...", "name": "...", "email": "...", "settings": {...}}`

---

## Schema Setup

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"admin"}' | jq -r .token)

curl -s -X PUT http://127.0.0.1:8080/schema/user.v1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "user.v1",
    "type": "object",
    "properties": {
      "id":         {"type": "string"},
      "name":       {"type": "string"},
      "email":      {"type": "string", "format": "email"},
      "created_at": {"type": "string", "format": "date-time"},
      "settings": {
        "type": "object",
        "properties": {
          "theme":         {"type": "string", "enum": ["light", "dark"]},
          "notifications": {"type": "boolean"},
          "language":      {"type": "string"}
        }
      }
    },
    "required": ["id", "email"]
  }'
```

---

## Python Client

```python
"""
user_profiles.py — User profile CRUD using DeltaDatabase

Install: pip install requests
"""
import requests
from datetime import datetime, timezone

BASE_URL = "http://127.0.0.1:8080"
DATABASE = "userdb"


class UserProfileClient:
    def __init__(self, client_id: str = "user-service"):
        self.session = requests.Session()
        resp = self.session.post(
            f"{BASE_URL}/api/login", json={"client_id": client_id}
        )
        resp.raise_for_status()
        self.session.headers["Authorization"] = f"Bearer {resp.json()['token']}"

    def create_user(self, user_id: str, name: str, email: str, **settings) -> dict:
        """Create a new user profile."""
        profile = {
            "id":         user_id,
            "name":       name,
            "email":      email,
            "created_at": datetime.now(timezone.utc).isoformat(),
            "settings":   settings or {"theme": "light", "notifications": True},
        }
        resp = self.session.put(
            f"{BASE_URL}/entity/{DATABASE}",
            json={user_id: profile},
        )
        resp.raise_for_status()
        print(f"Created user '{user_id}' ({email})")
        return profile

    def get_user(self, user_id: str) -> dict | None:
        """Retrieve a user profile. Returns None if not found."""
        resp = self.session.get(
            f"{BASE_URL}/entity/{DATABASE}", params={"key": user_id}
        )
        if resp.status_code == 404:
            return None
        resp.raise_for_status()
        return resp.json()

    def update_settings(self, user_id: str, **new_settings) -> dict:
        """Update specific settings for a user."""
        profile = self.get_user(user_id)
        if profile is None:
            raise ValueError(f"User '{user_id}' not found")
        profile.setdefault("settings", {}).update(new_settings)
        self.session.put(
            f"{BASE_URL}/entity/{DATABASE}", json={user_id: profile}
        ).raise_for_status()
        print(f"Updated settings for '{user_id}': {new_settings}")
        return profile

    def delete_user(self, user_id: str) -> None:
        """
        DeltaDatabase does not have a native DELETE endpoint.
        Store a tombstone marker to indicate the user was deleted.
        """
        tombstone = {"id": user_id, "deleted": True,
                     "deleted_at": datetime.now(timezone.utc).isoformat()}
        self.session.put(
            f"{BASE_URL}/entity/{DATABASE}", json={user_id: tombstone}
        ).raise_for_status()
        print(f"Tombstoned user '{user_id}'")


if __name__ == "__main__":
    client = UserProfileClient()

    # Create users
    client.create_user("user_001", "Alice Smith", "alice@example.com",
                       theme="dark", notifications=True, language="en")
    client.create_user("user_002", "Bob Jones", "bob@example.com",
                       theme="light", notifications=False, language="fr")

    # Retrieve a user
    alice = client.get_user("user_001")
    print(f"\nAlice's profile: {alice}")

    # Update settings
    client.update_settings("user_001", theme="light", language="de")

    # Verify update
    alice_updated = client.get_user("user_001")
    print(f"Alice's new theme: {alice_updated['settings']['theme']}")

    # Non-existent user
    nobody = client.get_user("user_999")
    print(f"user_999 exists: {nobody is not None}")
```

**Expected output:**

```
Created user 'user_001' (alice@example.com)
Created user 'user_002' (bob@example.com)

Alice's profile: {'id': 'user_001', 'name': 'Alice Smith', 'email': 'alice@example.com', ...}
Updated settings for 'user_001': {'theme': 'light', 'language': 'de'}
Alice's new theme: light
user_999 exists: False
```

---

## curl Walkthrough

```bash
BASE="http://127.0.0.1:8080"
TOKEN=$(curl -sf -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"demo"}' | jq -r .token)

# Create a user
curl -sf -X PUT "$BASE/entity/userdb" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "user_001": {
      "id": "user_001",
      "name": "Alice Smith",
      "email": "alice@example.com",
      "created_at": "2026-01-01T00:00:00Z",
      "settings": {"theme": "dark", "notifications": true}
    }
  }'

# Retrieve the user
curl -sf "$BASE/entity/userdb?key=user_001" \
  -H "Authorization: Bearer $TOKEN" | jq .

# Update the theme (fetch + modify + put)
PROFILE=$(curl -sf "$BASE/entity/userdb?key=user_001" \
  -H "Authorization: Bearer $TOKEN")
UPDATED=$(echo "$PROFILE" | jq '.settings.theme = "light"')
curl -sf -X PUT "$BASE/entity/userdb" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d "{\"user_001\": $UPDATED}"
```

---

## Batch User Import

Write multiple users in a single PUT request to reduce round-trips:

```python
def bulk_import_users(client: UserProfileClient, users: list[dict]) -> None:
    """Import multiple users in a single API call."""
    payload = {}
    for user in users:
        payload[user["id"]] = {
            "id":         user["id"],
            "name":       user["name"],
            "email":      user["email"],
            "created_at": datetime.now(timezone.utc).isoformat(),
            "settings":   {"theme": "light", "notifications": True},
        }

    resp = client.session.put(
        f"{BASE_URL}/entity/{DATABASE}", json=payload
    )
    resp.raise_for_status()
    print(f"Imported {len(users)} users in one request")


# Import 100 users at once
users = [
    {"id": f"user_{i:04d}", "name": f"User {i}", "email": f"user{i}@example.com"}
    for i in range(1, 101)
]
bulk_import_users(client, users)
```

---

## Multi-Database Pattern: Separating Credentials

For security, store credentials and profile data in separate databases:

```python
# Store credentials separately
cred_payload = {
    "user_001": {
        "user_id":       "user_001",
        "password_hash": "$2b$12$...",   # bcrypt hash — never plaintext
        "mfa_enabled":   True,
    }
}
client.session.put(f"{BASE_URL}/entity/credentials", json=cred_payload).raise_for_status()

# Store public profile in a different database
profile_payload = {
    "user_001": {
        "id":    "user_001",
        "name":  "Alice Smith",
        "email": "alice@example.com",
    }
}
client.session.put(f"{BASE_URL}/entity/userdb", json=profile_payload).raise_for_status()
```

Both databases are encrypted independently and can be accessed with different tokens.
