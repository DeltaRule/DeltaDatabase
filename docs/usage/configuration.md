
# Configuration Reference

Both workers are configured entirely through **command-line flags**. Some sensitive values (S3 credentials) can be supplied as **environment variables** instead to keep them out of process argument lists.

---

## Main Worker Flags

Start the Main Worker with `./bin/main-worker [flags]`.

| Flag | Default | Description |
|------|---------|-------------|
| `-grpc-addr` | `127.0.0.1:50051` | TCP address for the gRPC server (used by Processing Workers) |
| `-rest-addr` | `127.0.0.1:8080` | TCP address for the REST HTTP server (used by clients) |
| `-shared-fs` | `./shared/db` | Path to the shared filesystem root. Ignored when `-s3-endpoint` is set |
| `-master-key` | *(auto-generated)* | Hex-encoded 32-byte AES master key. If omitted, a new random key is generated on startup |
| `-key-id` | `main-key-v1` | Logical identifier for the master key (stored in entity metadata) |
| `-worker-ttl` | `1h` | TTL for Processing Worker session tokens |
| `-client-ttl` | `24h` | TTL for client Bearer tokens |
| `-grpc-max-recv-msg-size` | `4194304` (4 MiB) | Maximum gRPC message size in bytes the server will accept. Increase this when storing large JSON payloads via gRPC |
| `-rest-max-body-size` | `1048576` (1 MiB) | Maximum HTTP request body size in bytes for entity and schema PUT endpoints. Increase this when storing large JSON payloads via the REST API |
| `-s3-endpoint` | *(empty)* | S3-compatible endpoint (e.g. `minio:9000`). Setting this enables the S3 backend |
| `-s3-access-key` | *(empty)* | S3 access key ID. Prefer the `S3_ACCESS_KEY` env var |
| `-s3-secret-key` | *(empty)* | S3 secret access key. Prefer the `S3_SECRET_KEY` env var |
| `-s3-bucket` | `deltadatabase` | S3 bucket name |
| `-s3-use-ssl` | `false` | Enable TLS for the S3 connection. Set `true` for AWS S3 |
| `-s3-region` | *(empty)* | S3 region. Optional — leave empty for MinIO/SeaweedFS |

### Example: Main Worker with a persistent key

```bash
./bin/main-worker \
  -grpc-addr=0.0.0.0:50051 \
  -rest-addr=0.0.0.0:8080 \
  -shared-fs=/data/db \
  -master-key=a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9 \
  -client-ttl=12h \
  -worker-ttl=30m
```

### Example: Main Worker with MinIO

```bash
./bin/main-worker \
  -grpc-addr=0.0.0.0:50051 \
  -rest-addr=0.0.0.0:8080 \
  -s3-endpoint=minio:9000 \
  -s3-bucket=deltadatabase \
  -s3-use-ssl=false
```

---

## Processing Worker Flags

Start the Processing Worker with `./bin/proc-worker [flags]`.

| Flag | Default | Description |
|------|---------|-------------|
| `-main-addr` | `127.0.0.1:50051` | Main Worker gRPC address to subscribe to |
| `-worker-id` | *(hostname)* | Unique identifier for this worker instance |
| `-grpc-addr` | `127.0.0.1:0` | TCP address for this worker's own gRPC server |
| `-shared-fs` | `./shared/db` | Path to the shared filesystem root. Ignored when `-s3-endpoint` is set |
| `-cache-size` | `256` | Maximum number of entities to keep in the LRU cache |
| `-cache-ttl` | `0` | TTL per cache entry. `0` = LRU-only eviction, no time-based expiry |
| `-grpc-max-recv-msg-size` | `4194304` (4 MiB) | Maximum gRPC message size in bytes this worker will accept. Must be set to at least the same value as the Main Worker's `-grpc-max-recv-msg-size` when handling large payloads |
| `-s3-endpoint` | *(empty)* | S3-compatible endpoint. Setting this enables the S3 backend |
| `-s3-access-key` | *(empty)* | S3 access key ID. Prefer the `S3_ACCESS_KEY` env var |
| `-s3-secret-key` | *(empty)* | S3 secret access key. Prefer the `S3_SECRET_KEY` env var |
| `-s3-bucket` | `deltadatabase` | S3 bucket name |
| `-s3-use-ssl` | `false` | Enable TLS for the S3 connection |
| `-s3-region` | *(empty)* | S3 region. Optional |

