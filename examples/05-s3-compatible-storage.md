# Example 5 — S3-Compatible Object Storage Backend

DeltaDatabase supports replacing the shared PersistentVolumeClaim with any
**S3-compatible object store**, including:

| Service | Notes |
|---------|-------|
| **AWS S3** | Native; no endpoint override needed |
| **[RustFS](https://github.com/rustfs/rustfs)** | High-performance Rust-based S3-compatible store |
| **[SeaweedFS](https://github.com/seaweedfs/seaweedfs)** | Distributed blob / S3 store |
| **[MinIO](https://min.io/)** | Popular self-hosted S3-compatible store |

When the `-s3-endpoint` flag is provided, Processing Workers store encrypted
blobs and metadata in the bucket instead of on a mounted filesystem.  The
Main Worker continues to use its own small local volume for session state.

---

## Object layout inside the bucket

```
<bucket>/
├── files/
│   ├── <entityID>.json.enc    ← AES-GCM encrypted blob
│   └── <entityID>.meta.json   ← encryption metadata (IV, tag, schema, version …)
└── templates/
    └── <schemaID>.json        ← JSON Schema templates (optional)
```

---

## Docker Compose quick-start (RustFS)

```bash
# From the repository root:

# Optional: set your own credentials
export S3_ACCESS_KEY=myaccesskey
export S3_SECRET_KEY=mysecretkey
export MASTER_KEY=$(openssl rand -hex 32)

docker compose -f deploy/docker-compose/docker-compose.s3.yml build
docker compose -f deploy/docker-compose/docker-compose.s3.yml up
```

The REST API is available at **http://localhost:8080**.  
The RustFS console is available at **http://localhost:9001**.

### Using SeaweedFS instead

Replace the `rustfs` service image in `deploy/docker-compose/docker-compose.s3.yml`:

```yaml
rustfs:
  image: chrislusf/seaweedfs:latest
  command: server -s3
  environment:
    # SeaweedFS uses its own config format; see SeaweedFS docs for S3 gateway setup
```

### Using MinIO instead

```yaml
rustfs:
  image: minio/minio:latest
  command: server /data --console-address ":9001"
  environment:
    MINIO_ROOT_USER: "${S3_ACCESS_KEY:-minioadmin}"
    MINIO_ROOT_PASSWORD: "${S3_SECRET_KEY:-minioadmin}"
```

---

## Kubernetes deployment

```bash
# 1. Create namespace and secrets
kubectl create namespace deltadatabase

kubectl -n deltadatabase create secret generic delta-master-key \
  --from-literal=master-key="$(openssl rand -hex 32)"

kubectl -n deltadatabase create secret generic delta-s3-credentials \
  --from-literal=access-key="<your-access-key>" \
  --from-literal=secret-key="<your-secret-key>"

# 2. Deploy Main Worker (same manifest as the local-FS topology)
kubectl apply -f deploy/kubernetes/main-worker.yaml

# 3. Deploy Processing Workers with S3 backend
#    Edit deploy/kubernetes/proc-worker-s3.yaml to set S3_ENDPOINT and S3_BUCKET
kubectl apply -f deploy/kubernetes/proc-worker-s3.yaml

# 4. (Optional) Autoscale Processing Workers
kubectl apply -f deploy/kubernetes/proc-worker-hpa.yaml

# 5. Verify
kubectl -n deltadatabase rollout status deployment/proc-worker
kubectl -n deltadatabase port-forward svc/main-worker 8080:8080 &
curl http://localhost:8080/health
```

---

## Processing Worker flags

| Flag | Default | Description |
|------|---------|-------------|
| `-s3-endpoint` | *(empty)* | S3 endpoint URL. Empty = local filesystem backend. |
| `-s3-bucket` | *(required)* | Bucket name |
| `-s3-region` | `us-east-1` | Region (placeholder accepted for self-hosted services) |
| `-s3-access-key` | *(empty)* | Access key (falls back to AWS credential chain) |
| `-s3-secret-key` | *(empty)* | Secret access key |
| `-s3-path-style` | `true` | Enable path-style addressing (required for self-hosted services) |

### Example command

```bash
proc-worker \
  -main-addr=main-worker:50051 \
  -worker-id=proc-1 \
  -grpc-addr=0.0.0.0:50052 \
  -s3-endpoint=http://rustfs:9000 \
  -s3-bucket=delta-db \
  -s3-access-key=minioadmin \
  -s3-secret-key=minioadmin \
  -s3-path-style=true
```

---

## Schema validation with S3 backend

When using the S3 backend, the local templates directory is not available, so
**schema validation is disabled by default** (a warning is logged at startup).

To enable schema validation with S3 storage, write your JSON Schema templates
directly into the bucket under the `templates/` prefix using the REST API or
the AWS CLI:

```bash
# Upload a schema template via the REST API
curl -X POST http://localhost:8080/schemas/chat.v1 \
  -H "Content-Type: application/json" \
  -d @shared/db/templates/chat.v1.json

# Or directly via AWS CLI
aws --endpoint-url http://localhost:9000 s3 cp \
  shared/db/templates/chat.v1.json \
  s3://delta-db/templates/chat.v1.json
```

---

## Locking with S3 backend

The S3 backend uses an **in-process memory lock** (per entity). This is
correct and safe for single-pod deployments.

> **Multi-pod caveat:** When multiple Processing Worker pods share the same
> S3 bucket, concurrent writes to the same entity from different pods are not
> coordinated by DeltaDatabase.  For write-heavy multi-pod deployments, route
> writes for a given entity always to the same pod (consistent hashing), or
> add an external distributed lock (e.g. DynamoDB conditional writes or a
> Redis-based lock).

---

## Architecture

```
  Internet / Ingress
        │
        ▼  REST :8080
 ┌──────────────────┐
 │  main-worker     │  (local volume for session state)
 └────────┬─────────┘
          │  gRPC :50051
          │
     ┌────┴──────────────────────┐
     │                           │
  proc-worker-1          proc-worker-2   (HPA: 1–10 replicas)
     │                           │
     └─────────┬─────────────────┘
               │  S3 API
      ┌─────────────────┐
      │  RustFS / S3    │  (s3://delta-db/)
      └─────────────────┘
```
