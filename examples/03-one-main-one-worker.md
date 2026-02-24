# Example 3 — 1 Main Worker + 1 Processing Worker (Docker Compose)

The simplest **production-like** topology: two separate containers, each with a
single responsibility, communicating over an internal Docker network and sharing
one named volume.

**Use this when:** you want a clean separation of concerns in a small
deployment, or as a starting point before adding more Processing Workers.

---

## Files used

| File | Purpose |
|------|---------|
| `deploy/docker/Dockerfile.main-worker` | Main Worker image |
| `deploy/docker/Dockerfile.proc-worker` | Processing Worker image |
| `deploy/docker-compose/docker-compose.one-main-one-worker.yml` | Compose file with one of each |

---

## Quick start

```bash
# From the repository root:
docker compose \
  -f deploy/docker-compose/docker-compose.one-main-one-worker.yml \
  up --build
```

The REST API is available at **http://localhost:8080**.

---

## Supply a persistent master key

```bash
MASTER_KEY=$(openssl rand -hex 32)
echo "MASTER_KEY=${MASTER_KEY}" > .env

docker compose \
  -f deploy/docker-compose/docker-compose.one-main-one-worker.yml \
  up --build
```

> **Important:** Store `MASTER_KEY` securely (e.g., in a secrets manager).
> Losing the key means losing access to all encrypted data.

---

## Verify both services are healthy

```bash
docker compose \
  -f deploy/docker-compose/docker-compose.one-main-one-worker.yml \
  ps
```

Expected output:

```
NAME                     SERVICE       STATUS     PORTS
delta-main-worker        main-worker   running    0.0.0.0:8080->8080/tcp
delta-proc-worker-1      proc-worker   running
```

```bash
curl http://localhost:8080/health
```

---

## Architecture

```
┌──────────────────────────────────────────────────────┐
│  Docker Compose                                      │
│                                                      │
│  ┌────────────────┐  gRPC :50051  ┌───────────────┐  │
│  │  main-worker   │◄──────────────│  proc-worker  │  │
│  │  :8080  REST   │               │               │  │
│  └───────┬────────┘               └───────┬───────┘  │
│          │                                │          │
│          └──────── /shared/db (volume) ───┘          │
└──────────────────────────────────────────────────────┘
```

### Scaling up later

If you later need more Processing Workers, switch to the
[multiple-worker Compose file](02-one-main-multiple-workers.md) — no data
migration is required because all workers share the same volume.
