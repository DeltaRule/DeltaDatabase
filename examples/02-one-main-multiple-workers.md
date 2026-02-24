# Example 2 — 1 Main Worker + Multiple Processing Workers (Docker Compose)

The **Main Worker** runs in its own container and multiple **Processing
Workers** run as replicas that all connect to it and share the same filesystem
volume.

**Use this when:** you need higher read/write throughput and want to scale
horizontally on a single machine (or a Docker Swarm node) without Kubernetes.

---

## Files used

| File | Purpose |
|------|---------|
| `deploy/docker/Dockerfile.main-worker` | Main Worker image |
| `deploy/docker/Dockerfile.proc-worker` | Processing Worker image |
| `deploy/docker-compose/docker-compose.one-main-multiple-workers.yml` | Compose file with a `proc-worker` service scaled to 3 replicas |

---

## Quick start

```bash
# From the repository root — start with the default 3 Processing Workers:
docker compose \
  -f deploy/docker-compose/docker-compose.one-main-multiple-workers.yml \
  up --build
```

### Change the number of workers

Set `WORKER_COUNT` or use the `--scale` flag:

```bash
# Scale to 5 Processing Workers
docker compose \
  -f deploy/docker-compose/docker-compose.one-main-multiple-workers.yml \
  up --build --scale proc-worker=5
```

Or edit the `replicas` value in the Compose file directly.

---

## Supply a persistent master key

```bash
MASTER_KEY=$(openssl rand -hex 32)
echo "MASTER_KEY=${MASTER_KEY}" > .env

docker compose \
  -f deploy/docker-compose/docker-compose.one-main-multiple-workers.yml \
  up --build
```

---

## Verify workers are registered

```bash
# Log in and inspect registered workers
TOKEN=$(curl -s -X POST http://localhost:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"admin"}' | python3 -c "import sys,json; print(json.load(sys.stdin)['token'])")

curl -s -H "Authorization: Bearer ${TOKEN}" \
  http://localhost:8080/admin/workers | python3 -m json.tool
```

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Docker Compose                                             │
│                                                             │
│  ┌──────────────┐   gRPC subscribe   ┌──────────────────┐  │
│  │  main-worker │◄───────────────────│  proc-worker-1   │  │
│  │  :8080 REST  │◄───────────────────│  proc-worker-2   │  │
│  │  :50051 gRPC │◄───────────────────│  proc-worker-3   │  │
│  └──────┬───────┘                    └────────┬─────────┘  │
│         │                                     │            │
│         └──────────── /shared/db (volume) ────┘            │
└─────────────────────────────────────────────────────────────┘
```

The Main Worker round-robins incoming entity requests across all registered
Processing Workers.  Adding more workers increases throughput linearly until
the shared filesystem becomes the bottleneck.
