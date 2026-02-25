# internal/auth

Authentication and authorization logic for DeltaDatabase.

## Overview

The `auth` package provides authentication and token management for both
Processing Workers and REST API clients.  It implements:

- **Admin key** — a single master key (set at startup) that bypasses all RBAC.
  Works like a PostgreSQL superuser password or MinIO root access key.
- **API Key Manager** — create/validate/delete named RBAC keys with configurable
  permissions and optional expiry.  Keys are persisted to a JSON file on the
  shared filesystem so they survive restarts.
- **Token Manager** — short-lived session tokens issued by `POST /api/login`
  for the browser UI (not required for direct API access).
- **Worker Authenticator** — credential management for Processing Workers.

## Authentication Priority

Every `Authorization: Bearer <value>` header is checked in this order:

1. **Admin key** — matches the value set with `-admin-key` → full access, no
   RBAC checks.
2. **API key** — secret issued by `POST /api/keys` → access limited to the
   permissions granted at creation time.
3. **Session token** — short-lived token from `POST /api/login` → read + write
   access (frontend use only).

## Components

### KeyManager

Manages persistent RBAC API keys:

```go
km, err := auth.NewKeyManager("/shared/db/_auth/keys.json")

// Create a key with read+write permissions expiring in 7 days
expires := time.Now().Add(7 * 24 * time.Hour)
secret, key, err := km.CreateKey("ci-deploy", []auth.Permission{
    auth.PermRead,
    auth.PermWrite,
}, &expires)
// secret is shown once: "dk_abc123…"

// Validate a request's Bearer token
apiKey, err := km.ValidateKey(bearerTokenFromRequest)

// List, delete
keys := km.ListKeys()
err = km.DeleteKey(key.ID)
```

Permissions:

| Constant | Value | Grants |
|---|---|---|
| `auth.PermRead` | `"read"` | `GET /entity/…` |
| `auth.PermWrite` | `"write"` | `PUT /entity/…`, `PUT /schema/…` |
| `auth.PermAdmin` | `"admin"` | All of the above + key management |

### TokenManager

Manages session tokens for authenticated entities:

```go
tm := auth.NewTokenManager(
    1*time.Hour,   // Worker token TTL
    24*time.Hour,  // Client token TTL
)

// Generate worker token
workerToken, err := tm.GenerateWorkerToken("worker-1", "key-id", tags)

// Validate token
token, err := tm.ValidateWorkerToken(tokenString)

// Revoke token
err = tm.RevokeWorkerToken(tokenString)
```

### WorkerAuthenticator

Handles Processing Worker registration and credential verification:

```go
wa := auth.NewWorkerAuthenticator()

// Register worker
err := wa.RegisterWorker("worker-1", "password", tags)

// Authenticate worker
creds, err := wa.AuthenticateWorker("worker-1", "password")
```

## Token Lifecycle

1. **Generation**: Token created with TTL and associated metadata
2. **Validation**: Token verified on each request
3. **Expiration**: Automatic expiration after TTL
4. **Cleanup**: Background goroutine removes expired tokens every 5 minutes
5. **Revocation**: Manual revocation supported

## Security Features

- Cryptographically secure random tokens (32 bytes, hex/base64-encoded)
- SHA-256 hashing — raw secrets are never stored
- Thread-safe operations with read-write locks
- Automatic token expiration
- Atomic file writes for key persistence (`.tmp` + rename)
- API key secrets prefixed with `dk_` for easy identification

## Testing

Run unit tests:

```bash
go test ./internal/auth/
go test ./internal/auth/ -v
go test ./internal/auth/ -cover
```
