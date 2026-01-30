# Project Guidelines: DeltaDatabase

This document outlines the coding standards, project structure, and implementation guidelines for the DeltaDatabase project.

## 1. Documentation Standards
- **In-code Documentation**: All public functions, structs, and interfaces must have Go-standard doc comments.
- **Architecture Updates**: Any changes to the system design must be reflected in [Agents.md](Agents.md).
- **READMEs**: Every major directory in `pkg/` and `internal/` should contain a `README.md` explaining its purpose and local dependencies.
- **API Specs**: gRPC services should be documented within `.proto` files. REST endpoints should ideally have a basic Swagger/OpenAPI definition if complexity grows.

## 2. Project Structure
The project follows a standard Go layout:

```text
/
├── cmd/
│   ├── main-worker/        # Entry point for the Main Worker
│   └── proc-worker/        # Entry point for the Processing Worker
├── pkg/
│   ├── crypto/             # AES-GCM and Key management logic
│   ├── cache/              # LRU + TTL cache implementation
│   ├── fs/                 # Shared filesystem interaction & locking
│   └── schema/             # JSON Schema validation logic
├── internal/
│   ├── auth/               # Internal auth/token logic
│   └── routing/            # Main worker routing logic
├── api/
│   ├── proto/              # Protobuf definitions
│   └── rest/               # REST handler implementations
├── shared/                 # Local simulation of the shared filesystem
│   ├── db/
│   │   ├── files/          # Encrypted blobs
│   │   └── templates/      # JSON Schema templates
├── Agents.md               # Core design document
└── Guideline.md            # This document
```

## 3. Preferred Libraries
To maintain consistency and performance, use the following libraries:

| Category | Library | Purpose |
| :--- | :--- | :--- |
| **Communication** | `google.golang.org/grpc` | Primary worker-to-worker RPC |
| **REST API** | `github.com/gin-gonic/gin` | Lightweight and fast web framework |
| **JSON Schema** | `github.com/xeipuuv/gojsonschema` | Draft-07 validation |
| **Caching** | `github.com/hashicorp/golang-lru` | Performance-optimized LRU cache |
| **Encryption** | `crypto/aes`, `crypto/cipher` | Standard library AES-GCM |
| **Concurrency** | `golang.org/x/sync/errgroup` | Managing worker routines |
| **Testing** | `github.com/stretchr/testify` | Assertions and mock objects |

## 4. Implementation Workflow
1. **API First**: Define all gRPC services in `api/proto/` and generate Go code.
2. **Core Layers**: Implement the `pkg/` modules (Encryption, FS, Cache) with 100% unit test coverage.
3. **Main Worker**: Build the subscription management and routing logic (authenticated via mTLS).
4. **Processing Worker**: Implement the file processing cycle (Lock -> Read -> Decrypt -> Process -> Encrypt -> Write -> Unlock).
5. **Integration**: Use a local directory to simulate the shared FS and verify workers can communicate and share state via the disk.

## 5. Security Principles
- **No Plaintext Keys**: Encryption keys must never be stored on disk. Use volatile memory only.
- **Redaction**: Ensure logs never leak decrypted JSON or key material.
- **Fail Closed**: If encryption or validation fails, the system must reject the write/read and log a security event.
