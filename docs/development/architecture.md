
# Architecture

DeltaDatabase is built around a **two-worker model**: a single **Main Worker** handles authentication and routing, while one or more **Processing Workers** handle the actual data operations (encryption, decryption, caching, storage).

---

## High-Level Diagram

```
 ┌──────────────────────────────────────────────────────────────────┐
 │  Client (your application, browser, curl …)                      │
 └───────────────────────────────┬──────────────────────────────────┘
                                 │  REST (HTTP/JSON)  or  gRPC
                                 ▼
 ┌──────────────────────────────────────────────────────────────────┐
 │  Main Worker  (:8080 REST  |  :50051 gRPC)                       │
 │  • Issues client Bearer tokens   (POST /api/login)               │
 │  • Authenticates every request                                   │
 │  • Distributes master encryption key to Processing Workers       │
 │  • Routes entity requests to an available Processing Worker      │
 │  • Exposes the web management UI at /                            │
 └──────────────────────────────┬───────────────────────────────────┘
                                │  gRPC (internal)
                ┌───────────────┼───────────────┐
                ▼               ▼               ▼
        ┌────────────┐  ┌────────────┐  ┌────────────┐
        │ Proc Worker│  │ Proc Worker│  │ Proc Worker│
        │  :50052    │  │  :50053    │  │  :50054    │
        └─────┬──────┘  └─────┬──────┘  └─────┬──────┘
              └───────────────┴───────────────┘
                              │
             ┌────────────────┴────────────────┐
             │                                 │
    ┌────────┴────────┐               ┌────────┴────────┐
    │  Shared FS      │  ── or ──     │  S3-compatible  │
    │  /shared/db/    │               │  (MinIO, AWS S3,│
    │  ├── files/     │               │   RustFS, …)    │
    │  └── templates/ │               └─────────────────┘
    └─────────────────┘
```

---

## Main Worker

The Main Worker is the **single entry point** for all external clients. It never touches the data directly — it authenticates requests and delegates to Processing Workers.

### Responsibilities

| Responsibility | Details |
|---------------|---------|
| Client authentication | Issues Bearer tokens via `POST /api/login` |
| Worker authentication | Validates Processing Workers during the subscribe handshake (mTLS-ready) |
| Key distribution | Wraps the AES master key with each worker's RSA public key and vends it during subscription |
| Request routing | Forwards `GET /entity/…` and `PUT /entity/…` to an available Processing Worker |
| Worker registry | Maintains the list of active workers; removes workers that stop heartbeating |
| Web UI | Serves the embedded single-page management application at `/` |
| Schema storage | Stores JSON Schema templates in shared storage so all workers can access them |

### Routing Strategy

The Main Worker routes entity requests using a **cache-aware + least-loaded** algorithm:

1. Try to send to the worker that **most recently served the same entity** (to maximize LRU cache hits).
2. Fall back to the worker with the **fewest active requests** when no preferred worker is available.

---

## Processing Worker

Processing Workers are the **data plane**. They subscribe to the Main Worker at startup to receive the encryption key, then handle all read and write operations.

### Responsibilities

| Responsibility | Details |
|---------------|---------|
| Subscribe | Connect to Main Worker, provide RSA public key, receive wrapped AES key |
| Schema validation | Validate incoming JSON against the registered JSON Schema before writing |
| Encryption | Encrypt entities with AES-256-GCM before writing to storage |
| Decryption | Decrypt entities after reading from storage |
| Caching | Maintain an LRU in-memory cache of decrypted entities |
| File locking | Acquire advisory locks (`flock`) on shared-FS writes to prevent concurrent corruption |
| S3 locking | Use in-process mutexes for S3 writes (no shared-FS lock needed) |

### Worker Lifecycle

```
Startup
  │
  ├─ Generate RSA key pair (ephemeral)
  │
  ├─ Connect to Main Worker (gRPC)
  │
  ├─ Send Subscribe(worker_id, rsa_public_key)
  │
  ├─ Receive SubscribeResponse(token, wrapped_aes_key, key_id)
  │
  ├─ Unwrap AES key with RSA private key → store in volatile memory
  │
  ├─ Register as "Available"
  │
  └─ Start serving Process RPCs (GET / PUT)
         │
         ├─ GET: check cache → decrypt from storage if miss → return
         │
         └─ PUT: validate schema → encrypt → write atomically → update cache
```

---

## Storage Backends

### Shared Filesystem (default)

Any POSIX-compatible directory: local disk, NFS, CIFS/Samba, or a cloud-mounted volume.

- **File layout:**
  ```
  /shared/db/
  ├── files/
  │   ├── <entityID>.json.enc    # AES-256-GCM encrypted blob
  │   └── <entityID>.meta.json   # metadata (key_id, iv, tag, schema_id, version)
  └── templates/
      └── <schemaID>.json        # JSON Schema template
  ```
- **Write durability:** `fdatasync` before atomic rename — no data loss on crash.
- **Locking:** POSIX advisory `flock` per file prevents concurrent writers from corrupting entities.

### S3-Compatible (optional)

Any service implementing the S3 API: MinIO, RustFS, SeaweedFS, AWS S3, Ceph RadosGW.

- **Object layout:**
  ```
  deltadatabase/
  ├── files/<entityID>.json.enc
  ├── files/<entityID>.meta.json
  └── templates/<schemaID>.json
  ```
- **Locking:** In-process mutexes; S3's strong read-after-write consistency prevents races.
- **Advantage:** No shared PVC needed in Kubernetes.

---

## Key Management

```
Main Worker
  │
  ├─ Generates (or restores from -master-key flag) a 32-byte AES master key
  │   at startup — NEVER writes it to disk
  │
  └─ On each worker Subscribe:
       │
       ├─ Validates worker credentials
       │
       ├─ Encrypts master key with worker's RSA public key (RSA-OAEP)
       │
       └─ Sends wrapped key → worker unwraps → stores in RAM only
```

**Key rotation** is supported: generate a new key, restart the Main Worker with `-master-key=<new>`, and Processing Workers will receive the new key on their next subscription. Active files will be re-encrypted lazily on next write.

---

## Authentication Flow (Clients)

```
Client                         Main Worker
  │                                │
  ├──── POST /api/login ──────────►│
  │     {"client_id": "myapp"}     │
  │                                ├─ Generates Bearer token (JWT-style, signed)
  │◄─── {"token": "…"} ───────────┤
  │                                │
  ├──── GET /entity/db?key=foo ───►│
  │     Authorization: Bearer …    │
  │                                ├─ Validates token
  │                                ├─ Routes to Processing Worker
  │◄─── {"field": "value"} ───────┤
```

---

## Caching Architecture

Each Processing Worker maintains its own **LRU cache** of decrypted entities:

```
Processing Worker (in-memory)
┌──────────────────────────────────┐
│  LRU Cache (configurable size)   │
│                                  │
│  key: "chatdb/session_001"       │
│  value: {"messages": [...]}       │
│  version: 3                      │
│                                  │
│  key: "userdb/user_42"           │
│  value: {"name": "Alice", ...}   │
│  version: 1                      │
│                                  │
│  … (up to -cache-size entries)   │
└──────────────────────────────────┘
```

**Cache coherence:** When reading from disk, the worker checks `meta.version` against the cached version. If they differ, the disk copy wins and the cache is refreshed.

See the [Caching Model](../usage/caching) page for the full read/write path description.
