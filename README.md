# DeltaDatabase

> A lightweight, encrypted-at-rest JSON database written in Go — built for
> production-grade workloads that need per-entity encryption, JSON Schema
> validation, and a simple REST API.

[![License](https://img.shields.io/badge/license-DeltaDatabase%20v1.0-blue)](LICENSE)
[![Go version](https://img.shields.io/badge/go-1.25%2B-00ADD8)](go.mod)

---

## Table of Contents

1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Prerequisites](#prerequisites)
4. [Building from Source](#building-from-source)
5. [Quick Start](#quick-start)
6. [Configuration Reference](#configuration-reference)
7. [REST API Reference](#rest-api-reference)
8. [JSON Schema Templates](#json-schema-templates)
9. [Authentication](#authentication)
10. [Web Management UI](#web-management-ui)
11. [Chat Interface Example](#chat-interface-example)
    - [Go client](#go-client)
    - [Python client](#python-client)
    - [curl / shell](#curl--shell)
12. [Advanced: Running Multiple Workers](#advanced-running-multiple-workers)
13. [Security Model](#security-model)
14. [Testing](#testing)
15. [Project Structure](#project-structure)
16. [License](#license)

---

## Overview

DeltaDatabase stores arbitrary JSON documents — called **entities** — inside
named **databases**.  Every entity is:

* **Validated** against a JSON Schema template before being persisted.
* **Encrypted** at rest using AES-256-GCM before touching disk.
* **Cached** in memory by Processing Workers using an LRU + TTL policy, so
  repeated reads are served without disk I/O.
* **Accessed** through a plain HTTP REST API or gRPC, making integration
  straightforward from any programming language.

A built-in single-page web UI is served by the Main Worker at `/` so you can
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
                     Shared Filesystem
                     /shared/db/
                       ├── files/
                       │   ├── chatdb_Chat_id.json.enc
                       │   └── chatdb_Chat_id.meta.json
                       └── templates/
                           └── chat.v1.json
```

**Main Worker** — single entry-point for all clients. Handles authentication,
token issuance, key distribution, and load-balancing across Processing Workers.

**Processing Worker** — the data plane. Subscribes to the Main Worker to
receive the AES master key (wrapped in the worker's RSA public key), then
handles `GET` and `PUT` operations: validate → encrypt/decrypt → read/write FS
→ update cache.

**Shared Filesystem** — any POSIX-compatible directory (local, NFS, CIFS, …).
All workers see the same directory; file-level advisory locks prevent
concurrent writes.

---

## Prerequisites

| Tool | Minimum version | Notes |
|------|-----------------|-------|
| Go   | 1.21            | Tested up to 1.25 |
| Git  | any             | |
| Python | 3.9+         | Integration tests only |

No external databases, message brokers, or container runtimes are required for
development.

---

## Building from Source

```bash
git clone https://github.com/DeltaRule/DeltaDatabase.git
cd DeltaDatabase

# Build both workers
go build -o bin/main-worker ./cmd/main-worker/
go build -o bin/proc-worker ./cmd/proc-worker/

# Verify
./bin/main-worker --help
./bin/proc-worker --help
```

Run all Go unit tests:

```bash
go test ./...
```

---

## Quick Start

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

### 4. Obtain a client token

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"myapp"}' | jq -r .token)

echo "Token: $TOKEN"
```

### 5. Store an entity

```bash
curl -s -X PUT http://127.0.0.1:8080/entity/chatdb \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"session_001": {"messages": [{"role":"user","content":"Hello!"}]}}'
```

Response:

```json
{"status":"ok"}
```

### 6. Retrieve the entity

```bash
curl -s "http://127.0.0.1:8080/entity/chatdb?key=session_001" \
  -H "Authorization: Bearer $TOKEN"
```

Response:

```json
{"messages":[{"role":"user","content":"Hello!"}]}
```

---

## Configuration Reference

### Main Worker flags

| Flag | Default | Description |
|------|---------|-------------|
| `-grpc-addr` | `127.0.0.1:50051` | TCP address for the gRPC server |
| `-rest-addr` | `127.0.0.1:8080` | TCP address for the REST HTTP server |
| `-shared-fs` | `./shared/db` | Path to the shared filesystem root |
| `-master-key` | *(auto-generated)* | Hex-encoded 32-byte AES master key |
| `-key-id` | `main-key-v1` | Logical identifier for the master key |
| `-worker-ttl` | `1h` | TTL for Processing Worker session tokens |
| `-client-ttl` | `24h` | TTL for client Bearer tokens |

### Processing Worker flags

| Flag | Default | Description |
|------|---------|-------------|
| `-main-addr` | `127.0.0.1:50051` | Main Worker gRPC address |
| `-worker-id` | *(hostname)* | Unique ID for this worker |
| `-grpc-addr` | `127.0.0.1:0` | TCP address for this worker's gRPC server |
| `-shared-fs` | `./shared/db` | Path to the shared filesystem root |
| `-cache-size` | `256` | Maximum number of cached entities |
| `-cache-ttl` | `5m` | Time-to-live per cache entry |

---

## REST API Reference

All entity endpoints require an `Authorization: Bearer <token>` header.

### `POST /api/login`

Obtain a client Bearer token.

**Request body:**

```json
{ "client_id": "myapp" }
```

**Response:**

```json
{
  "token":      "bWDQOfIs…",
  "client_id":  "myapp",
  "expires_at": "2026-02-25T12:00:00Z"
}
```

---

### `GET /health`

Returns the system health status. No authentication required.

**Response:**

```json
{ "status": "ok" }
```

---

### `GET /admin/workers`

Returns a list of all registered Processing Workers and their status. No
authentication required.

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

## JSON Schema Templates

DeltaDatabase validates every `PUT` payload against a JSON Schema template
(draft-07) before encryption and storage. Templates are JSON files placed in
`{shared-fs}/templates/`.

### Creating a template

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

### Using a schema on a PUT (gRPC)

When calling the gRPC `Process` RPC directly, set the `schema_id` field of
`ProcessRequest` to `"chat.v1"`. The Processing Worker will reject any payload
that does not match the schema.

The REST API currently accepts any valid JSON; schema enforcement is applied
when the Main Worker routes to a Processing Worker with schema awareness.

---

## Authentication

DeltaDatabase uses a simple Bearer-token model for external clients:

1. **Login** — `POST /api/login` with your `client_id` returns a token valid
   for the configured `-client-ttl` (default 24 h).
2. **Authorize** — Include the token as `Authorization: Bearer <token>` on
   every `GET /entity/…` and `PUT /entity/…` request.
3. **Refresh** — Tokens are not refreshable; obtain a new token by logging in
   again before expiry.

Processing Workers use a separate RSA + token-based handshake with the Main
Worker (see [Architecture](#architecture)).

---

## Web Management UI

The Main Worker serves a built-in single-page management UI at `/`:

```
http://127.0.0.1:8080/
```

Features:

| Page | Description |
|------|-------------|
| **Dashboard** | Live health status and worker counts |
| **Workers** | Table of all registered Processing Workers with status, key ID, last-seen time, and tags |
| **Entities** | GET and PUT entities through a simple form; results displayed inline as formatted JSON |
| **API Explorer** | Send arbitrary GET/PUT requests to any endpoint; shows HTTP status and round-trip time |

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
    token      string
}

// NewChatClient logs in and returns a ready-to-use client.
func NewChatClient(baseURL, clientID string) (*ChatClient, error) {
    c := &ChatClient{httpClient: &http.Client{}, baseURL: baseURL}
    if err := c.login(clientID); err != nil {
        return nil, err
    }
    return c, nil
}

func (c *ChatClient) login(clientID string) error {
    body, _ := json.Marshal(map[string]string{"client_id": clientID})
    resp, err := c.httpClient.Post(c.baseURL+"/api/login",
        "application/json", bytes.NewReader(body))
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    var result struct {
        Token string `json:"token"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return err
    }
    c.token = result.Token
    return nil
}

func (c *ChatClient) doRequest(method, path string, body io.Reader) (*http.Response, error) {
    req, err := http.NewRequest(method, c.baseURL+path, body)
    if err != nil {
        return nil, err
    }
    req.Header.Set("Authorization", "Bearer "+c.token)
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
    // --- 1. Connect and authenticate ---
    client, err := NewChatClient(baseURL, "demo-app")
    if err != nil {
        panic(err)
    }
    fmt.Println("Logged in successfully")

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

    def __init__(self, base_url: str, client_id: str) -> None:
        self.base_url = base_url
        self.session = requests.Session()
        self._login(client_id)

    # ── Auth ────────────────────────────────────────────────────────

    def _login(self, client_id: str) -> None:
        resp = self.session.post(
            f"{self.base_url}/api/login",
            json={"client_id": client_id},
            timeout=10,
        )
        resp.raise_for_status()
        token = resp.json()["token"]
        self.session.headers.update({"Authorization": f"Bearer {token}"})
        print(f"Logged in as '{client_id}'")

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
    client = DeltaChatClient(BASE_URL, "python-demo")

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

# ── 1. Login ──────────────────────────────────────────────────────
TOKEN=$(curl -sf -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"bash-demo"}' | jq -r .token)

echo "Token: ${TOKEN:0:20}…"

# ── Helper: append one message ────────────────────────────────────
append_message() {
  local role="$1" content="$2"

  # Fetch current session (empty object if not found yet)
  existing=$(curl -sf "$BASE/entity/$DB?key=$SESSION" \
    -H "Authorization: Bearer $TOKEN" 2>/dev/null || echo '{"messages":[]}')

  # Build updated payload using jq
  updated=$(echo "$existing" | jq \
    --arg role "$role" --arg content "$content" \
    '.messages += [{"role":$role,"content":$content}]')

  # PUT back
  curl -sf -X PUT "$BASE/entity/$DB" \
    -H "Authorization: Bearer $TOKEN" \
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
  -H "Authorization: Bearer $TOKEN" | jq .
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

The Main Worker round-robins GET requests across all available workers. File-
level advisory locks (`flock`-style) ensure that concurrent writes from
different workers never corrupt an entity.

View the registered workers at any time:

```bash
curl -s http://127.0.0.1:8080/admin/workers | jq .
```

---

## Security Model

| Property | Implementation |
|----------|---------------|
| Encryption at rest | AES-256-GCM per entity; nonce and AEAD tag stored in `.meta.json` |
| Key distribution | RSA-OAEP wrap/unwrap; master key never leaves Main Worker in plaintext |
| In-memory only | Processing Workers clear keys on shutdown; keys are never written to disk |
| Tamper detection | AEAD tag checked on every decryption; reads fail closed on mismatch |
| Schema validation | JSON Schema draft-07 enforced before every write |
| Log redaction | No plaintext entity data or key material is emitted in logs |
| Token expiry | Worker tokens: 1 h (configurable). Client tokens: 24 h (configurable) |

> **Important**: The `-master-key` flag value appears in the shell command
> history. In production, load the key from an environment variable or a
> secrets manager and pass it via a wrapper script.

---

## Testing

### Go unit tests

```bash
go test ./...
```

### Python integration tests

Install dependencies once:

```bash
cd tests
pip install -r requirements.txt
```

Run individual test suites:

```bash
# Authentication tests
pytest tests/test_authentication.py -v

# Encryption tests
pytest tests/test_encryption.py -v

# Full end-to-end suite (requires both workers running)
pytest tests/test_whole.py -v
```

---

## Project Structure

```
.
├── cmd/
│   ├── main-worker/        # Main Worker entry point & server
│   │   ├── main.go
│   │   ├── server.go       # gRPC + REST handler
│   │   ├── frontend.go     # Embedded web UI
│   │   └── static/
│   │       └── index.html  # Single-page management app
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
├── Agents.md               # System design document
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
