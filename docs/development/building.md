---
layout: default
title: Building from Source
parent: Development
nav_order: 3
---

# Building from Source

{: .note }
> For most use-cases, the [Docker / Kubernetes deployment](../usage/deployment) is the recommended way to run DeltaDatabase. Build from source only when you need to develop, modify, or test the code locally.

---

## Prerequisites

| Tool | Minimum version | Notes |
|------|-----------------|-------|
| Go | 1.25 | Required for both workers |
| Git | any | |
| Python | 3.9+ | Integration tests only |

No external databases, message brokers, or container runtimes are required for local development.

---

## Clone and Build

```bash
git clone https://github.com/DeltaRule/DeltaDatabase.git
cd DeltaDatabase

# Build the Main Worker
go build -o bin/main-worker ./cmd/main-worker/

# Build the Processing Worker
go build -o bin/proc-worker ./cmd/proc-worker/

# Verify both binaries
./bin/main-worker --help
./bin/proc-worker --help
```

Both binaries are self-contained — no dynamic libraries or external dependencies are needed at runtime.

---

## Running Locally

### 1. Create the shared filesystem directory

```bash
mkdir -p ./shared/db/files ./shared/db/templates
```

### 2. Start the Main Worker

```bash
./bin/main-worker \
  -grpc-addr=127.0.0.1:50051 \
  -rest-addr=127.0.0.1:8080 \
  -shared-fs=./shared/db
```

The first line of output shows the generated master key — copy it for subsequent restarts:

```
2026/02/24 12:00:00 Generated new master encryption key
2026/02/24 12:00:00 Key (hex): a1b2c3d4...  ← save this!
```

{: .tip }
> Pass `-master-key=<hex>` on subsequent starts to reuse the same key and keep previously stored data readable.

### 3. Start the Processing Worker

Open a second terminal:

```bash
./bin/proc-worker \
  -main-addr=127.0.0.1:50051 \
  -worker-id=proc-1 \
  -grpc-addr=127.0.0.1:50052 \
  -shared-fs=./shared/db
```

### 4. Verify with a health check

```bash
curl http://127.0.0.1:8080/health
# {"status":"ok"}
```

---

## Development Workflow

1. **API First** — Define gRPC services in `api/proto/` and regenerate Go code.
2. **Core Layers** — Implement `pkg/` modules with unit tests.
3. **Integration** — Wire the layers in `cmd/main-worker/` and `cmd/proc-worker/`.
4. **Test** — Run unit tests and Python integration tests (see [Testing](testing)).

---

## Regenerating Protobuf Code

If you modify `.proto` files in `api/proto/`, regenerate the Go code with:

```bash
# Install protoc and the Go plugin (once)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Regenerate
protoc \
  --go_out=. \
  --go-grpc_out=. \
  api/proto/*.proto
```

---

## Common Build Flags

| Flag | Purpose |
|------|---------|
| `go build -race` | Enable the race detector (recommended during development) |
| `go build -ldflags="-s -w"` | Strip debug info for smaller production binaries |
| `CGO_ENABLED=0` | Produce a fully static binary (needed for distroless Docker images) |

Example optimized build:

```bash
CGO_ENABLED=0 go build \
  -ldflags="-s -w" \
  -o bin/main-worker \
  ./cmd/main-worker/
```
