---
layout: default
title: Authentication
parent: Usage
nav_order: 4
---

# Authentication

DeltaDatabase uses a **Bearer token** model for external clients and a separate **RSA + token handshake** for Processing Workers.

---

## Client Authentication

### Step 1 — Login

Obtain a token by calling `POST /api/login` with your `client_id`:

```bash
curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id": "myapp"}'
```

Response:

```json
{
  "token":      "bWDQOfIsXsdpo1OZhIwcGrRu…",
  "client_id":  "myapp",
  "expires_at": "2026-02-25T12:00:00Z"
}
```

The token is valid for the duration configured by `-client-ttl` on the Main Worker (default: **24 hours**).

### Step 2 — Use the Token

Include the token as an `Authorization: Bearer` header on every entity request:

```bash
curl -s "http://127.0.0.1:8080/entity/chatdb?key=session_001" \
  -H "Authorization: Bearer bWDQOfIsXsdpo1OZhIwcGrRu…"
```

### Step 3 — Refresh

Tokens cannot be refreshed. Obtain a new token by logging in again before the current token expires.

---

## Token Expiry

| Token type | Default TTL | Configured by |
|------------|-------------|---------------|
| Client Bearer token | 24 hours | `-client-ttl` on Main Worker |
| Processing Worker session token | 1 hour | `-worker-ttl` on Main Worker |

---

## Worker Authentication (Internal)

Processing Workers authenticate with the Main Worker during subscription using a **pre-shared credentials + RSA key exchange**:

1. Worker generates an ephemeral RSA key pair at startup.
2. Worker sends `Subscribe(worker_id, rsa_public_key)` to the Main Worker.
3. Main Worker validates the worker credentials.
4. Main Worker wraps the AES master key with the worker's RSA public key (RSA-OAEP) and issues a short-lived session token.
5. Worker unwraps the AES key using its RSA private key and stores it in volatile memory.
6. Worker uses the session token for subsequent gRPC calls to the Main Worker.

This ensures that **the plaintext AES master key never travels over the wire unencrypted**.

---

## Public Endpoints

The following endpoints do **not** require authentication:

| Endpoint | Description |
|----------|-------------|
| `GET /health` | Health check |
| `GET /admin/schemas` | List schema IDs |
| `GET /schema/{id}` | Retrieve a schema document |

---

## Securing the API in Production

{: .warning }
> In development, `client_id` is trusted as-is. In production, harden the auth model:

- Put the Main Worker behind a reverse proxy (nginx, Traefik) with TLS termination.
- Use a fixed, strong `client_id` and rotate tokens regularly.
- Set a short `-client-ttl` (e.g., `1h`) for sensitive applications.
- The `-master-key` flag value appears in shell history — use a wrapper script or secrets manager to supply it.
