# Examples

This folder contains ready-to-use deployment examples for DeltaDatabase.  
Pick the topology that fits your use case:

| # | File | Description |
|---|------|-------------|
| 1 | [01-all-in-one.md](01-all-in-one.md) | Main Worker **and** Processing Worker inside a **single Docker container** — ideal for development, CI, or edge nodes. |
| 2 | [02-one-main-multiple-workers.md](02-one-main-multiple-workers.md) | **1 Main Worker + N Processing Workers** via Docker Compose — horizontal scale-out for higher throughput. |
| 3 | [03-one-main-one-worker.md](03-one-main-one-worker.md) | **1 Main Worker + 1 Processing Worker** as separate containers — the simplest production-like setup. |
| 4 | [04-kubernetes-autoscaling.md](04-kubernetes-autoscaling.md) | **1 Main Worker + autoscaling Processing Workers** on Kubernetes, starting at 1 replica and scaling with HPA. |
| 5 | [05-s3-compatible-storage.md](05-s3-compatible-storage.md) | Processing Workers backed by an **S3-compatible object store** (RustFS, SeaweedFS, MinIO, AWS S3) instead of a shared filesystem. |

## Where to find the deployment files

```
deploy/
├── docker/                              # Dockerfiles
│   ├── Dockerfile.main-worker           # Main Worker image
│   ├── Dockerfile.proc-worker           # Processing Worker image
│   ├── Dockerfile.all-in-one            # Both workers in one image
│   └── entrypoint-all-in-one.sh         # Startup script for all-in-one image
├── docker-compose/                      # Docker Compose configurations
│   ├── docker-compose.all-in-one.yml
│   ├── docker-compose.one-main-one-worker.yml
│   ├── docker-compose.one-main-multiple-workers.yml
│   └── docker-compose.s3.yml            # S3-compatible object storage backend
└── kubernetes/                          # Kubernetes manifests
    ├── shared-pvc.yaml                  # ReadWriteMany PVC (local FS topology)
    ├── main-worker.yaml                 # Main Worker Deployment + Service
    ├── proc-worker.yaml                 # Processing Worker Deployment (local FS)
    ├── proc-worker-s3.yaml              # Processing Worker Deployment (S3 backend)
    ├── proc-worker-hpa.yaml             # HorizontalPodAutoscaler
    └── s3-secret.yaml                   # Kubernetes Secret for S3 credentials
```
