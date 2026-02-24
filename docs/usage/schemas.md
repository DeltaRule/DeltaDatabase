---
layout: default
title: JSON Schema Templates
parent: Usage
nav_order: 5
---

# JSON Schema Templates

DeltaDatabase validates every `PUT` entity payload against a **JSON Schema (draft-07)** template before encryption and storage. Invalid data is rejected with HTTP `400`.

Schemas are stored in `{shared-fs}/templates/` (or as S3 objects under `templates/`) and can be managed via the REST API, the web UI, or by placing files directly on disk.

---

## Why Use Schemas?

- **Data integrity** — ensure every stored entity has the expected fields and types.
- **Early rejection** — bad data is caught before encryption and disk I/O.
- **Self-documenting** — schemas serve as the authoritative contract for your data model.

---

## Creating a Schema

### Via the REST API (recommended)

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"admin"}' | jq -r .token)

curl -s -X PUT http://127.0.0.1:8080/schema/chat.v1 \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "$id": "chat.v1",
    "type": "object",
    "properties": {
      "messages": {
        "type": "array",
        "items": {
          "type": "object",
          "properties": {
            "role":    {"type": "string", "enum": ["user", "assistant", "system"]},
            "content": {"type": "string"}
          },
          "required": ["role", "content"]
        }
      }
    },
    "required": ["messages"]
  }'
```

### Via the Web UI

Open **http://localhost:8080/** → **Schemas** tab → **New Schema** and paste your JSON Schema document.

### Directly on Disk

Place a file at `{shared-fs}/templates/{schemaID}.json`:

```bash
cat > ./shared/db/templates/chat.v1.json << 'EOF'
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "chat.v1",
  "type": "object",
  "properties": {
    "messages": {"type": "array"}
  },
  "required": ["messages"]
}
EOF
```

The worker picks up the file automatically — no restart required.

---

## Listing and Retrieving Schemas

```bash
# List all registered schema IDs
curl http://127.0.0.1:8080/admin/schemas

# Retrieve a specific schema
curl http://127.0.0.1:8080/schema/chat.v1
```

---

## Schema Naming Convention

Schema IDs use the format `{name}.{version}`:

| Schema ID | Purpose |
|-----------|---------|
| `chat.v1` | Chat session with messages array |
| `user.v1` | User profile data |
| `product.v2` | Product catalogue entry (v2 with new fields) |
| `iot_reading.v1` | IoT sensor reading |

---

## Example Schemas

### Chat Messages

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
          "role":    {"type": "string", "enum": ["user", "assistant", "system"]},
          "content": {"type": "string"}
        },
        "required": ["role", "content"]
      }
    }
  },
  "required": ["messages"]
}
```

### User Profile

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "user.v1",
  "type": "object",
  "properties": {
    "id":         {"type": "string"},
    "name":       {"type": "string"},
    "email":      {"type": "string", "format": "email"},
    "created_at": {"type": "string", "format": "date-time"},
    "settings":   {"type": "object"}
  },
  "required": ["id", "email"]
}
```

### IoT Sensor Reading

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "iot_reading.v1",
  "type": "object",
  "properties": {
    "device_id":   {"type": "string"},
    "timestamp":   {"type": "string", "format": "date-time"},
    "temperature": {"type": "number"},
    "humidity":    {"type": "number", "minimum": 0, "maximum": 100},
    "location":    {
      "type": "object",
      "properties": {
        "lat": {"type": "number"},
        "lng": {"type": "number"}
      }
    }
  },
  "required": ["device_id", "timestamp"]
}
```

### Product Catalogue

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "product.v1",
  "type": "object",
  "properties": {
    "sku":         {"type": "string"},
    "name":        {"type": "string"},
    "description": {"type": "string"},
    "price":       {"type": "number", "minimum": 0},
    "currency":    {"type": "string", "enum": ["USD", "EUR", "GBP"]},
    "in_stock":    {"type": "boolean"},
    "tags":        {"type": "array", "items": {"type": "string"}}
  },
  "required": ["sku", "name", "price", "currency"]
}
```

---

## What Happens on Validation Failure

If a `PUT /entity/{database}` request contains data that does not match the registered schema:

1. The Processing Worker rejects the payload.
2. The Main Worker returns `HTTP 400` with an error message.
3. Nothing is written to disk.

Example:

```bash
# This will fail — "messages" is required but missing
curl -s -X PUT http://127.0.0.1:8080/entity/chatdb \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"session_bad": {"wrong_field": "oops"}}'
```

```json
{"error": "schema validation failed: messages is required"}
```

---

## Schema Versioning

When you need to evolve your data model:

1. Create a new schema with an incremented version: `chat.v2`.
2. Write new entities using the new schema.
3. Old entities remain stored under their original schema ID.

There is no automatic migration — migration logic belongs in your application.
