# DeltaDatabase

> A lightweight, **encrypted-at-rest** JSON database written in Go — built for
> production-grade workloads that need per-entity encryption, JSON Schema
> validation, and a simple REST API.

[![License](https://img.shields.io/badge/license-DeltaDatabase%20v1.0-blue)](https://github.com/DeltaRule/DeltaDatabase/blob/main/LICENSE)
[![Go version](https://img.shields.io/badge/go-1.25%2B-00ADD8)](https://github.com/DeltaRule/DeltaDatabase/blob/main/go.mod)
[![Documentation](https://img.shields.io/badge/docs-readthedocs-blue)](https://deltadatabase.readthedocs.io/en/latest/)

---

## What is DeltaDatabase?

DeltaDatabase stores arbitrary **JSON documents** — called *entities* — inside *schema-databases* — the schema IS the database. Every entity is:

- **Validated** against a JSON Schema template before being persisted.
- **Encrypted** at rest using AES-256-GCM before touching disk.
- **Cached** in memory using a smart LRU policy for high-speed reads.
- **Accessed** through a plain HTTP REST API or gRPC from any language.

A built-in single-page web UI is served at `/` so you can browse and manage schema-databases without any external tooling.

---

## Quick Navigation

!!! tip
    **New here?** Start with the [Quick Start guide](usage/quickstart) to have DeltaDatabase running in under 5 minutes.

| I want to… | Go to… |
|------------|--------|
| Get DeltaDatabase running quickly | [Quick Start](usage/quickstart) |
| Understand the system architecture | [Architecture](development/architecture) |
| See the full REST API | [API Reference](usage/api-reference) |
| Deploy with Docker or Kubernetes | [Deployment](usage/deployment) |
| See real-world usage examples | [Examples](usage/examples/) |
| Understand the security model | [Security](usage/security) |
| Use the web management UI | [Management UI](usage/frontend) |
| Build from source | [Building](development/building) |
| Run tests | [Testing](development/testing) |

---

## Key Features

### 🔐 Encrypted at Rest
All entities are encrypted with AES-256-GCM before being written to disk. The encryption key is managed exclusively by the Main Worker and never written to disk.

### 📋 Schema Validation
Define the shape of your data using JSON Schema (draft-07). Every write is validated before encryption — bad data is rejected before it reaches storage.

### ⚡ Smart Caching
An in-memory LRU cache means frequently-accessed entities are served without disk I/O. The cache is coherent across multiple Processing Workers.

### 🔌 Dual API
Access DeltaDatabase via REST (HTTP/JSON) or gRPC. The same data is accessible through either interface.

### 🌐 Built-in Web UI
A single-page management UI is embedded in the `main-worker` binary — no additional installation required.

### 📦 Flexible Storage
Choose between a shared POSIX filesystem (NFS, local) or any S3-compatible object store (MinIO, AWS S3, RustFS, SeaweedFS).

### 🚀 Horizontally Scalable
Add more Processing Workers behind the same Main Worker to increase throughput linearly.

---

## Architecture Overview

```
 Client (app, browser, curl)
        │  REST (HTTP/JSON)  or  gRPC
        ▼
 ┌──────────────────────────────────────┐
 │  Main Worker  (:8080 REST | :50051 gRPC) │
 │  • Auth & token issuance             │
 │  • Key distribution to workers       │
 │  • Routes requests to workers        │
 │  • Serves the web management UI      │
 └──────────────────┬───────────────────┘
                    │  gRPC (internal)
         ┌──────────┼──────────┐
         ▼          ▼          ▼
   ┌──────────┐ ┌──────────┐ ┌──────────┐
   │ Proc     │ │ Proc     │ │ Proc     │
   │ Worker 1 │ │ Worker 2 │ │ Worker 3 │
   └────┬─────┘ └────┬─────┘ └────┬─────┘
        └────────────┴────────────┘
                     │
            Shared FS  or  S3
```

See the full [Architecture](development/architecture) page for details.

---

## Get Started in One Command

```bash
docker compose -f deploy/docker-compose/docker-compose.all-in-one.yml up
```

The REST API is available at **http://localhost:8080** and the web UI at **http://localhost:8080/**.

→ [Full Quick Start guide](usage/quickstart)

---

## Changelog

See [CHANGELOG](changelog) for a full history of releases and changes.