### Example: Processing Worker with a large cache

```bash
./bin/proc-worker \
  -main-addr=main-worker:50051 \
  -worker-id=proc-1 \
  -grpc-addr=0.0.0.0:50052 \
  -shared-fs=/data/db \
  -cache-size=1024
```

### Example: Processing Worker with AWS S3

```bash
export S3_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE
export S3_SECRET_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY

./bin/proc-worker \
  -main-addr=main-worker:50051 \
  -worker-id=proc-1 \
  -grpc-addr=0.0.0.0:50052 \
  -s3-endpoint=s3.amazonaws.com \
  -s3-use-ssl=true \
  -s3-region=us-east-1 \
  -s3-bucket=my-deltadatabase-bucket
```

---

## Environment Variables

| Variable | Equivalent Flag | Workers |
|----------|----------------|---------|
| `S3_ACCESS_KEY` | `-s3-access-key` | Both |
| `S3_SECRET_KEY` | `-s3-secret-key` | Both |

!!! warning
    **Security note:** The `-master-key` flag value appears in the shell command history. In production, load the key from an environment variable or a secrets manager and pass it via a wrapper script.
    
---

## Configuration in Docker Compose

When using Docker Compose, pass flags via the `command:` field and environment variables via `environment:`:

```yaml
services:
  main-worker:
    image: deltadatabase/main-worker:latest
    command: >
      -grpc-addr=0.0.0.0:50051
      -rest-addr=0.0.0.0:8080
      -shared-fs=/shared/db
    environment:
      - MASTER_KEY=${MASTER_KEY}
    ports:
      - "8080:8080"
      - "50051:50051"
```

---

## Configuration in Kubernetes

Use a `Secret` for the master key and a `ConfigMap` for non-sensitive settings:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: delta-master-key
  namespace: deltadatabase
stringData:
  master-key: "a1b2c3d4..."  # your hex key here
```

Reference it in the Deployment:

```yaml
env:
  - name: MASTER_KEY
    valueFrom:
      secretKeyRef:
        name: delta-master-key
        key: master-key
```

See the [Deployment guide](deployment) for complete Kubernetes examples.

---

## Configuring Maximum Data / Payload Size

By default DeltaDatabase accepts up to **4 MiB per gRPC message** and **1 MiB per REST request body**.
If your entities are larger you must raise both limits consistently across Main Worker and every Processing Worker.

### REST clients (curl, Python, browser UI)

Use `-rest-max-body-size` on the Main Worker:

```bash
./bin/main-worker \
  -rest-addr=0.0.0.0:8080 \
  -rest-max-body-size=10485760   # 10 MiB
```

### gRPC clients

Use `-grpc-max-recv-msg-size` on **both** the Main Worker and every Processing Worker.
Both values should be identical so that forwarded messages are accepted end-to-end:

```bash
# Main Worker
./bin/main-worker \
  -grpc-addr=0.0.0.0:50051 \
  -grpc-max-recv-msg-size=10485760   # 10 MiB

# Processing Worker (must match the Main Worker's setting)
./bin/proc-worker \
  -main-addr=main-worker:50051 \
  -grpc-addr=0.0.0.0:50052 \
  -grpc-max-recv-msg-size=10485760   # 10 MiB
```

### Docker Compose example

```yaml
services:
  main-worker:
    image: donti/deltadatabase:latest-main
    command: >
      -grpc-addr=0.0.0.0:50051
      -rest-addr=0.0.0.0:8080
      -grpc-max-recv-msg-size=10485760
      -rest-max-body-size=10485760

  proc-worker:
    image: donti/deltadatabase:latest-proc
    command: >
      -main-addr=main-worker:50051
      -grpc-addr=0.0.0.0:50052
      -grpc-max-recv-msg-size=10485760
```

!!! note
    The `/api/keys` management endpoint always limits request bodies to 64 KiB regardless of `-rest-max-body-size`.  That endpoint only handles small JSON payloads (API key metadata) and the smaller limit is intentional.
