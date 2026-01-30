# Agents Design for JSON File Database (Go)

## Overview
A lightweight JSON-backed database implemented in Go with two worker types:
- Main Worker: handles authentication, authorization, routing, and subscription management. Supports both gRPC and REST APIs for clients.
- Processing Worker: performs JSON processing (updating, creating, getting), caches JSON in memory, encrypts JSON before persistent storage.

All workers share a common filesystem (shared mount path) and a shared encryption key (or key material managed centrally). The Main Worker vends encryption/decryption keys to Processing Workers during a subscription handshake. Communication between Main and Processing Workers uses gRPC.

## Goals
- Simple, auditable JSON datastore on disk (encrypted-at-rest).
- Separation of responsibilities: Main Worker = auth & routing; Processing Worker = process & encrypt.
- Shared FS so any Processing Worker can process any file.
- Workers cache JSON for performance; caches respect coherence rules.
- JSON shapes defined by explicit JSON Schema templates.

## Components
- Main Worker
  - Exposes subscription and client authentication endpoints via both gRPC and REST.
  - Authenticates users/clients and Processing Workers (mutual auth for workers).
  - Vends encryption key material and short-lived tokens to Processing Workers that present correct credentials.
  - Routes requests (e.g., which Processing Worker to use) and can act as a registry for available Processing Workers.

- Processing Worker
  - Subscribes to Main Worker to receive key material and subscription token.
  - Processes JSON files (validate, transform, enrich, query) and returns processed JSON to callers or writes encrypted JSON to disk.
  - Encrypts JSON when writing to shared filesystem; decrypts when reading using the shared key provided by Main Worker.
  - Maintains an in-memory cache (LRU + TTL) of decrypted JSON to speed repeated processing.

- Shared Filesystem
  - A single mount path visible to all workers (e.g., NFS, SMB, or a cloud shared volume).
  - Files store encrypted JSON blobs + metadata (version, schema id, HMAC/tag).

## Filesystem Layout (example)
- /shared/db/
  - files/
    - <file-id>.json.enc    # encrypted blob
    - <file-id>.meta.json   # metadata (schema id, version, timestamps, writer id)
  - templates/
    - <schema-id>.json      # JSON Schema templates

## Authentication & Subscription Flow (Workers)
1. Processing Worker registers with Main Worker via mutually authenticated channel (mTLS or pre-shared credentials).
2. Main Worker validates identity and issues:
   - A short-lived worker token for RPC calls.
   - The encryption key (or key-wrapping material) encrypted to the Processing Worker's public key.
3. Processing Worker stores the decrypted key in volatile memory only (never persist plaintext key to disk).
4. Processing Worker uses token for service registration; Main Worker lists it as available.

Security note: require mTLS or equivalent for the subscription step. Consider Hardware Security Module (HSM) or OS keyring for key handling.

