---
layout: default
title: Usage
nav_order: 3
has_children: true
---

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
| [Caching Model](caching) | How the LRU cache works |
| [Benchmark Results](benchmarks) | Measured performance numbers |
| [Examples](examples/) | Real-world usage examples |

## Concepts

### Databases and Entities

DeltaDatabase organizes data into **databases** and **entities**:

- A **database** is a named collection (e.g., `chatdb`, `userdb`).
- An **entity** is a named JSON document within a database (e.g., `session_001`).

Think of it as: `database` = table, `entity key` = primary key, `entity value` = JSON row.

```
chatdb/
├── session_001  →  {"messages": [...]}
├── session_002  →  {"messages": [...]}
└── session_003  →  {"messages": [...]}
```

### The REST API

All entity operations use two endpoints:

```
PUT  /entity/{database}          — create or update one or more entities
GET  /entity/{database}?key=...  — retrieve a single entity
```

Both require an `Authorization: Bearer <token>` header.

### JSON Schema Validation

Before any entity is stored, it is validated against the JSON Schema registered for that database. Invalid data is rejected with an HTTP `400` response.

Schemas are stored in `{shared-fs}/templates/` or can be managed via the REST API or the web UI.
