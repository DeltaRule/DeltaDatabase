
# Development

This section covers everything you need to know to develop, extend, or contribute to DeltaDatabase.

## In This Section

| Page | Description |
|------|-------------|
| [Architecture](architecture) | System design, component overview, data flows |
| [Project Structure](project-structure) | Directory layout and what lives where |
| [Building from Source](building) | Prerequisites, build steps, and binary output |
| [Testing](testing) | Unit tests, integration tests, and Python end-to-end tests |

## Development Philosophy

DeltaDatabase follows a strict separation of concerns:

- **Main Worker** = authentication, authorization, routing, key management
- **Processing Worker** = encryption, decryption, caching, file I/O
- **Shared storage** = the single source of truth (filesystem or S3)

All communication between workers uses **gRPC**. All client communication uses **REST (HTTP/JSON)** or **gRPC**.

## Preferred Libraries

| Category | Library | Purpose |
|----------|---------|---------|
| Communication | `google.golang.org/grpc` | Worker-to-worker RPC |
| REST API | `github.com/gin-gonic/gin` | Lightweight web framework |
| JSON Schema | `github.com/xeipuuv/gojsonschema` | Draft-07 validation |
| Caching | `github.com/hashicorp/golang-lru` | LRU cache |
| Encryption | `crypto/aes`, `crypto/cipher` | AES-GCM (standard library) |
| Concurrency | `golang.org/x/sync/errgroup` | Worker goroutine management |
| Testing | `github.com/stretchr/testify` | Assertions and mocks |

## Security Principles

1. **No Plaintext Keys on Disk** — Encryption keys are stored in volatile memory only and cleared on shutdown.
2. **Log Redaction** — Logs never contain decrypted entity data or key material.
3. **Fail Closed** — If encryption, decryption, or schema validation fails, the operation is rejected and a security event is logged.
4. **Schema Enforced on Every Write** — Bad data is rejected before reaching storage.