## Key Management
- Master Key (KM): stored and managed by Main Worker or a central key manager (external KMS support required). Main Worker can derive file keys using KDFs or issue wrapped keys.
- Key distribution: use public-key encryption to deliver per-worker wrapped keys (Main Worker encrypts key material with worker's public key).
- Key rotation: Main Worker supports key rotation; when rotating, it generates new file keys and re-encrypts active files lazily on next write or via a background rewrap job.
- Key storage on Processing Worker: in-memory only, cleared on shutdown.
- Use a single shared key for all files.

## Encryption & Integrity
- Use AES-GCM (recommended) for authenticated encryption.
- Every encrypted file should include a versioned header: {key-id, alg: AES-GCM, iv, tag, schema-id, writer-id} in associated metadata.
- Use file-level HMAC or AEAD tag for tamper detection.

## APIs & Message Formats
Main Worker supports both HTTP+JSON (REST) and gRPC for client interactions. Communication between Main and Processing Workers uses gRPC. Examples below use HTTP JSON payloads for REST and protobuf for gRPC.

- Processing Worker Subscribe (gRPC)
  - Request: Subscribe(worker_id, pubkey)
  - Response: SubscribeResponse(token, wrapped_key, key_id)

- Main Worker Client Request to Get Entity (REST example)
  - Request: GET /entity/chatdb?key=Chat_id
    - headers: Authorization: Bearer <client-token>
  - Main Worker authenticates client and forwards to a Processing Worker, which retrieves and returns the entity data.

- Main Worker Client Request to Update/Create Entity (REST example)
  - Request: PUT /entity/chatdb
    - headers: Authorization: Bearer <client-token>
    - body: {"Chat_id": {"chat":[{"type":"assistant", "text":"HERE TEXT"}]}}
  - Main Worker authenticates client and forwards to a Processing Worker for validation, encryption, and storage.

- Processing Worker Process API (internal gRPC)
  - Request: Process(database_name, entity_key, schema_id, operation, params)
  - Response: ProcessResponse(status, result)

## JSON Templates / Schema
- Use JSON Schema (draft-07 or later) to define allowed shapes.
- Templates stored under `/shared/db/templates/<schema-id>.json`.
- Metadata in `<file-id>.meta.json` must include `schema_id` and `schema_version`.
- Processing Workers MUST validate JSON against the declared schema before accepting writes.

Sample schema snippet (stored as template):
{
  "$id": "user.v1",
  "type": "object",
  "properties": {
    "id": {"type":"string"},
    "name": {"type":"string"},
    "email": {"type":"string","format":"email"}
  },
  "required": ["id","email"]
}

## Database Perspective and Example
The system acts as a JSON-based database where clients query for data entities within named databases (e.g., "chatdb") rather than individual files. For example, a chat frontend might store data as:

Database: chatdb
Entity: Chat_id: {"chat":[{"type":"assistant", "text":"HERE TEXT"}]}

Clients query the Main Worker with requests like "Give me Chat_id from chatdb". The Main Worker authenticates the client, then authorizes and routes the request to a Processing Worker. The Processing Worker checks its in-memory cache for the data; if not present, it loads the encrypted file from disk, decrypts it, and caches it. If memory is full, the least recently used (LRU) entry is deallocated. Similar flows apply for updating or creating new data entities.

## Caching Algorithm
- Cache type: in-memory LRU cache with TTL per entry (no persistence across restarts; cache is volatile and cleared on shutdown).
- Cache entry structure: {entity_key, database_name, schema_id, version, decrypted_json, last_updated, expiry}
- Coherence rules:
  - Writers update cache when they modify an entity.
  - When a Processing Worker reads an entity from disk, it checks the entity's `meta.version` against the cached version; if mismatch, reload from disk.
  - Optionally implement a lightweight pub/sub (via Main Worker) to notify workers of invalidations.
- Eviction policy: LRU with a configurable TTL; when memory is full, the least recently used entry is deallocated. The cache tracks last access/change time internally but does not include it in transported data.

## Concurrency & File Locking
- To avoid races: use an advisory lock scheme on the shared FS (flock-style, preferred locking/coordination mechanism: FS Locks) or a coordination service (e.g., etcd or Main Worker as lock coordinator).
- Writers obtain an exclusive lock for the file before decrypting -> modify -> encrypt -> write.
- Readers obtain a shared/read lock when reading concurrently (if FS supports it), otherwise read-verify with file version checks.

## Operational Considerations
- Logging: redact any secret material; do not log plaintext keys or decrypted JSON unless explicitly allowed.
- Backup/Recovery: back up encrypted blobs and associated metadata. Key material backup must be separate and secure.
- Monitoring: track cache hit ratio, subscription failures, encryption/decryption failure rates, key rotation events.

## Testing & Local Development
- Local setup can use a local directory as shared FS and TLS between workers simulated with self-signed certs.
- Provide a small test harness in Go to simulate subscription, process, and cache behavior.

## Example Sequence: Client Query for Data Entity
1. Client queries Main Worker for data entity (e.g., "Give me Chat_id from chatdb").
2. Main Worker authenticates the client and authorizes the request.
3. Main Worker routes the request to an available Processing Worker.
4. Processing Worker checks its in-memory cache for the data; if not present, loads the encrypted file from disk, decrypts it, and caches it. If memory is full, the least recently used entry is deallocated.
5. Processing Worker returns the decrypted JSON to Main Worker, which forwards it to the client.
6. For updating or creating new data entities, the flow is similar: client sends update/create request to Main Worker, which routes to Processing Worker for validation, encryption, and storage, updating the cache accordingly.
