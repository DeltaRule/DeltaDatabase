# Example 5 — S3-Compatible Object Storage

This example shows how to run DeltaDatabase with an **S3-compatible object
store** instead of (or alongside) a shared POSIX filesystem.  Any service that
implements the S3 API works out of the box:

| Service | Description |
|---------|-------------|
| **MinIO** | Open-source, self-hosted, Kubernetes-native |
| **RustFS** | High-performance S3-compatible object storage written in Rust |
| **SeaweedFS** | Distributed storage with an S3-compatible gateway |
| **AWS S3** | Amazon managed object storage |
| **Ceph/RadosGW** | Ceph object gateway (S3-compatible) |

---

## Why use S3-compatible storage?

| Feature | Shared FS | S3-compatible |
|---------|-----------|---------------|
| Horizontal scale-out | Requires an RWX volume (NFS, CephFS, …) | Works with any S3 endpoint — no shared volume needed |
| Cross-cloud / hybrid | Single cloud / on-prem | Multi-cloud, on-prem, or hybrid |
| Data durability | Depends on volume configuration | Built-in replication and versioning |
| Locking mechanism | POSIX advisory flock (per-file) | In-process mutex + S3 strong consistency |
| Setup complexity | Simple (a directory) | Requires an S3 endpoint and bucket |

Use **Shared FS** for simple single-machine or bare-metal deployments.  
Use **S3-compatible storage** when:
- You need to run Processing Workers in multiple Kubernetes nodes/clouds.
- You want managed storage durability (S3 replication / versioning).
- You cannot provision a ReadWriteMany PersistentVolumeClaim.

---

## Quick Start — Docker Compose with MinIO

```bash
# Clone and build
git clone https://github.com/DeltaRule/DeltaDatabase.git
cd DeltaDatabase

# Start MinIO + Main Worker + 3 Processing Workers
docker compose -f deploy/docker-compose/docker-compose.with-s3.yml up --build
```

This starts:

| Container | Description | Port |
|-----------|-------------|------|
| `delta-minio` | MinIO object storage | `9000` (S3 API), `9001` (web console) |
| `delta-minio-init` | One-shot bucket creation | — |
| `delta-main-worker` | Main Worker (REST + gRPC) | `8080`, `50051` |
| `proc-worker` × 3 | Processing Workers | `50052` |

Open the MinIO console at http://localhost:9001 (user: `minioadmin`,
password: `minioadmin`) to inspect stored objects.

---

## Using the API

Once running, the API is identical to the shared-FS setup:

```bash
# Obtain a token
TOKEN=$(curl -s -X POST http://127.0.0.1:8080/api/login \
  -H 'Content-Type: application/json' \
  -d '{"client_id":"myapp"}' | jq -r .token)

# Store an entity
curl -s -X PUT http://127.0.0.1:8080/entity/chatdb \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"session_001": {"messages": [{"role":"user","content":"Hello S3!"}]}}'

# Retrieve it
curl -s "http://127.0.0.1:8080/entity/chatdb?key=session_001" \
  -H "Authorization: Bearer $TOKEN"
```

Objects are stored in the `deltadatabase` bucket under these keys:

```
files/<entityID>.json.enc   — AES-256-GCM encrypted entity blob
files/<entityID>.meta.json  — metadata (key ID, IV, tag, schema, version)
templates/<schemaID>.json   — JSON Schema templates
```

---

## Configuration Flags

### Processing Worker (`proc-worker`)

| Flag | Default | Description |
|------|---------|-------------|
| `-s3-endpoint` | *(empty — disables S3)* | S3 service host:port, e.g. `minio:9000` |
| `-s3-access-key` | `""` | Access key ID (or set `S3_ACCESS_KEY` env var) |
| `-s3-secret-key` | `""` | Secret access key (or set `S3_SECRET_KEY` env var) |
| `-s3-bucket` | `deltadatabase` | Bucket name |
| `-s3-use-ssl` | `false` | Enable TLS (set to `true` for AWS S3) |
| `-s3-region` | `""` | Region (optional; leave empty for MinIO/SeaweedFS) |

