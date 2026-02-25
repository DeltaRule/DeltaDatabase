
# Examples

The following examples show how to use DeltaDatabase for different real-world applications. Each example includes complete, runnable code in multiple languages.

---

## Available Examples

| Example | Use Case | Languages |
|---------|---------|-----------|
| [Chat Application](chat-app) | Store AI/LLM conversation histories | Go, Python, curl |
| [User Profiles](user-profiles) | CRUD for user accounts and settings | Python, curl |
| [IoT Sensor Data](iot-sensors) | Store time-series sensor readings | Python, curl |
| [Configuration Store](config-store) | Manage per-service and per-environment config | Go, curl |
| [E-Commerce Catalogue](ecommerce) | Product catalogue and inventory | Python, curl |

---

## Common Pattern

All examples follow the same three-step pattern:

```
1. Login  →  POST /api/login  →  get Bearer token
2. Write  →  PUT /entity/{db}  →  store JSON entity
3. Read   →  GET /entity/{db}?key=...  →  retrieve entity
```

### Minimal Working Example (curl)

```bash
BASE="http://127.0.0.1:8080"

# 1. Get a token
TOKEN=$(curl -sf -X POST "$BASE/api/login" \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"demo"}' | jq -r .token)

# 2. Write an entity
curl -sf -X PUT "$BASE/entity/mydb" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"my_key": {"hello": "world"}}'

# 3. Read it back
curl -sf "$BASE/entity/mydb?key=my_key" \
  -H "Authorization: Bearer $TOKEN"
# → {"hello":"world"}
```

---

## Before Running Examples

Make sure DeltaDatabase is running:

```bash
# Quickest way — Docker all-in-one
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up -d

# Verify
curl http://127.0.0.1:8080/health
# → {"status":"ok"}
```
