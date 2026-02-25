
# Authentication

DeltaDatabase uses a **three-tier Bearer token model** for external clients and a separate **RSA + token handshake** for Processing Workers.

> **For server-to-server or script usage:** use an admin key or API key directly as the Bearer value on every request — **no login step is ever required**. The keys are static and do not expire unless you explicitly set `expires_in` when creating an API key.

---

## Three-Tier Authentication Priority

Every `Authorization: Bearer <value>` header is evaluated in this order:

| Priority | Type | How to obtain | Expiry |
|----------|------|---------------|--------|
| 1 | **Admin key** | Set `-admin-key` flag or `$ADMIN_KEY` env var at startup | Never expires |
| 2 | **API key** (`dk_…`) | `POST /api/keys` (requires admin) | Never, unless `expires_in` is set at creation |
| 3 | **Session token** | `POST /api/login` — browser UI only | Short-lived (default: 24 h, set by `-client-ttl`) |

---

## Client Authentication

### Option A — Admin Key (direct Bearer)

Supply the admin key directly in every request header (no login required):

```bash
curl -s http://127.0.0.1:8080/admin/workers \
  -H "Authorization: Bearer ${ADMIN_KEY}"
```

### Option B — API Key (direct Bearer)

Create a named API key via the Management UI or REST API, then use its secret directly.
**No login step required — the secret is the Bearer token.**

```bash
# Create a read+write key (requires admin)
curl -s -X POST http://127.0.0.1:8080/api/keys \
  -H "Authorization: Bearer ${ADMIN_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-deploy","permissions":["read","write"]}'

# Use the returned dk_… secret directly on every request
curl -s http://127.0.0.1:8080/entity/mydb?key=hello \
  -H "Authorization: Bearer dk_abc123…"
```

### Option C — Session Token (browser UI only)

This option exists solely so that the built-in web UI can exchange a key for a
short-lived token without storing the raw key in browser storage. **Do not use
this pattern in server-to-server code or scripts** — use Option A or B instead.

Obtain a short-lived session token by posting your admin key or API key to `/api/login`.  
The session token inherits the **exact permissions** of the key used to authenticate.

#### Step 1 — Login

```bash
curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"key": "YOUR_ADMIN_OR_API_KEY"}'
```

Response:

```json
{
  "token":       "bWDQOfIsXsdpo1OZhIwcGrRu…",
  "client_id":   "admin",
  "expires_at":  "2026-02-26T09:00:00Z",
  "permissions": ["read","write","admin"]
}
```

The token is valid for the duration configured by `-client-ttl` on the Main Worker (default: **24 hours**).

#### Step 2 — Use the Token

```bash
curl -s "http://127.0.0.1:8080/entity/chatdb?key=session_001" \
  -H "Authorization: Bearer bWDQOfIsXsdpo1OZhIwcGrRu…"
```

#### Step 3 — Token Expiry

Tokens cannot be refreshed. When a token expires, obtain a new one by calling `POST /api/login` again with your key.

---

## Dev Mode (no admin key configured)

When the server is started **without** `-admin-key` (local development only), the old `client_id` login is accepted for backwards compatibility:

```bash
curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id": "myapp"}'
```

This issues a session token with `read` + `write` permissions.  
**Do not use dev mode in production.**

---

## Token Expiry

| Token type | Default TTL | Configured by |
|------------|-------------|---------------|
| Client session token | 24 hours | `-client-ttl` on Main Worker |
| Processing Worker session token | 1 hour | `-worker-ttl` on Main Worker |

---

## API Key Management

API keys are persisted to disk (`<shared-fs>/_auth/keys.json`) and survive restarts.

### Create a key

```bash
# Key with read+write, expires in 7 days
curl -s -X POST http://127.0.0.1:8080/api/keys \
  -H "Authorization: Bearer ${ADMIN_KEY}" \
  -H 'Content-Type: application/json' \
  -d '{"name":"ci-deploy","permissions":["read","write"],"expires_in":"7d"}'
```

Response (secret shown **once only**):

```json
{
  "id":          "a1b2c3d4e5f6a7b8",
  "name":        "ci-deploy",
  "secret":      "dk_abc123…",
  "permissions": ["read","write"],
  "expires_at":  "2026-03-04T09:00:00Z",
  "created_at":  "2026-02-25T09:00:00Z"
}
```

### List keys

```bash
curl -s http://127.0.0.1:8080/api/keys \
  -H "Authorization: Bearer ${ADMIN_KEY}"
```

### Delete a key

```bash
curl -s -X DELETE http://127.0.0.1:8080/api/keys/<key-id> \
  -H "Authorization: Bearer ${ADMIN_KEY}"
```

### Permissions

| Constant | Value | Grants |
|---|---|---|
| `read` | `"read"` | `GET /entity/…` |
| `write` | `"write"` | `PUT /entity/…`, `PUT /schema/…` |
| `admin` | `"admin"` | All of the above + key management + `/admin/workers` |

---

## Worker Authentication (Internal)

Processing Workers authenticate with the Main Worker during subscription using a **public-key + token handshake**:

1. Worker generates an ephemeral RSA key pair at startup.
2. Worker sends `Subscribe(worker_id, rsa_public_key)` to the Main Worker over gRPC.
3. Main Worker wraps the AES master key with the worker's RSA public key (RSA-OAEP) and issues a short-lived session token.
4. Worker unwraps the AES key using its RSA private key and stores it in volatile memory.
5. Worker uses the session token for subsequent gRPC calls to the Main Worker.

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

!!! warning
    Configure an admin key before exposing the server to any network.

- Start the Main Worker with `-admin-key` or set the `ADMIN_KEY` environment variable.
- Put the Main Worker behind a reverse proxy (nginx, Traefik) with TLS termination.
- Create scoped API keys for each service; do not share the admin key.
- Set a short `-client-ttl` (e.g., `1h`) for sensitive applications.
- The `-master-key` flag value appears in shell history — use a wrapper script or secrets manager to supply it.