### Main Worker (`main-worker`)

The same `-s3-*` flags are accepted.  The Main Worker uses S3 to store JSON
Schema templates so they are accessible to all Processing Workers.

---

## Environment Variable Credentials

To avoid exposing credentials in the process argument list, set:

```bash
export S3_ACCESS_KEY=your-access-key
export S3_SECRET_KEY=your-secret-key
```

The workers read these variables automatically when the flag equivalents are
not provided.

---

## Kubernetes — MinIO + DeltaDatabase

Apply the S3 configuration first, then the worker deployments:

```bash
# Create namespace
kubectl create namespace deltadatabase

# Deploy MinIO + bucket init Job + Secret/ConfigMap
kubectl apply -f deploy/kubernetes/s3-config.yaml

# Wait for the bucket init job to complete
kubectl wait --for=condition=complete job/minio-bucket-init -n deltadatabase --timeout=120s

# Deploy the Main Worker (with S3 flags in your overlay)
kubectl apply -f deploy/kubernetes/main-worker.yaml

# Deploy Processing Workers (with S3 flags in your overlay)
kubectl apply -f deploy/kubernetes/proc-worker.yaml
```

### Patching the worker deployments for S3

Add the following to the `args` of both worker containers and inject the
Secret as environment variables:

```yaml
# In main-worker.yaml / proc-worker.yaml
env:
  - name: S3_ACCESS_KEY
    valueFrom:
      secretKeyRef:
        name: delta-s3-credentials
        key: access-key
  - name: S3_SECRET_KEY
    valueFrom:
      secretKeyRef:
        name: delta-s3-credentials
        key: secret-key
command:
  - "sh"
  - "-c"
  - >
    exec proc-worker
    -main-addr=main-worker:50051
    -worker-id=$(POD_NAME)
    -grpc-addr=0.0.0.0:50052
    -s3-endpoint=$(S3_ENDPOINT)
    -s3-bucket=$(S3_BUCKET)
    -s3-use-ssl=$(S3_USE_SSL)
envFrom:
  - configMapRef:
      name: delta-s3-config
```

When `-s3-endpoint` is set, **no shared PersistentVolumeClaim is needed**.
You can remove the `volumes` and `volumeMounts` blocks from the worker
deployments and delete `deploy/kubernetes/shared-pvc.yaml` from your manifests.

---

## AWS S3

Replace MinIO with AWS S3 by setting:

| Setting | Value |
|---------|-------|
| `-s3-endpoint` | `s3.amazonaws.com` |
| `-s3-access-key` | Your AWS Access Key ID |
| `-s3-secret-key` | Your AWS Secret Access Key |
| `-s3-use-ssl` | `true` |
| `-s3-region` | Your bucket region, e.g. `us-east-1` |
| `-s3-bucket` | Your S3 bucket name |

Example:

```bash
proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -s3-endpoint=s3.amazonaws.com \
  -s3-use-ssl=true \
  -s3-region=us-east-1 \
  -s3-bucket=my-deltadatabase-bucket \
  -s3-access-key=AKIAIOSFODNN7EXAMPLE \
  -s3-secret-key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
```

---

## RustFS / SeaweedFS

Both expose a standard S3 API.  Use the same flags as MinIO:

```bash
# SeaweedFS S3 gateway (default port 8333)
proc-worker \
  -s3-endpoint=seaweedfs-s3:8333 \
  -s3-bucket=deltadatabase \
  -s3-use-ssl=false \
  -s3-access-key=any \
  -s3-secret-key=any
```

---

## Mixing Shared FS and S3

The two backends are mutually exclusive per worker process.  All workers in a
deployment must use **the same backend** (either all shared-FS or all S3) to
ensure every worker reads and writes to the same data store.
