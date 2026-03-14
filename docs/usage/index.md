
# Usage

This section covers everything you need to use DeltaDatabase in your applications and infrastructure.

## In This Section

| Page | Description |
|------|-------------|
| [Quick Start](quickstart) | Get DeltaDatabase running in under 5 minutes |
| [Configuration](configuration) | All flags and environment variables |
| [API Reference](api-reference) | Complete REST API documentation |
| [Authentication](authentication) | Tokens, login, and token expiry |
| [JSON Schema Templates](schemas) | Defining and validating entity shapes |
| [Deployment](deployment) | Docker, Docker Compose, Kubernetes, S3 |
| [Security Model](security) | Encryption, key management, hardening |
| [Management UI](frontend) | Browser-based UI for managing schema-databases, schemas, and API keys |
| [Caching Model](caching) | How the LRU cache works |
| [Benchmark Results](benchmarks) | Measured performance numbers |
| [Examples](examples/) | Real-world usage examples |

## Concepts

### Databases and Entities

DeltaDatabase organizes data into **schema-databases** and **entities**:

- A **schema-database** is identified by a `schema_id` (e.g., `chat.v1`, `user.v1`). The schema IS the database.
- An **entity** is a named JSON document within a schema-database (e.g., `session_001`).

Think of it as: `schema_id` = table, `entity key` = primary key, `entity value` = JSON row.

```
chat.v1/
├── session_001  →  {"messages": [...]}
├── session_002  →  {"messages": [...]}
└── session_003  →  {"messages": [...]}
```

### The REST API

All entity operations use two endpoints:

```
PUT  /entity/{schema_id}          — create or update one or more entities
GET  /entity/{schema_id}?key=...  — retrieve a single entity
```

Both require an `Authorization: Bearer <token>` header.

### JSON Schema Validation

Before any entity is stored, it is validated against the JSON Schema registered for that schema_id (if a template exists). Invalid data is rejected with an HTTP `400` response.

Schemas are stored in `{shared-fs}/templates/` or can be managed via the REST API or the web UI.
