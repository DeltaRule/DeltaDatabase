# Examples

This folder contains ready-to-use deployment examples for DeltaDatabase.  
Pick the topology that fits your use case:

| # | File | Description |
|---|------|-------------|
| 1 | [01-all-in-one.md](01-all-in-one.md) | Main Worker **and** Processing Worker inside a **single Docker container** — ideal for development, CI, or edge nodes. |
| 2 | [02-one-main-multiple-workers.md](02-one-main-multiple-workers.md) | **1 Main Worker + N Processing Workers** via Docker Compose — horizontal scale-out for higher throughput. |
| 3 | [03-one-main-one-worker.md](03-one-main-one-worker.md) | **1 Main Worker + 1 Processing Worker** as separate containers — the simplest production-like setup. |
| 4 | [04-kubernetes-autoscaling.md](04-kubernetes-autoscaling.md) | **1 Main Worker + autoscaling Processing Workers** on Kubernetes, starting at 1 replica and scaling with HPA. |

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
│   └── docker-compose.one-main-multiple-workers.yml
└── kubernetes/                          # Kubernetes manifests
    ├── shared-pvc.yaml                  # ReadWriteMany PVC
    ├── main-worker.yaml                 # Main Worker Deployment + Service
    ├── proc-worker.yaml                 # Processing Worker Deployment + headless Service
    └── proc-worker-hpa.yaml             # HorizontalPodAutoscaler
```
