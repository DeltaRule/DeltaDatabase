# DeltaDatabase

> A lightweight, encrypted-at-rest JSON database written in Go — built for
> production-grade workloads that need per-entity encryption, JSON Schema
> validation, and a simple REST API.

[![License](https://img.shields.io/badge/license-DeltaDatabase%20v1.0-blue)](LICENSE)
[![Go version](https://img.shields.io/badge/go-1.25%2B-00ADD8)](go.mod)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-blue)](https://deltadatabase.readthedocs.io/en/latest/)
[![Docker Hub](https://img.shields.io/badge/docker-donti%2Fdeltadatabase-2496ED?logo=docker)](https://hub.docker.com/r/donti/deltadatabase)

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Recommended Deployment: Docker / Kubernetes](#recommended-deployment-docker--kubernetes)
4. [Quick Start (Local / Development)](#quick-start-local--development)
5. [Configuration Reference](#configuration-reference)
6. [REST API Reference](#rest-api-reference)
7. [JSON Schema Templates](#json-schema-templates)
8. [Authentication](#authentication)
9. [Web Management UI](#web-management-ui)
10. [Chat Interface Example](#chat-interface-example)
    - [Go client](#go-client)
    - [Python client](#python-client)
    - [curl / shell](#curl--shell)
11. [Chat Frontend Example (3-step lookup)](#chat-frontend-example-3-step-lookup)
    - [Data model](#data-model)
    - [Python walkthrough](#python-walkthrough)
    - [curl walkthrough](#curl-walkthrough)
12. [Advanced: Running Multiple Workers](#advanced-running-multiple-workers)
13. [S3-Compatible Object Storage](#s3-compatible-object-storage)
14. [Security Model](#security-model)
15. [Caching Model](#caching-model)
16. [Benchmark Results](#benchmark-results)
17. [Testing](#testing)
18. [Building from Source](#building-from-source)
19. [Project Structure](#project-structure)
20. [License](#license)

---

## Overview

DeltaDatabase stores arbitrary JSON documents — called **entities** — inside
named **databases**.  Every entity is:

* **Validated** against a JSON Schema template before being persisted.
* **Encrypted** at rest using AES-256-GCM before touching disk.
* **Smart-cached** in memory using an LRU-only policy (no time-based expiry):
  data is cached on every write and served directly on reads; the cache holds
  entries until LRU pressure forces the least-recently-used one out.  See
  [Caching Model](#caching-model) for details.
* **Accessed** through a plain HTTP REST API or gRPC, making integration
  straightforward from any programming language.

A built-in multi-page web UI is served by the Main Worker at `/` so you can
browse and manage databases without any external tooling.

---

## Architecture

```
 ┌──────────────────────────────────────────────────────────────────┐
 │  Client (your application, browser, curl …)                      │
 └───────────────────────────────┬──────────────────────────────────┘
                                 │  REST (HTTP/JSON)  or  gRPC
                                 ▼
 ┌──────────────────────────────────────────────────────────────────┐
 │  Main Worker  (:8080 REST  |  :50051 gRPC)                       │
 │  • Issues client Bearer tokens   (POST /api/login)               │
 │  • Authenticates every request                                   │
 │  • Distributes master encryption key to Processing Workers       │
 │  • Routes entity requests to an available Processing Worker      │
 │  • Exposes the web management UI at /                            │
 └──────────────────────────────┬───────────────────────────────────┘
                                │  gRPC (internal)
                ┌───────────────┼───────────────┐
                ▼               ▼               ▼
        ┌────────────┐  ┌────────────┐  ┌────────────┐
        │ Proc Worker│  │ Proc Worker│  │ Proc Worker│
        │  :50052    │  │  :50053    │  │  :50054    │
        └─────┬──────┘  └─────┬──────┘  └─────┬──────┘
              │               │               │
              └───────────────┴───────────────┘
                              │
             ┌────────────────┴────────────────┐
             │                                 │
    ┌────────┴────────┐               ┌────────┴────────┐
    │  Shared FS      │  ── or ──     │  S3-compatible  │
    │  /shared/db/    │               │  object store   │
    │  ├── files/     │               │  (MinIO, AWS S3,│
    │  └── templates/ │               │  RustFS, …)     │
    └─────────────────┘               └─────────────────┘
```

**Main Worker** — single entry-point for all clients. Handles authentication,
token issuance, key distribution, and cache-aware + least-loaded routing across
Processing Workers.

**Processing Worker** — the data plane. Subscribes to the Main Worker to
receive the AES master key (wrapped in the worker's RSA public key), then
handles `GET` and `PUT` operations: validate → encrypt/decrypt → read/write
storage → update cache.

**Storage Backend** — either a shared POSIX filesystem or an S3-compatible
object store.  Both backends are interchangeable at startup time via flags:

* **Shared FS** (default) — any POSIX-compatible directory (local, NFS,
  CIFS, …).  File-level advisory locks (`flock`) prevent concurrent writes.
  Writes use an explicit `fdatasync` + atomic rename sequence to guarantee
  durability even if a worker crashes mid-write.

* **S3-compatible** (optional) — MinIO, RustFS, SeaweedFS, AWS S3, Ceph
  RadosGW, or any other S3-compatible service.  No shared PVC is needed.
  Per-entity in-process mutexes replace file locks; S3's strong
  read-after-write consistency prevents cross-worker races.

---

## Recommended Deployment: Docker / Kubernetes

> **For production and staging, Docker Compose and Kubernetes are the
> recommended ways to run DeltaDatabase.**  They handle service orchestration,
> environment variables, shared volumes, and graceful restarts for you —
> no manual binary setup required.

Pre-built images are published automatically to **[Docker Hub → donti/deltadatabase](https://hub.docker.com/r/donti/deltadatabase)**
on every merge to `main` and on every release tag.

| Image tag | Description |
|-----------|-------------|
| `donti/deltadatabase:latest-aio` | Both workers, always latest |
| `donti/deltadatabase:latest-main` | Main Worker, always latest |
| `donti/deltadatabase:latest-proc` | Processing Worker, always latest |
| `donti/deltadatabase:v0.1.1-alpha-aio` | Pinned release |

| Scenario | Guide | Recommendation |
|----------|-------|---------------|
| Local dev / CI | [All-in-one container](examples/01-all-in-one.md) | Simplest — one `docker compose up` |
| Small production | [1 Main + 1 Worker](examples/03-one-main-one-worker.md) | Minimal production setup |
| Scale-out | [1 Main + N Workers](examples/02-one-main-multiple-workers.md) | Docker Compose scale-out |
| Cloud / auto-scaling | [Kubernetes + HPA](examples/04-kubernetes-autoscaling.md) | ⭐ **Recommended for production** |
| Cloud storage | [S3-compatible storage](examples/05-s3-compatible-storage.md) | No shared PVC needed |

**Get started in one command — images pulled automatically from Docker Hub:**

```bash
# No clone required — just point Compose at the file from the repo, or:
docker run -d -p 8080:8080 -e ADMIN_KEY=changeme -v delta_data:/shared/db \
  donti/deltadatabase:latest-aio

# Or with Docker Compose (also uses the pre-built image):
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up
```

The REST API is available at **http://localhost:8080** and the web UI at
**http://localhost:8080/**.

See the [`examples/`](examples/) folder and the [`deploy/`](deploy/) folder
for all Docker and Kubernetes files.

> Building and running the workers from source (for local development) is
> documented in [BUILDING.md](BUILDING.md).

---

## Quick Start (Local / Development)

> For production use, see [Recommended Deployment: Docker / Kubernetes](#recommended-deployment-docker--kubernetes).
> To build the binaries from source, see [BUILDING.md](BUILDING.md).

The following steps start one Main Worker and one Processing Worker on a
single machine, then store and retrieve an entity.

### 1. Create the shared filesystem directory

```bash
mkdir -p ./shared/db/files ./shared/db/templates
```

### 2. Start the Main Worker

```bash
./bin/main-worker \
  -grpc-addr=127.0.0.1:50051 \
  -rest-addr=127.0.0.1:8080 \
  -shared-fs=./shared/db
```

The first line of output shows the generated master key — copy it for the
next step:

```
2026/02/24 12:00:00 Generated new master encryption key
2026/02/24 12:00:00 Key (hex): a1b2c3d4...  ← save this!
```

> **Tip:** Pass `-master-key=<hex>` on subsequent starts to reuse the same key
> and keep previously stored data readable.

### 3. Start the Processing Worker

Open a second terminal:

```bash
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -shared-fs=./shared/db
```

The worker subscribes to the Main Worker, receives the wrapped key, and
registers itself as available.

### 4. Store an entity

Use the admin key directly as a Bearer token — no separate login step needed:

```bash
ADMIN_KEY=mysecretkey

curl -s -X PUT http://127.0.0.1:8080/entity/chatdb \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"session_001": {"messages": [{"role":"user","content":"Hello!"}]}}'
```

Response:

```json
{"status":"ok"}
```

### 5. Retrieve the entity

```bash
curl -s "http://127.0.0.1:8080/entity/chatdb?key=session_001" \
  -H "Authorization: Bearer $ADMIN_KEY"
```

Response:

```json
{"messages":[{"role":"user","content":"Hello!"}]}
```

### 6. Delete the entity

```bash
curl -s -X DELETE "http://127.0.0.1:8080/entity/chatdb?key=session_001" \
  -H "Authorization: Bearer $ADMIN_KEY"
```

Response:

```json
{"status":"ok"}
```

---

## Configuration Reference

### Main Worker flags

| Flag | Default | Description |
|------|---------|-------------|
| `-grpc-addr` | `127.0.0.1:50051` | TCP address for the gRPC server |
| `-rest-addr` | `127.0.0.1:8080` | TCP address for the REST HTTP server |
| `-shared-fs` | `./shared/db` | Path to the shared filesystem root (ignored when `-s3-endpoint` is set) |
| `-master-key` | *(auto-generated)* | Hex-encoded 32-byte AES master key |
| `-key-id` | `main-key-v1` | Logical identifier for the master key |
| `-admin-key` | *(from `$ADMIN_KEY`)* | Master admin Bearer key — bypasses all RBAC; set once at startup |
| `-key-store` | `<shared-fs>/_auth/keys.json` | Path to the RBAC API key JSON store |
| `-worker-ttl` | `1h` | TTL for Processing Worker session tokens |
| `-client-ttl` | `24h` | TTL for frontend session tokens (`/api/login`) |
| `-grpc-max-recv-msg-size` | `4194304` (4 MiB) | Maximum gRPC message size in bytes the server will accept |
| `-rest-max-body-size` | `1048576` (1 MiB) | Maximum HTTP request body size in bytes for entity and schema PUT endpoints |
| `-s3-endpoint` | *(empty)* | S3-compatible endpoint, e.g. `minio:9000`; enables S3 backend |
| `-s3-access-key` | *(empty)* | S3 access key ID (or `S3_ACCESS_KEY` env var) |
| `-s3-secret-key` | *(empty)* | S3 secret access key (or `S3_SECRET_KEY` env var) |
| `-s3-bucket` | `deltadatabase` | S3 bucket name |
| `-s3-use-ssl` | `false` | Enable TLS for the S3 connection (set `true` for AWS S3) |
| `-s3-region` | *(empty)* | S3 region (optional; leave empty for MinIO/SeaweedFS) |

### Processing Worker flags

| Flag | Default | Description |
|------|---------|-------------|
| `-main-addr` | `127.0.0.1:50051` | Main Worker gRPC address |
| `-worker-id` | *(hostname)* | Unique ID for this worker |
| `-grpc-addr` | `127.0.0.1:0` | TCP address for this worker's gRPC server |
| `-shared-fs` | `./shared/db` | Path to the shared filesystem root (ignored when `-s3-endpoint` is set) |
| `-cache-size` | `256` | Maximum number of cached entities |
| `-cache-ttl` | `0` | Time-to-live per cache entry (`0` = LRU-only eviction, no time-based expiry) |
| `-grpc-max-recv-msg-size` | `4194304` (4 MiB) | Maximum gRPC message size in bytes this worker will accept. Should match the Main Worker's setting |
| `-s3-endpoint` | *(empty)* | S3-compatible endpoint, e.g. `minio:9000`; enables S3 backend |
| `-s3-access-key` | *(empty)* | S3 access key ID (or `S3_ACCESS_KEY` env var) |
| `-s3-secret-key` | *(empty)* | S3 secret access key (or `S3_SECRET_KEY` env var) |
| `-s3-bucket` | `deltadatabase` | S3 bucket name |
| `-s3-use-ssl` | `false` | Enable TLS for the S3 connection |
| `-s3-region` | *(empty)* | S3 region (optional) |

---

## REST API Reference

All entity endpoints require an `Authorization: Bearer <key>` header where
`<key>` is either the admin key or an RBAC API key.  See [Authentication](#authentication).

### `POST /api/login`

*(Frontend only)* Exchange an admin key or API key for a short-lived session
token used by the web UI.

**Request body:**

```json
{ "key": "mysecretkey" }
```

**Response:**

```json
{
  "token":       "bWDQOfIs…",
  "client_id":   "admin",
  "expires_at":  "2026-02-25T12:00:00Z",
  "permissions": ["read","write","admin"]
}
```

> **Tip for non-browser clients:** skip `/api/login` entirely — use your admin
> key or API key directly as the `Bearer` value on every request.

---

### `GET /api/keys`

List all RBAC API keys. Requires `admin` permission.

### `POST /api/keys`

Create a new RBAC API key. Requires `admin` permission.

**Request body:**

```json
{
  "name":        "ci-deploy",
  "permissions": ["read", "write"],
  "expires_in":  "30d"
}
```

`expires_in` accepts Go durations (`24h`, `168h`) or day shorthand (`7d`, `30d`).
Omit to create a key that never expires.

**Response** (secret shown once only):

```json
{
  "id":          "a1b2c3d4",
  "name":        "ci-deploy",
  "secret":      "dk_…",
  "permissions": ["read","write"],
  "expires_at":  "2026-03-26T09:00:00Z",
  "created_at":  "2026-02-25T09:00:00Z"
}
```

### `DELETE /api/keys/{id}`

Permanently delete a key by its ID. Requires `admin` permission.

---

### `GET /health`

Returns the system health status. No authentication required.

**Response:**

```json
{ "status": "ok" }
```

---

### `GET /admin/workers`

Returns a list of all registered Processing Workers and their status.
Requires `admin` permission.

**Response:**

```json
[
  {
    "worker_id": "proc-1",
    "status":    "Available",
    "key_id":    "main-key-v1",
    "last_seen": "2026-02-24T12:01:30Z",
    "tags":      { "grpc_addr": "127.0.0.1:50052" }
  }
]
```

---

### `PUT /entity/{database}`

Create or update one or more entities in a database.

**Path parameter:** `database` — name of the database (e.g., `chatdb`).

**Request body** — a JSON object where each key is an entity key and each
value is the entity's JSON document:

```json
{
  "session_001": { "messages": [{"role":"user","content":"Hi"}] },
  "session_002": { "messages": [{"role":"user","content":"Hello"}] }
}
```

**Response:**

```json
{ "status": "ok" }
```

---

### `GET /entity/{database}?key={entityKey}`

Retrieve a single entity.

**Path parameter:** `database` — name of the database.

**Query parameter:** `key` — entity key.

**Response** — the entity's JSON document directly:

```json
{ "messages": [{"role":"user","content":"Hi"}] }
```

**Error responses:**

| HTTP code | Meaning |
|-----------|---------|
| `400` | Missing `key` query parameter or missing database |
| `401` | Missing or invalid Bearer token |
| `404` | Entity not found |

---

### `DELETE /entity/{database}?key={entityKey}`

Delete a single entity by key from a database. Requires `write` permission.

**Path parameter:** `database` — name of the database.

**Query parameter:** `key` — entity key.

**Response:**

```json
{ "status": "ok" }
```

**Error responses:**

| HTTP code | Meaning |
|-----------|---------|
| `400` | Missing `key` query parameter or missing database |
| `401` | Missing or invalid Bearer token |
| `403` | Token lacks `write` permission |

---

### `GET /admin/schemas`

Returns a list of all defined schema IDs. No authentication required.

**Response:**

```json
["chat.v1", "user_credentials.v1", "user_chats.v1"]
```

---

### `GET /schema/{schemaID}`

Retrieve the JSON Schema document for a schema ID. No authentication required.

**Path parameter:** `schemaID` — the schema identifier (e.g., `chat.v1`).

**Response** — the raw JSON Schema document:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "chat.v1",
  "type": "object",
  "properties": { "chat": { "type": "array" } },
  "required": ["chat"]
}
```

**Error responses:**

| HTTP code | Meaning |
|-----------|---------|
| `404` | Schema not found |

---

### `PUT /schema/{schemaID}`

Create or replace a JSON Schema. Authentication required.

**Path parameter:** `schemaID` — the schema identifier (e.g., `chat.v1`).

**Request body** — a valid JSON Schema document:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "name": { "type": "string" }
  },
  "required": ["name"]
}
```

**Response:**

```json
{ "status": "ok" }
```

**Error responses:**

| HTTP code | Meaning |
|-----------|---------|
| `400` | Invalid JSON or invalid JSON Schema |
| `401` | Missing or invalid Bearer token |

---

## JSON Schema Templates

DeltaDatabase validates every `PUT` payload against a JSON Schema template
(draft-07) before encryption and storage. Templates are JSON files placed in
`{shared-fs}/templates/`.

### Creating a template on disk

Create `./shared/db/templates/chat.v1.json`:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "chat.v1",
  "type": "object",
  "properties": {
    "messages": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "role":    { "type": "string", "enum": ["user", "assistant", "system"] },
          "content": { "type": "string" }
        },
        "required": ["role", "content"]
      }
    }
  },
  "required": ["messages"]
}
```

### Creating a template via the REST API (recommended)

Schemas can also be defined directly through the REST API or the web
management UI — no filesystem access required.

```bash
# Save a schema via the API (use admin key directly — no login needed)
ADMIN_KEY=mysecretkey

curl -X PUT http://127.0.0.1:8080/schema/chat.v1 \
  -H "Authorization: Bearer $ADMIN_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "chat.v1",
    "type": "object",
    "properties": {
      "messages": { "type": "array" }
    },
    "required": ["messages"]
  }'

# List all schemas
curl http://127.0.0.1:8080/admin/schemas

# Retrieve a schema
curl http://127.0.0.1:8080/schema/chat.v1
```

The web management UI exposes a **📋 Schemas** tab where you can list, load,
and edit schemas through a form — no `curl` or file editing needed.

### Using a schema on a PUT (gRPC)

When calling the gRPC `Process` RPC directly, set the `schema_id` field of
`ProcessRequest` to `"chat.v1"`. The Processing Worker will reject any payload
that does not match the schema.

The REST API currently accepts any valid JSON; schema enforcement is applied
when the Main Worker routes to a Processing Worker with schema awareness.

---

## Authentication

DeltaDatabase uses the same direct-key model as PostgreSQL and MinIO: you
include the key on every request — **no separate login step is required** for
API access.

### Admin key

Start the Main Worker with a master admin key:

```bash
main-worker -admin-key=mysecretkey
# or via environment variable:
ADMIN_KEY=mysecretkey main-worker
```

Use it directly as a Bearer token on every request — full access, no RBAC:

```bash
# Write an entity directly with the admin key
curl -X PUT http://127.0.0.1:8080/entity/mydb \
  -H "Authorization: Bearer mysecretkey" \
  -H "Content-Type: application/json" \
  -d '{"user:1": {"name": "Alice"}}'

# Read it back
curl http://127.0.0.1:8080/entity/mydb?key=user:1 \
  -H "Authorization: Bearer mysecretkey"
```

### RBAC API keys

Create scoped keys via the API (requires admin key):

```bash
# Create a read-only key that expires in 30 days
curl -X POST http://127.0.0.1:8080/api/keys \
  -H "Authorization: Bearer mysecretkey" \
  -H "Content-Type: application/json" \
  -d '{"name":"readonly-ci","permissions":["read"],"expires_in":"30d"}'

# The response includes the secret (shown once only):
# {"id":"abc123","name":"readonly-ci","secret":"dk_…","permissions":["read"],…}
```

Use the returned secret directly as a Bearer token — no login needed:

```bash
curl http://127.0.0.1:8080/entity/mydb?key=user:1 \
  -H "Authorization: Bearer dk_…"
```

Available permissions: `read`, `write`, `admin` (full key management).

| Endpoint | Required permission |
|---|---|
| `GET /entity/…` | `read` |
| `PUT /entity/…` | `write` |
| `DELETE /entity/…` | `write` |
| `GET /admin/workers` | `admin` |
| `GET /api/keys` | `admin` |
| `POST /api/keys` | `admin` |
| `DELETE /api/keys/{id}` | `admin` |
| `PUT /schema/…` | `write` |

### Frontend / browser login

The web UI at `/` uses `POST /api/login` to exchange a key for a short-lived
session token (so the raw key is never stored in browser storage):

```json
POST /api/login
{"key": "mysecretkey"}
→ {"token": "…", "client_id": "admin", "expires_at": "…", "permissions": ["admin"]}
```

> **Note:** `/api/login` is purely for the browser UI.  All other clients
> (curl, SDKs, CI pipelines) should use the admin key or an API key directly.

### Key persistence

API keys are stored in `<shared-fs>/_auth/keys.json` and survive restarts.
Override the path with `-key-store=/path/to/keys.json`.

### Keycloak integration

See [`deploy/docker-compose/docker-compose.with-keycloak.yml`](deploy/docker-compose/docker-compose.with-keycloak.yml)
for a ready-to-run Keycloak + DeltaDatabase setup that lets you use Keycloak
as an external identity provider. Keycloak sits in front of the Main Worker
and translates OIDC tokens to DeltaDatabase Bearer tokens.

Processing Workers use a separate RSA + token-based handshake with the Main
Worker (see [Architecture](#architecture)).

---

## Web Management UI

The Main Worker serves a built-in multi-page management UI at `/`:

```
http://127.0.0.1:8080/
```

![Login](https://github.com/user-attachments/assets/bcba6cbc-61a1-4377-9b6f-455153edea53)
![Dashboard](https://github.com/user-attachments/assets/82004499-a0f7-49f5-9ee6-79c9ff893e2f)
![Databases](https://github.com/user-attachments/assets/afb838e5-2018-4417-b21f-41cbdb723b8a)

**Pages:**

| Page | Description |
|------|-------------|
| **Login** | Beautiful sign-in card — enter your admin key, API key, or a dev-mode Client ID |
| **Dashboard** | Live health status, worker counts, database count, and cache statistics |
| **Databases** | Dropdown + card grid of all databases; click any card to explore its entities |
| **Entities** | GET, PUT, and DELETE entities with a database dropdown pre-populated from `GET /api/databases` |
| **Workers** | Table of all registered Processing Workers with status, key ID, last-seen, and tags |
| **Schemas** | List, load, create, and edit JSON Schema templates; export as Pydantic or TypeScript |
| **API Keys** | Create and delete RBAC API keys (admin only) with permissions and optional expiry |
| **Explorer** | Send arbitrary HTTP requests to any endpoint with quick-access buttons |

**Frontend-specific APIs (authenticated):**

| Endpoint | Description |
|----------|-------------|
| `GET /api/databases` | Returns a sorted list of databases currently in the entity cache (requires `read`) |
| `GET /api/me` | Returns the caller's `client_id`, `permissions`, and `is_admin` flag |

The UI is **fully responsive** — on mobile a hamburger menu opens the sidebar as an overlay.

No additional installation is required — the UI is embedded directly in the
`main-worker` binary.

---

## Chat Interface Example

The following examples demonstrate a complete **chat session backend** where:

* Each user session is stored under database `chatdb`.
* The entity key is the session ID (e.g., `session_001`).
* The entity value is a JSON object containing the conversation history.

### Go client

```go
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/url"
)

const (
    baseURL  = "http://127.0.0.1:8080"
    database = "chatdb"
)

// Message represents a single chat turn.
type Message struct {
    Role    string `json:"role"`
    Content string `json:"content"`
}

// Session holds the conversation history for one chat session.
type Session struct {
    Messages []Message `json:"messages"`
}

// ChatClient wraps the DeltaDatabase REST API for chat storage.
type ChatClient struct {
    httpClient *http.Client
    baseURL    string
    apiKey     string // admin key or RBAC API key — used directly on every request
}

// NewChatClient returns a ready-to-use client authenticated with apiKey.
// No login step is required — just like connecting to PostgreSQL or MinIO.
func NewChatClient(baseURL, apiKey string) *ChatClient {
    return &ChatClient{
        httpClient: &http.Client{},
        baseURL:    baseURL,
        apiKey:     apiKey,
    }
}

func (c *ChatClient) doRequest(method, path string, body io.Reader) (*http.Response, error) {
    req, err := http.NewRequest(method, c.baseURL+path, body)
    if err != nil {
        return nil, err
    }
    req.Header.Set("Authorization", "Bearer "+c.apiKey)
    if body != nil {
        req.Header.Set("Content-Type", "application/json")
    }
    return c.httpClient.Do(req)
}

// GetSession retrieves the conversation history for a session.
// Returns an empty session if the session does not yet exist.
func (c *ChatClient) GetSession(sessionID string) (Session, error) {
    path := fmt.Sprintf("/entity/%s?key=%s", database, url.QueryEscape(sessionID))
    resp, err := c.doRequest(http.MethodGet, path, nil)
    if err != nil {
        return Session{}, err
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusNotFound {
        return Session{}, nil // new session
    }
    if resp.StatusCode != http.StatusOK {
        return Session{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
    }

    var session Session
    return session, json.NewDecoder(resp.Body).Decode(&session)
}

// AppendMessage adds a new message to a session and persists the updated history.
func (c *ChatClient) AppendMessage(sessionID string, msg Message) error {
    session, err := c.GetSession(sessionID)
    if err != nil {
        return fmt.Errorf("get session: %w", err)
    }
    session.Messages = append(session.Messages, msg)

    entityJSON, err := json.Marshal(session)
    if err != nil {
        return err
    }

    payload, err := json.Marshal(map[string]json.RawMessage{sessionID: entityJSON})
    if err != nil {
        return err
    }

    path := fmt.Sprintf("/entity/%s", database)
    resp, err := c.doRequest(http.MethodPut, path, bytes.NewReader(payload))
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("put failed with status %d", resp.StatusCode)
    }
    return nil
}

func main() {
    // --- 1. Connect — use the admin key directly, no login step needed ---
    apiKey := "mysecretkey"  // set via -admin-key flag at startup
    client := NewChatClient(baseURL, apiKey)
    fmt.Println("Connected to DeltaDatabase")

    sessionID := "session_001"

    // --- 2. Simulate a multi-turn conversation ---
    turns := []Message{
        {Role: "user",      Content: "Hello! Can you help me with Go?"},
        {Role: "assistant", Content: "Of course! What would you like to know?"},
        {Role: "user",      Content: "How do I read a file?"},
        {Role: "assistant", Content: "Use os.ReadFile(path) — it returns a []byte and an error."},
    }

    for _, msg := range turns {
        if err := client.AppendMessage(sessionID, msg); err != nil {
            panic(fmt.Sprintf("AppendMessage: %v", err))
        }
        fmt.Printf("Stored [%s]: %q\n", msg.Role, msg.Content)
    }

    // --- 3. Read the full conversation back ---
    session, err := client.GetSession(sessionID)
    if err != nil {
        panic(err)
    }

    fmt.Printf("\n=== Session %s (%d messages) ===\n", sessionID, len(session.Messages))
    for _, m := range session.Messages {
        fmt.Printf("  [%-9s] %s\n", m.Role, m.Content)
    }
}
```

Expected output:

```
Logged in successfully
Stored [user]:      "Hello! Can you help me with Go?"
Stored [assistant]: "Of course! What would you like to know?"
Stored [user]:      "How do I read a file?"
Stored [assistant]: "Use os.ReadFile(path) — it returns a []byte and an error."

=== Session session_001 (4 messages) ===
  [user     ] Hello! Can you help me with Go?
  [assistant] Of course! What would you like to know?
  [user     ] How do I read a file?
  [assistant] Use os.ReadFile(path) — it returns a []byte and an error.
```

---

### Python client

```python
"""
chat_client.py — DeltaDatabase chat backend example (Python)

Requirements:
    pip install requests
"""

import requests
import json

BASE_URL = "http://127.0.0.1:8080"
DATABASE = "chatdb"


class DeltaChatClient:
    """Simple chat-session client backed by DeltaDatabase."""

    def __init__(self, base_url: str, api_key: str) -> None:
        self.base_url = base_url
        self.session = requests.Session()
        # Use the API key (or admin key) directly — no login step required.
        self.session.headers.update({"Authorization": f"Bearer {api_key}"})

    # ── Session helpers ─────────────────────────────────────────────

    def get_session(self, session_id: str) -> dict:
        """Return the conversation dict; empty if session does not exist yet."""
        resp = self.session.get(
            f"{self.base_url}/entity/{DATABASE}",
            params={"key": session_id},
            timeout=10,
        )
        if resp.status_code == 404:
            return {"messages": []}
        resp.raise_for_status()
        return resp.json()

    def append_message(self, session_id: str, role: str, content: str) -> None:
        """Append one message to the session and persist the updated history."""
        data = self.get_session(session_id)
        data.setdefault("messages", []).append({"role": role, "content": content})

        resp = self.session.put(
            f"{self.base_url}/entity/{DATABASE}",
            json={session_id: data},
            timeout=10,
        )
        resp.raise_for_status()

    def print_session(self, session_id: str) -> None:
        """Print the full conversation for a session."""
        data = self.get_session(session_id)
        messages = data.get("messages", [])
        print(f"\n=== Session '{session_id}' ({len(messages)} messages) ===")
        for m in messages:
            role = m.get("role", "?")
            content = m.get("content", "")
            print(f"  [{role:<9}] {content}")


# ── Main ─────────────────────────────────────────────────────────────

if __name__ == "__main__":
    ADMIN_KEY = "mysecretkey"  # set via -admin-key flag at startup
    client = DeltaChatClient(BASE_URL, ADMIN_KEY)

    session_id = "py_session_001"

    # Simulate a short conversation
    conversation = [
        ("user",      "What is DeltaDatabase?"),
        ("assistant", "It is an encrypted JSON database written in Go."),
        ("user",      "Does it support schemas?"),
        ("assistant", "Yes — JSON Schema draft-07 validation on every write."),
        ("user",      "How are entities stored on disk?"),
        ("assistant", "As AES-256-GCM ciphertext with a separate .meta.json sidecar."),
    ]

    for role, content in conversation:
        client.append_message(session_id, role, content)
        print(f"  stored [{role}]: {content!r}")

    client.print_session(session_id)
```

Expected output:

```
Logged in as 'python-demo'
  stored [user]: 'What is DeltaDatabase?'
  stored [assistant]: 'It is an encrypted JSON database written in Go.'
  stored [user]: 'Does it support schemas?'
  stored [assistant]: 'Yes — JSON Schema draft-07 validation on every write.'
  stored [user]: 'How are entities stored on disk?'
  stored [assistant]: 'As AES-256-GCM ciphertext with a separate .meta.json sidecar.'

=== Session 'py_session_001' (6 messages) ===
  [user     ] What is DeltaDatabase?
  [assistant] It is an encrypted JSON database written in Go.
  [user     ] Does it support schemas?
  [assistant] Yes — JSON Schema draft-07 validation on every write.
  [user     ] How are entities stored on disk?
  [assistant] As AES-256-GCM ciphertext with a separate .meta.json sidecar.
```

---

### curl / shell

A complete shell-script walkthrough using only `curl` and `jq`:

```bash
#!/usr/bin/env bash
# chat_demo.sh — DeltaDatabase chat demo using curl + jq

BASE="http://127.0.0.1:8080"
DB="chatdb"
SESSION="bash_session_001"
ADMIN_KEY="mysecretkey"   # set via -admin-key flag at startup

# ── Helper: append one message ────────────────────────────────────
append_message() {
  local role="$1" content="$2"

  # Fetch current session (empty object if not found yet)
  existing=$(curl -sf "$BASE/entity/$DB?key=$SESSION" \
    -H "Authorization: Bearer $ADMIN_KEY" 2>/dev/null || echo '{"messages":[]}')

  # Build updated payload using jq
  updated=$(echo "$existing" | jq \
    --arg role "$role" --arg content "$content" \
    '.messages += [{"role":$role,"content":$content}]')

  # PUT back
  curl -sf -X PUT "$BASE/entity/$DB" \
    -H "Authorization: Bearer $ADMIN_KEY" \
    -H 'Content-Type: application/json' \
    -d "{\"$SESSION\": $updated}" > /dev/null

  echo "  stored [$role]: $content"
}

# ── 2. Build conversation ─────────────────────────────────────────
append_message "user"      "How do I start DeltaDatabase?"
append_message "assistant" "Run bin/main-worker and bin/proc-worker, then POST /api/login."
append_message "user"      "Where is data stored?"
append_message "assistant" "In the shared filesystem under shared/db/files/ as encrypted blobs."

# ── 3. Read it back ───────────────────────────────────────────────
echo ""
echo "=== Full session ==="
curl -sf "$BASE/entity/$DB?key=$SESSION" \
  -H "Authorization: Bearer $ADMIN_KEY" | jq .
```

Expected output:

```
Token: bWDQOfIsXsdpo1OZh…
  stored [user]: How do I start DeltaDatabase?
  stored [assistant]: Run bin/main-worker and bin/proc-worker, then POST /api/login.
  stored [user]: Where is data stored?
  stored [assistant]: In the shared filesystem under shared/db/files/ as encrypted blobs.

=== Full session ===
{
  "messages": [
    { "role": "user",      "content": "How do I start DeltaDatabase?" },
    { "role": "assistant", "content": "Run bin/main-worker and bin/proc-worker, then POST /api/login." },
    { "role": "user",      "content": "Where is data stored?" },
    { "role": "assistant", "content": "In the shared filesystem under shared/db/files/ as encrypted blobs." }
  ]
}
```

---

## Advanced: Running Multiple Workers

Horizontal scale-out is achieved by starting additional Processing Workers
pointing at the same Main Worker and the same shared filesystem directory:

```bash
# Terminal 2 — worker 2
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-2 \
  -grpc-addr=127.0.0.1:50053 \
  -shared-fs=./shared/db

# Terminal 3 — worker 3
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-3 \
  -grpc-addr=127.0.0.1:50054 \
  -shared-fs=./shared/db
```

The Main Worker routes requests using a **cache-aware + least-loaded** strategy: it
first tries to send each request to the worker that most recently served that
entity (to maximise LRU cache hits), and falls back to the worker with the
fewest active requests when no preferred worker is available. File-
level advisory locks (`flock`-style) ensure that concurrent writes from
different workers never corrupt an entity.  Each write also uses an explicit
`fdatasync` before the atomic rename, guaranteeing that no data is lost even if
a worker crashes between the rename and a kernel writeback flush.

View the registered workers at any time:

```bash
ADMIN_KEY=mysecretkey
curl -s -H "Authorization: Bearer $ADMIN_KEY" http://127.0.0.1:8080/admin/workers | jq .
```

---

## S3-Compatible Object Storage

DeltaDatabase supports an optional **S3-compatible storage backend** that
replaces the shared POSIX filesystem.  Pass `-s3-endpoint` to any worker to
switch from the default local-FS backend to S3:

```bash
# MinIO (in Docker / Kubernetes)
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -s3-endpoint=minio:9000 \
  -s3-bucket=deltadatabase \
  -s3-use-ssl=false \
  -s3-access-key=minioadmin \
  -s3-secret-key=minioadmin

# AWS S3
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -s3-endpoint=s3.amazonaws.com \
  -s3-use-ssl=true \
  -s3-region=us-east-1 \
  -s3-bucket=my-deltadatabase-bucket
```

To avoid exposing credentials in the argument list, set the
`S3_ACCESS_KEY` and `S3_SECRET_KEY` environment variables instead.

**Compatible services:** MinIO · RustFS · SeaweedFS · AWS S3 · Ceph RadosGW

When `-s3-endpoint` is set:
* No shared PersistentVolumeClaim is needed in Kubernetes.
* Per-entity in-process mutexes replace filesystem advisory locks.
* Objects are stored in the bucket with the keys `files/<id>.json.enc`,
  `files/<id>.meta.json`, and `templates/<schemaID>.json`.

See [examples/05-s3-compatible-storage.md](examples/05-s3-compatible-storage.md)
for a complete Docker Compose and Kubernetes walkthrough.

---

## Security Model

| Property | Implementation |
|----------|---------------|
| Encryption at rest | AES-256-GCM per entity; nonce and AEAD tag stored in `.meta.json` (FS) or `.meta.json` S3 object |
| Key distribution | RSA-OAEP wrap/unwrap; master key never leaves Main Worker in plaintext |
| In-memory only | Processing Workers clear keys on shutdown; keys are never written to disk or object storage |
| Tamper detection | AEAD tag checked on every decryption; reads fail closed on mismatch |
| Schema validation | JSON Schema draft-07 enforced before every write |
| Log redaction | No plaintext entity data or key material is emitted in logs |
| Token expiry | Worker tokens: 1 h (configurable). Client tokens: 24 h (configurable) |
| Path traversal | Entity keys, database names and schema IDs are validated to reject `/`, `\`, and `..` |
| Request body limit | REST PUT/schema endpoints reject bodies larger than 1 MiB |
| Admin endpoints | `GET /admin/workers` requires a valid Bearer token; `GET /admin/schemas` is public |
| Write durability (FS) | `fdatasync` before atomic rename guarantees no data loss on worker crash |
| S3 credentials | Pass via `S3_ACCESS_KEY` / `S3_SECRET_KEY` env vars, not CLI flags |

> **Important**: The `-master-key` flag value appears in the shell command
> history. In production, load the key from an environment variable or a
> secrets manager and pass it via a wrapper script.

---

## Caching Model

DeltaDatabase uses a **smart LRU-only cache** — data is held in memory until
LRU pressure forces it out.  There is no time-based TTL eviction.

### Write path (PUT)

1. The incoming JSON is validated against the schema.
2. The JSON is encrypted with AES-256-GCM and written atomically to disk.
3. The plaintext JSON is **immediately cached** in the LRU store.

### Read path (GET)

1. The cache is checked first.  On a **cache hit** the decrypted JSON is
   returned without any disk I/O.
2. On a **cache miss**:
   a. Check whether the cache has room for a new entry.
   b. If the cache is **full**, the **least-recently-used** entry is evicted
      to make room.
   c. The encrypted blob is read from disk, decrypted, and stored in cache.
3. Subsequent reads for the same key are served from cache until the entry is
   evicted by LRU pressure.

### Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `proc-worker -cache-size` | `256` | Maximum entries in the Processing Worker LRU cache |
| `proc-worker -cache-ttl` | `0` | `0` = LRU-only (no time expiry); positive value sets TTL |
| `main-worker -entity-cache-size` | `1024` | Maximum entries in the Main Worker LRU entity store |

---

## Benchmark Results

The following numbers were measured on a standard CI Linux runner
(2 vCPU, 7 GB RAM) with both workers running in-process (`go run`).
Real hardware will be faster.

| Benchmark | Mean latency | Throughput |
|-----------|-------------|------------|
| REST PUT (sequential, warm) | ~1 ms | ~1,000 ops/s |
| REST GET (warm cache, sequential) | ~1 ms | ~1,000 ops/s |
| REST PUT→GET round-trip | ~2 ms | ~520 ops/s |
| gRPC PUT (proc-worker direct) | ~0.6 ms | ~1,700 ops/s |
| gRPC GET (warm cache, proc-worker) | ~0.3 ms | ~3,500 ops/s |
| Concurrent PUT (32 threads × 20 each) | — | ~910 ops/s total |
| Concurrent GET (32 threads × 25 each) | — | ~970 ops/s total |
| Bulk write 1000 entities | ~1 s total | ~1,000 ops/s |

> Benchmarks are run with `pytest tests/test_benchmarks.py -v --benchmark-sort=mean`.
> Use `pytest-benchmark compare` to track regressions across runs.

---

## Testing

See [BUILDING.md](BUILDING.md) for instructions on running Go unit tests and
Python integration tests.

---

## Building from Source

For full build instructions, prerequisites, and testing setup, see
[BUILDING.md](BUILDING.md).

---

## Project Structure

```
.
├── cmd/
│   ├── main-worker/        # Main Worker entry point & server
│   │   ├── main.go
│   │   ├── server.go       # gRPC + REST handler
│   │   ├── frontend.go     # Embedded web UI + /api/login, /api/databases, /api/me
│   │   └── static/
│   │       ├── index.html  # Login page
│   │       ├── app.html    # Multi-page management SPA
│   │       └── css/
│   │           └── delta.css   # Self-contained design system
│   └── proc-worker/        # Processing Worker entry point
│       ├── main.go
│       ├── worker.go       # Subscription & key management
│       └── server.go       # gRPC Process handler (GET/PUT)
├── pkg/
│   ├── crypto/             # AES-GCM encryption + RSA key wrapping
│   ├── cache/              # LRU + TTL in-memory cache
│   ├── fs/                 # Shared filesystem storage + file locking
│   └── schema/             # JSON Schema draft-07 validation
├── internal/
│   ├── auth/               # Token manager + worker authenticator
│   └── routing/            # Worker registry
├── api/
│   └── proto/              # Protobuf definitions + generated Go code
├── shared/
│   └── db/
│       ├── files/          # Encrypted entity blobs (runtime)
│       └── templates/      # JSON Schema templates
├── tests/                  # Python integration & end-to-end tests
├── deploy/
│   ├── docker/             # Dockerfiles (main-worker, proc-worker, all-in-one)
│   ├── docker-compose/     # Docker Compose configurations
│   └── kubernetes/         # Kubernetes manifests (Deployments, Services, HPA)
├── examples/               # Containerization deployment guides
├── Agents.md               # System design document
├── BUILDING.md             # Build from source & testing instructions
├── Guideline.md            # Coding standards
├── LICENSE                 # DeltaDatabase Software License v1.0
└── README.md               # This file
```

---

## License

DeltaDatabase is released under the **DeltaDatabase Software License v1.0**.

Key points:

* ✅ **Commercial use permitted** — you may use DeltaDatabase in commercial
  products and services.
* 🔒 **License is fork-locked** — any fork or derivative work must be
  distributed under this same license; you may not relicense.
* 📅 **Non-retroactive amendments** — future changes to the license apply
  only to future releases; past releases remain under their original terms.
* 🚫 **Funding restriction** — entities materially funded (>25%) by the
  governments of Israel or Russia may not use this software.
* 💚 **Charity requirement** — companies making Commercial Use must have
  donated ≥ USD $500 to a qualifying charity (Palestinian aid, Trans Rights,
  LGBTQ+ Rights, or Ukrainian aid) within the preceding 36 months.

See [LICENSE](LICENSE) for the full terms.
