
# Project Structure

DeltaDatabase follows the standard Go project layout.

```
.
├── cmd/
│   ├── main-worker/            # Main Worker entry point & server
│   │   ├── main.go             # Flag parsing and startup
│   │   ├── server.go           # gRPC + REST handler
│   │   ├── frontend.go         # Embedded web UI handler
│   │   └── static/
│   │       └── index.html      # Single-page management app (embedded)
│   └── proc-worker/            # Processing Worker entry point
│       ├── main.go             # Flag parsing and startup
│       ├── worker.go           # Subscription & key management
│       └── server.go           # gRPC Process handler (GET/PUT)
├── pkg/
│   ├── crypto/                 # AES-GCM encryption + RSA key wrapping
│   ├── cache/                  # LRU in-memory cache
│   ├── fs/                     # Shared filesystem storage + file locking
│   ├── metrics/                # Operational metrics
│   └── schema/                 # JSON Schema draft-07 validation
├── internal/
│   ├── auth/                   # Token manager + worker authenticator
│   └── routing/                # Worker registry + routing logic
├── api/
│   └── proto/                  # Protobuf definitions + generated Go code
├── shared/
│   └── db/
│       ├── files/              # Encrypted entity blobs (runtime data)
│       └── templates/          # JSON Schema templates
├── tests/                      # Python integration & end-to-end tests
│   ├── requirements.txt
│   ├── test_authentication.py
│   ├── test_encryption.py
│   ├── test_e2e_security.py
│   ├── test_benchmarks.py
│   └── test_whole.py
├── deploy/
│   ├── docker/                 # Dockerfiles
│   │   ├── Dockerfile.main-worker
│   │   ├── Dockerfile.proc-worker
│   │   ├── Dockerfile.all-in-one
│   │   └── entrypoint-all-in-one.sh
│   ├── docker-compose/         # Docker Compose configurations
│   │   ├── docker-compose.all-in-one.yml
│   │   ├── docker-compose.one-main-one-worker.yml
│   │   ├── docker-compose.one-main-multiple-workers.yml
│   │   └── docker-compose.with-s3.yml
│   └── kubernetes/             # Kubernetes manifests
│       ├── shared-pvc.yaml
│       ├── main-worker.yaml
│       ├── proc-worker.yaml
│       ├── proc-worker-hpa.yaml
│       └── s3-config.yaml
├── examples/                   # Deployment guides (Markdown)
│   ├── 01-all-in-one.md
│   ├── 02-one-main-multiple-workers.md
│   ├── 03-one-main-one-worker.md
│   ├── 04-kubernetes-autoscaling.md
│   └── 05-s3-compatible-storage.md
├── Agents.md                   # Core system design document
├── BUILDING.md                 # Build from source & testing
├── Guideline.md                # Coding standards
├── LICENSE
└── README.md
```

---

## Package Responsibilities

### `pkg/crypto`

Provides all cryptographic primitives:

- **AES-256-GCM** encryption and decryption of entity blobs.
- **RSA-OAEP** key wrapping — used by the Main Worker to vend the master key to Processing Workers.
- Nonce generation (random 12-byte IV per entity write).

### `pkg/cache`

In-memory **LRU cache** with optional TTL support:

- Backed by `github.com/hashicorp/golang-lru`.
- Keyed by `"<database>/<entity_key>"`.
- Stores the decrypted JSON and the entity version number (for coherence checks).
- Thread-safe.

### `pkg/fs`

Shared filesystem abstraction:

- Implements the storage interface for both **POSIX filesystem** and **S3-compatible** backends.
- File-level advisory locks (`flock`) prevent concurrent writes on shared-FS.
- Atomic write path: encrypt → write temp file → `fdatasync` → rename.
- S3 backend uses `github.com/minio/minio-go` for object operations.

### `pkg/schema`

JSON Schema validation:

- Validates JSON documents against draft-07 schemas using `github.com/xeipuuv/gojsonschema`.
- Loads schemas from the configured templates directory on demand and caches them in memory.

### `pkg/metrics`

Operational observability:

- Cache hit/miss counters.
- Encryption/decryption error rates.
- Subscription and key rotation event counters.

### `internal/auth`

Token management:

- Issues and validates Bearer tokens for external clients.
- Issues and validates short-lived tokens for Processing Workers.
- Token expiry is configurable via `-client-ttl` and `-worker-ttl`.

### `internal/routing`

Worker registry:

- Maintains the set of active Processing Workers.
- Implements cache-aware + least-loaded routing.
- Removes workers that fail health checks.

### `api/proto`

Protocol Buffer definitions:

- `Subscribe` RPC — used by Processing Workers to register with the Main Worker.
- `Process` RPC — used by the Main Worker to forward GET/PUT operations to workers.
- Generated Go code is committed alongside the `.proto` files.

---

## Adding a New Package

Follow these steps when adding a new `pkg/` module:

1. Create `pkg/<name>/` with a `doc.go` comment explaining the package purpose.
2. Add a `README.md` in the directory explaining purpose, usage, and dependencies.
3. Write unit tests with 100% coverage using `github.com/stretchr/testify`.
4. Update `Agents.md` if the new module changes the system architecture.
