# Example 4 — 1 Main Worker + Autoscaling Processing Workers (Kubernetes)

The **Main Worker** runs as a single-replica Deployment.  **Processing Workers**
start at 1 replica and are automatically scaled up (to a maximum of 10) by a
`HorizontalPodAutoscaler` based on CPU utilisation.

**Use this when:** you need elastic, cloud-native scaling and want Kubernetes to
manage worker lifecycle, health checks, and rolling updates.

---

## Files used

| File | Purpose |
|------|---------|
| `deploy/docker/Dockerfile.main-worker` | Main Worker image (build once, push to registry) |
| `deploy/docker/Dockerfile.proc-worker` | Processing Worker image |
| `deploy/kubernetes/shared-pvc.yaml` | ReadWriteMany PersistentVolumeClaim |
| `deploy/kubernetes/main-worker.yaml` | Main Worker Deployment + ClusterIP Service |
| `deploy/kubernetes/proc-worker.yaml` | Processing Worker Deployment + headless Service |
| `deploy/kubernetes/proc-worker-hpa.yaml` | HorizontalPodAutoscaler (min 1, max 10, target CPU 60 %) |

---

## Prerequisites

1. A Kubernetes cluster (v1.26+) with the **Metrics Server** installed:

   ```bash
   kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml
   ```

2. A **ReadWriteMany** StorageClass (e.g., `nfs-client`, `azurefile`,
   `efs-sc`, or Longhorn with RWX enabled).  Update `storageClassName` in
   `deploy/kubernetes/shared-pvc.yaml` before applying.

3. A container registry that your cluster can pull images from.

---

## Build & push images

```bash
# Replace <registry> with your registry (e.g., ghcr.io/myorg, docker.io/myuser)
REGISTRY=<registry>

docker build -f deploy/docker/Dockerfile.main-worker -t ${REGISTRY}/delta-main-worker:latest .
docker build -f deploy/docker/Dockerfile.proc-worker -t ${REGISTRY}/delta-proc-worker:latest .

docker push ${REGISTRY}/delta-main-worker:latest
docker push ${REGISTRY}/delta-proc-worker:latest
```

Update the `image:` fields in `deploy/kubernetes/main-worker.yaml` and
`deploy/kubernetes/proc-worker.yaml` to use your registry.

---

## Deploy

```bash
# 1. Create the namespace
kubectl create namespace deltadatabase

# 2. Store the master encryption key as a Secret
MASTER_KEY=$(openssl rand -hex 32)
kubectl -n deltadatabase create secret generic delta-master-key \
  --from-literal=master-key="${MASTER_KEY}"

# 3. Apply manifests in order
kubectl apply -f deploy/kubernetes/shared-pvc.yaml
kubectl apply -f deploy/kubernetes/main-worker.yaml
kubectl apply -f deploy/kubernetes/proc-worker.yaml
kubectl apply -f deploy/kubernetes/proc-worker-hpa.yaml

# 4. Wait for everything to be ready
kubectl -n deltadatabase rollout status deployment/main-worker
kubectl -n deltadatabase rollout status deployment/proc-worker
```

---

## Verify

```bash
# Check pods
kubectl -n deltadatabase get pods

# Check HPA status
kubectl -n deltadatabase get hpa proc-worker-hpa

# Port-forward REST API for local testing
kubectl -n deltadatabase port-forward svc/main-worker 8080:8080 &
curl http://localhost:8080/health

# List registered Processing Workers
curl -s http://localhost:8080/admin/workers | python3 -m json.tool
```

---

## How autoscaling works

```
Load increases
      │
      ▼  CPU > 60 %
┌─────────────────────────────────────────────────────────────────┐
│  HPA evaluates metrics every 15 s (default)                     │
│  → adds up to 2 new proc-worker pods per 60 s (scaleUp policy)  │
│  → removes 1 pod per 120 s when CPU drops (scaleDown policy)    │
│  → always keeps at least 1 pod (minReplicas)                    │
│  → never exceeds 10 pods    (maxReplicas)                       │
└─────────────────────────────────────────────────────────────────┘
```

Each new pod connects to the Main Worker on startup and registers itself,
making the Main Worker aware of the new capacity automatically.

---

## Architecture

```
 Internet / Ingress
        │
        ▼  REST :8080
 ┌──────────────────┐
 │  main-worker     │  (Deployment, 1 replica)
 │  Service: ClusterIP                         │
 └────────┬─────────┘
          │  gRPC :50051 (internal)
          │  ← proc-workers subscribe here
     ┌────┴──────────────────────────────┐
     │                                   │
  proc-worker-xxx  proc-worker-yyy  …  (HPA: 1–10 replicas)
     │                   │
     └───────────────────┘
            /shared/db  (ReadWriteMany PVC)
```

> **Tip:** For production, add a `PodDisruptionBudget` to ensure at least one
> Processing Worker is always running during node maintenance:
>
> ```bash
> kubectl -n deltadatabase create poddisruptionbudget proc-worker-pdb \
>   --selector=app=proc-worker --min-available=1
> ```
