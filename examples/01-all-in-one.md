# Example 1 — All-in-One (Single Docker Container)

Both the **Main Worker** and one **Processing Worker** run inside the same
container via a lightweight shell entrypoint script.

**Use this when:** you want the simplest possible deployment for local
development, CI pipelines, or resource-constrained edge nodes where running
two separate containers is unnecessary overhead.

---

## Files used

| File | Purpose |
|------|---------|
| `deploy/docker/Dockerfile.all-in-one` | Multi-stage build that compiles both binaries |
| `deploy/docker/entrypoint-all-in-one.sh` | Starts Main Worker, waits 2 s, then starts Processing Worker |
| `deploy/docker-compose/docker-compose.all-in-one.yml` | Convenience Compose wrapper |

---

## Quick start

### Option A — Docker Compose (recommended)

```bash
# From the repository root:
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml build
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up
```

The REST API is available at **http://localhost:8080**.

### Option B — Plain Docker

```bash
# Build
docker build \
  -f deploy/docker/Dockerfile.all-in-one \
  -t deltadatabase/all-in-one:latest \
  .

# Run (generate a fresh master key on startup)
docker run -d \
  --name deltadatabase \
  -p 8080:8080 \
  -v delta_data:/shared/db \
  deltadatabase/all-in-one:latest
```

Supply a fixed key to keep data readable across restarts:

```bash
# Generate a key once and save it
MASTER_KEY=$(openssl rand -hex 32)
echo "MASTER_KEY=${MASTER_KEY}" > .env

docker run -d \
  --name deltadatabase \
  -p 8080:8080 \
  -e MASTER_KEY="${MASTER_KEY}" \
  -v delta_data:/shared/db \
  deltadatabase/all-in-one:latest
```

---

## Verify it is running

```bash
# Health check
curl http://localhost:8080/health

# List workers registered with the Main Worker
curl -s http://localhost:8080/admin/workers | python3 -m json.tool
```

---

## Architecture inside the container

```
┌─────────────────────────────────────┐
│  Docker container                   │
│                                     │
│  main-worker  :8080 (REST)          │
│               :50051 (gRPC)         │
│        │                            │
│        │ gRPC subscribe             │
│        ▼                            │
│  proc-worker  :50052 (gRPC)         │
│                                     │
│  /shared/db  (named volume)         │
└─────────────────────────────────────┘
```

> **Note:** Because both processes share the same PID namespace you will see
> logs from both interleaved in `docker logs deltadatabase`.
