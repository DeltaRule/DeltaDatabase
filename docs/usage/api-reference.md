---
layout: default
title: API Reference
parent: Usage
nav_order: 3
---

# REST API Reference

All endpoints are served by the **Main Worker** at the configured `-rest-addr` (default `127.0.0.1:8080`).

Entity endpoints require an `Authorization: Bearer <token>` header. See [Authentication](authentication) for how to obtain a token.

---

## Authentication

### `POST /api/login`

Obtain a client Bearer token.

**Request:**

```http
POST /api/login
Content-Type: application/json

{"client_id": "myapp"}
```

**Response `200 OK`:**

```json
{
  "token":      "bWDQOfIs…",
  "client_id":  "myapp",
  "expires_at": "2026-02-25T12:00:00Z"
}
```

**Example:**

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"myapp"}' | jq -r .token)
```

---

## Health

### `GET /health`

Returns system health. No authentication required.

**Response `200 OK`:**

```json
{"status": "ok"}
```

---

## Entities

### `PUT /entity/{database}`

Create or update one or more entities in a database.

**Path parameter:** `database` — name of the database (e.g., `chatdb`).

**Request:**

```http
PUT /entity/chatdb
Authorization: Bearer <token>
Content-Type: application/json

{
  "session_001": {"messages": [{"role": "user", "content": "Hello!"}]},
  "session_002": {"messages": [{"role": "user", "content": "Hi there!"}]}
}
```

The request body is a JSON **object** where each key is an entity key and each value is the entity's JSON document. Multiple entities can be written in a single request.

**Response `200 OK`:**

```json
{"status": "ok"}
```

**Error responses:**

| Code | Meaning |
|------|---------|
| `400` | Invalid JSON, schema validation failure, or body exceeds 1 MiB |
| `401` | Missing or invalid Bearer token |

**Example:**

```bash
curl -s -X PUT http://127.0.0.1:8080/entity/chatdb \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"session_001": {"messages": [{"role":"user","content":"Hello!"}]}}'
```

---

### `GET /entity/{database}?key={entityKey}`

Retrieve a single entity by key.

**Path parameter:** `database` — name of the database.

**Query parameter:** `key` — entity key.

**Request:**

```http
GET /entity/chatdb?key=session_001
Authorization: Bearer <token>
```

**Response `200 OK`** — the entity's JSON document:

```json
{"messages": [{"role": "user", "content": "Hello!"}]}
```

**Error responses:**

| Code | Meaning |
|------|---------|
| `400` | Missing `key` query parameter |
| `401` | Missing or invalid Bearer token |
| `404` | Entity not found |

**Example:**

```bash
curl -s "http://127.0.0.1:8080/entity/chatdb?key=session_001" \
  -H "Authorization: Bearer $TOKEN"
```

---

## Schemas

### `GET /admin/schemas`

List all registered schema IDs. No authentication required.

**Response `200 OK`:**

```json
["chat.v1", "user_credentials.v1", "user_chats.v1"]
```

---

### `GET /schema/{schemaID}`

Retrieve a JSON Schema document. No authentication required.

**Path parameter:** `schemaID` — the schema identifier (e.g., `chat.v1`).

**Response `200 OK`:**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "chat.v1",
  "type": "object",
  "properties": {
    "messages": {"type": "array"}
  },
  "required": ["messages"]
}
```

**Error responses:**

| Code | Meaning |
|------|---------|
| `404` | Schema not found |

---

### `PUT /schema/{schemaID}`

Create or replace a JSON Schema. Authentication required.

**Path parameter:** `schemaID` — the schema identifier.

**Request:**

```http
PUT /schema/product.v1
Authorization: Bearer <token>
Content-Type: application/json

{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "product.v1",
  "type": "object",
  "properties": {
    "name":  {"type": "string"},
    "price": {"type": "number", "minimum": 0}
  },
  "required": ["name", "price"]
}
```

**Response `200 OK`:**

```json
{"status": "ok"}
```

**Error responses:**

| Code | Meaning |
|------|---------|
| `400` | Invalid JSON or invalid JSON Schema |
| `401` | Missing or invalid Bearer token |

---

## Admin

### `GET /admin/workers`

Returns all registered Processing Workers and their status. Requires a valid Bearer token.

**Response `200 OK`:**

```json
[
  {
    "worker_id": "proc-1",
    "status":    "Available",
    "key_id":    "main-key-v1",
    "last_seen": "2026-02-24T12:01:30Z",
    "tags":      {"grpc_addr": "127.0.0.1:50052"}
  },
  {
    "worker_id": "proc-2",
    "status":    "Available",
    "key_id":    "main-key-v1",
    "last_seen": "2026-02-24T12:01:28Z",
    "tags":      {"grpc_addr": "127.0.0.1:50053"}
  }
]
```

---

## Error Format

All error responses return a JSON body with an `error` field:

```json
{"error": "entity not found"}
```

| HTTP Code | Meaning |
|-----------|---------|
| `200` | Success |
| `400` | Bad request (invalid JSON, schema violation, missing parameter) |
| `401` | Unauthorized (missing or expired token) |
| `404` | Not found (entity or schema does not exist) |
| `413` | Request body too large (exceeds 1 MiB limit) |
| `500` | Internal server error |

---

## Request Limits

| Limit | Value |
|-------|-------|
| Maximum body size (PUT entity, PUT schema) | 1 MiB |
| Entity key characters | No `/`, `\`, or `..` sequences |
| Database name characters | No `/`, `\`, or `..` sequences |
| Schema ID characters | No `/`, `\`, or `..` sequences |
