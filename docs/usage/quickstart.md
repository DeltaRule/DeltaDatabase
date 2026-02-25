
# Quick Start

Get DeltaDatabase running in under 5 minutes.

!!! tip
    **Recommended:** Use Docker for the easiest experience. The binary approach is shown below for local development.
    
---

## Option A — Docker (Recommended)

The quickest way to run DeltaDatabase is with the all-in-one Docker Compose file:

```bash
# Clone the repository
git clone https://github.com/DeltaRule/DeltaDatabase.git
cd DeltaDatabase

# Start everything (Main Worker + Processing Worker in one container)
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up
```

The REST API is available at **http://localhost:8080** and the web UI at **http://localhost:8080/**.

---

## Option B — Pre-built Binaries

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

Look for the generated master key in the startup output and save it:

```
2026/02/24 12:00:00 Key (hex): a1b2c3d4...  ← save this for restarts!
```

### 3. Start a Processing Worker

Open a second terminal:

```bash
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -shared-fs=./shared/db
```

---

## Your First API Calls

### Check health

```bash
curl http://127.0.0.1:8080/health
```

```json
{"status": "ok"}
```

### Get a token

```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"myapp"}' | jq -r .token)

echo "Token: $TOKEN"
```

### Store an entity

```bash
curl -s -X PUT http://127.0.0.1:8080/entity/mydb \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"hello_world": {"message": "Hello from DeltaDatabase!", "count": 1}}'
```

```json
{"status": "ok"}
```

### Retrieve the entity

```bash
curl -s "http://127.0.0.1:8080/entity/mydb?key=hello_world" \
  -H "Authorization: Bearer $TOKEN"
```

```json
{"message": "Hello from DeltaDatabase!", "count": 1}
```

### View all workers

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/admin/workers | jq .
```

```json
[
  {
    "worker_id": "proc-1",
    "status":    "Available",
    "key_id":    "main-key-v1",
    "last_seen": "2026-02-24T12:01:30Z",
    "tags":      {"grpc_addr": "127.0.0.1:50052"}
  }
]
```

---

## Web Management UI

Open **http://127.0.0.1:8080/** in your browser to access the built-in management UI.

| Tab | Description |
|-----|-------------|
| Dashboard | Live health status and worker count |
| Workers | All registered Processing Workers |
| Entities | GET and PUT entities through a form |
| Schemas | Manage JSON Schema templates |
| API Explorer | Send custom requests, see response and timing |

---

## Next Steps

- [Configure flags and environment variables](configuration)
- [Learn the full REST API](api-reference)
- [Set up JSON Schema validation](schemas)
- [Deploy with Docker Compose or Kubernetes](deployment)
- [See real-world examples](examples/)
