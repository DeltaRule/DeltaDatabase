
# Deployment

DeltaDatabase supports several deployment topologies, from a single all-in-one container to a cloud-native Kubernetes cluster with autoscaling.

Pre-built images are published to Docker Hub automatically on every merge to `main` and on every release tag:

**Docker Hub:** [https://hub.docker.com/r/donti/deltadatabase](https://hub.docker.com/r/donti/deltadatabase)

| Image tag | Description |
|-----------|-------------|
| `donti/deltadatabase:latest-aio` | Both workers in one container (latest `main`) |
| `donti/deltadatabase:latest-main` | Main Worker only (latest `main`) |
| `donti/deltadatabase:latest-proc` | Processing Worker only (latest `main`) |
| `donti/deltadatabase:v0.1.1-alpha-aio` | Pinned release |
| `donti/deltadatabase:v0.1.1-alpha-main` | Pinned release |
| `donti/deltadatabase:v0.1.1-alpha-proc` | Pinned release |

---

## Deployment Topologies

| Scenario | Recommendation | Guide |
|----------|---------------|-------|
| Local development / CI | All-in-one container | [All-in-One](#all-in-one-single-container) |
| Small production | 1 Main + 1 Processing Worker | [1M + 1W](#1-main-worker--1-processing-worker) |
| Scale-out | 1 Main + N Processing Workers | [1M + NW](#1-main-worker--n-processing-workers) |
| Cloud / auto-scaling | Kubernetes + HPA | [Kubernetes](#kubernetes-with-autoscaling) |
| Managed storage | S3-compatible backend | [S3 Storage](#s3-compatible-storage) |

---

## All-in-One (Single Container)

Both workers run inside the same Docker container. Ideal for development, CI, or edge nodes.

### Docker Compose (recommended)

```bash
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up
```

The REST API is available at **http://localhost:8080**.

### Plain Docker

```bash
# Pull the latest image
docker pull donti/deltadatabase:latest-aio

# Run with a persistent master key and admin key
MASTER_KEY=$(openssl rand -hex 32)
ADMIN_KEY=$(openssl rand -hex 24)
docker run -d \
  --name deltadatabase \
  -p 8080:8080 \
  -e MASTER_KEY="${MASTER_KEY}" \
  -e ADMIN_KEY="${ADMIN_KEY}" \
  -v delta_data:/shared/db \
  donti/deltadatabase:latest-aio

# Pin to a specific release instead:
#   docker run ... donti/deltadatabase:v0.1.1-alpha-aio
```

### Container Architecture

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

---

## 1 Main Worker + 1 Processing Worker

The simplest production-like setup: two separate containers.

```bash
docker compose \
  -f deploy/docker-compose/docker-compose.one-main-one-worker.yml \
  up
```

---

## 1 Main Worker + N Processing Workers

Horizontal scale-out for higher throughput. All Processing Workers share the same filesystem volume.

```bash
# Start with 3 Processing Workers (default)
docker compose \
  -f deploy/docker-compose/docker-compose.one-main-multiple-workers.yml \
  up

# Scale to 5 workers
docker compose \
  -f deploy/docker-compose/docker-compose.one-main-multiple-workers.yml \
  up --scale proc-worker=5
```

### Architecture

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

---

## Kubernetes with Autoscaling

Processing Workers start at 1 replica and scale up to 10 based on memory utilisation (deallocation-based) via a `HorizontalPodAutoscaler`. Using memory as the scaling metric avoids spurious scale-up events from short CPU spikes and instead tracks actual working-set pressure, scaling down as memory is deallocated when load decreases.

### Prerequisites

- Kubernetes cluster v1.26+ with the Metrics Server installed.
- A ReadWriteMany StorageClass (NFS, Azure Files, AWS EFS, or Longhorn with RWX).

### Images

The Kubernetes manifests already reference the pre-built Docker Hub images:

```
donti/deltadatabase:latest-main
donti/deltadatabase:latest-proc
```

To pin a specific release, edit the `image:` field in
`deploy/kubernetes/main-worker.yaml` and `deploy/kubernetes/proc-worker.yaml`:

```yaml
# e.g. pin to v0.1.1-alpha
image: donti/deltadatabase:v0.1.1-alpha-main
```

### Deploy (individual manifests)

```bash
# Create namespace and secret
kubectl create namespace deltadatabase
MASTER_KEY=$(openssl rand -hex 32)
kubectl -n deltadatabase create secret generic delta-master-key \
  --from-literal=master-key="${MASTER_KEY}"

# Apply manifests
kubectl apply -f deploy/kubernetes/shared-pvc.yaml
kubectl apply -f deploy/kubernetes/main-worker.yaml
kubectl apply -f deploy/kubernetes/proc-worker.yaml
kubectl apply -f deploy/kubernetes/proc-worker-hpa.yaml

# Wait for rollout
kubectl -n deltadatabase rollout status deployment/main-worker
kubectl -n deltadatabase rollout status deployment/proc-worker
```

### Kubernetes Architecture

```
Internet / Ingress
       │
       ▼  REST :8080
┌──────────────────┐
│   main-worker    │  (Deployment, 1 replica)
│   ClusterIP svc  │
└────────┬─────────┘
         │  gRPC :50051
    ┌────┴──────────────────────────────┐
    │                                   │
 proc-worker-1  proc-worker-2  …  (HPA: 1–10)
    │                   │
    └───────────────────┘
          /shared/db  (ReadWriteMany PVC)
```

### HPA Behaviour

The `HorizontalPodAutoscaler` targets **80% memory utilisation** (deallocation-based scaling):

- Adds up to 2 new pods per 60 seconds when memory utilisation exceeds 80%.
- Removes 1 pod per 120 seconds as memory is deallocated and utilisation drops below the target.
- Always keeps at least 1 pod; never exceeds 10 pods.

---

## One-Command Kustomize Deploy (Shared FS + Autoscaling + Monitoring)

The `deploy/kubernetes/kustomize/` overlay bundles all components into a single `kubectl apply -k` command:

- **Shared filesystem** PVC (ReadWriteMany, no S3 dependency)
- **Memory-based autoscaling** HPA for Processing Workers
- **Prometheus** with Kubernetes service-discovery scraping both workers
- **Grafana** with the pre-built DeltaDatabase dashboard provisioned automatically

### Prerequisites

- Kubernetes cluster v1.26+ with the Metrics Server installed:
  ```bash
  kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
  ```
- A ReadWriteMany StorageClass. Edit `deploy/kubernetes/kustomize/shared-pvc.yaml` to set `storageClassName` to your provider (e.g., `nfs-client`, `azurefile`, `efs-sc`).

### Deploy

```bash
# 1. Create the namespace, master-key secret, and (optionally) a Grafana admin password
kubectl create namespace deltadatabase
kubectl -n deltadatabase create secret generic delta-master-key \
  --from-literal=master-key="$(openssl rand -hex 32)"

# Optional: override the default Grafana admin password before applying
kubectl -n deltadatabase create secret generic grafana-admin \
  --from-literal=admin-password="$(openssl rand -hex 16)"

# 2. Apply everything with one command
kubectl apply -k deploy/kubernetes/kustomize

# 3. Wait for rollout
kubectl -n deltadatabase rollout status deployment/main-worker
kubectl -n deltadatabase rollout status deployment/proc-worker
kubectl -n deltadatabase rollout status deployment/prometheus
kubectl -n deltadatabase rollout status deployment/grafana
```

!!! warning
    If you skip creating the `grafana-admin` secret, a default password of `admin` is used.
    Always set a strong password before exposing Grafana to any network.

### Access

```bash
# REST API
kubectl -n deltadatabase port-forward svc/main-worker 8080:8080
# open http://localhost:8080

# Grafana dashboard (login: admin / admin)
kubectl -n deltadatabase port-forward svc/grafana 3000:3000
# open http://localhost:3000

# Prometheus UI
kubectl -n deltadatabase port-forward svc/prometheus 9090:9090
# open http://localhost:9090
```

### Kustomize Architecture

```
Internet / Ingress
       │
       ▼  REST :8080
┌──────────────────┐     metrics :9090
│   main-worker    │ ──────────────────────────────┐
│   ClusterIP svc  │                               │
└────────┬─────────┘                               │
         │  gRPC :50051                            │
    ┌────┴──────────────────────────────┐          │
    │                                   │          ▼
 proc-worker-1  proc-worker-2  …  (HPA: 1–10)  ┌──────────┐
    │  metrics :9091            │              │ Prometheus│
    └──────────────────────────-┘ ←────────── │  :9090   │
          /shared/db  (RWX PVC)               └────┬─────┘
                                                   │
                                              ┌────▼─────┐
                                              │  Grafana  │
                                              │  :3000    │
                                              └───────────┘
```

### Customising the Overlay

| Resource | File | Common change |
|---|---|---|
| Storage class | `kustomize/shared-pvc.yaml` | Set `storageClassName` |
| Image tag | `kustomize/main-worker.yaml` / `kustomize/proc-worker.yaml` | Pin to a release tag |
| HPA limits | `kustomize/proc-worker-hpa.yaml` | Adjust `minReplicas`, `maxReplicas`, `averageUtilization` |
| Grafana password | `kustomize/prometheus-grafana.yaml` | Change `GF_SECURITY_ADMIN_PASSWORD` env var |

---

## S3-Compatible Storage

Replace the shared POSIX filesystem with any S3-compatible object store. No ReadWriteMany PVC needed.

**Supported services:** MinIO · RustFS · SeaweedFS · AWS S3 · Ceph RadosGW

### Quick Start with MinIO

```bash
docker compose -f deploy/docker-compose/docker-compose.with-s3.yml up
```

This starts MinIO, the Main Worker, and 3 Processing Workers all configured to use the S3 backend.

Open the MinIO console at **http://localhost:9001** (user: `minioadmin`, password: `minioadmin`).

### Manual S3 Configuration

```bash
# Processing Worker with MinIO
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -s3-endpoint=minio:9000 \
  -s3-bucket=deltadatabase \
  -s3-use-ssl=false \
  -s3-access-key=minioadmin \
  -s3-secret-key=minioadmin

# Processing Worker with AWS S3
export S3_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE
export S3_SECRET_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY

./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -s3-endpoint=s3.amazonaws.com \
  -s3-use-ssl=true \
  -s3-region=us-east-1 \
  -s3-bucket=my-deltadatabase-bucket
```

### S3 Object Layout

```
deltadatabase/
├── files/<entityID>.json.enc    — AES-256-GCM encrypted blob
├── files/<entityID>.meta.json   — metadata (key ID, IV, tag, schema, version)
└── templates/<schemaID>.json    — JSON Schema templates
```

---

## Supply a Persistent Master Key

By default, the Main Worker generates a **new random key** on each startup. Entities encrypted with the old key will be unreadable after a restart.

To persist data across restarts, generate a key once and supply it on every start:

```bash
# Generate once and save
MASTER_KEY=$(openssl rand -hex 32)
echo "MASTER_KEY=${MASTER_KEY}" >> .env

# Docker Compose picks up .env automatically
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up
```

!!! warning
    Store the master key securely. If the key is lost, all stored data becomes permanently unrecoverable.

---

## Supply an Admin Key

The admin key is the master Bearer credential for the Management UI and REST API. Without it, any caller can issue session tokens (dev mode only — not suitable for production).

```bash
# Generate once and save
ADMIN_KEY=$(openssl rand -hex 24)
echo "ADMIN_KEY=${ADMIN_KEY}" >> .env

# Docker Compose picks up .env automatically
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up
```

Use the admin key to log in to the Management UI at **http://localhost:8080/** or as a Bearer token in API calls:

```bash
curl -s http://localhost:8080/admin/workers \
  -H "Authorization: Bearer ${ADMIN_KEY}"
```

!!! warning
    Set a strong, randomly-generated admin key before exposing DeltaDatabase to any network. Store it in a secrets manager — never commit it to source control.
    