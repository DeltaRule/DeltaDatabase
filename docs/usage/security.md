
# Security Model

DeltaDatabase is designed with **security-first** principles. This page describes every security mechanism in the system.

---

## Security Properties at a Glance

| Property | Implementation |
|----------|---------------|
| Encryption at rest | AES-256-GCM per entity; nonce and AEAD tag stored in `.meta.json` |
| Key distribution | RSA-OAEP wrap/unwrap; master key never leaves the Main Worker in plaintext |
| Keys in memory only | Processing Workers clear keys on shutdown; keys are never written to disk |
| Tamper detection | AEAD tag checked on every decryption; reads fail closed on mismatch |
| Schema validation | JSON Schema draft-07 enforced before every write |
| Log redaction | No plaintext entity data or key material is emitted in logs |
| Token expiry | Worker tokens: 1 h (configurable). Client tokens: 24 h (configurable) |
| Path traversal protection | Entity keys, database names, and schema IDs are validated to reject `/`, `\`, and `..` |
| Request body limit | REST PUT/schema endpoints reject bodies larger than 1 MiB |
| Write durability (FS) | `fdatasync` before atomic rename guarantees no data loss on worker crash |
| S3 credentials | Pass via `S3_ACCESS_KEY` / `S3_SECRET_KEY` env vars, not CLI flags |

---

## Encryption Details

### Algorithm

**AES-256-GCM** (Authenticated Encryption with Associated Data):

- 256-bit key derived from the master key.
- Random 12-byte nonce per entity write.
- 128-bit AEAD authentication tag.

### On-disk Format

For each entity, two files are written:

```
files/<entityID>.json.enc
  └─ raw AES-256-GCM ciphertext

files/<entityID>.meta.json
  └─ {
       "key_id":    "main-key-v1",
       "alg":       "AES-GCM",
       "iv":        "<base64-nonce>",
       "tag":       "<base64-AEAD-tag>",
       "schema_id": "chat.v1",
       "version":   3,
       "writer_id": "proc-1"
     }
```

The AEAD tag is checked on every read. If the tag does not match (e.g., due to file corruption or tampering), the read fails with an error and a security event is logged.

### Key Derivation and Distribution

```
Main Worker
  │
  ├─ Holds master AES key in volatile RAM (never on disk)
  │
  └─ On Processing Worker Subscribe:
       │
       ├─ Worker sends its ephemeral RSA public key
       │
       ├─ Main Worker encrypts master key with RSA-OAEP
       │
       └─ Worker decrypts with its RSA private key
            → stores plaintext key in volatile RAM only
            → RSA private key discarded after use
```

---

## Key Management

### Key Rotation

1. Generate a new 32-byte AES key: `openssl rand -hex 32`.
2. Restart the Main Worker with `-master-key=<new_key>`.
3. Processing Workers receive the new key on their next subscription.
4. New writes use the new key. Old entities remain encrypted under the old key.
5. Old entities are re-encrypted lazily on the next write, or via a background rewrap job.

!!! warning
    Keep a secure backup of all master keys ever used. Entities encrypted under an old key require the old key to be readable.
    
### Key Storage Best Practices

- Never commit the master key to source control.
- Never pass the master key as a CLI flag in production (it appears in `ps` output and shell history).
- Use a secrets manager (HashiCorp Vault, AWS Secrets Manager, Kubernetes Secret) to inject the key as an environment variable.
- Use HSM or OS keyring integration for hardware-backed key storage in high-security environments.

---

## Authentication Security

### Client Tokens

- Tokens are signed server-side and validated on every request.
- Token TTL is configurable via `-client-ttl` (default: 24 h).
- Tokens are not refreshable; clients must re-login.

### Worker Tokens

- Processing Workers receive a short-lived session token (default: 1 h) during subscription.
- The token is used for all subsequent gRPC calls to the Main Worker.
- Workers must re-subscribe after token expiry.

---

## Input Validation

### Path Traversal Protection

Entity keys, database names, and schema IDs are validated to reject:
- `/` (path separator)
- `\` (Windows path separator)
- `..` (parent directory traversal)

### Request Size Limits

- PUT entity and PUT schema endpoints reject request bodies larger than **1 MiB**.

### Schema Validation

Every `PUT /entity/{database}` request is validated against the registered JSON Schema before being encrypted and stored. Validation failures return HTTP `400` and nothing is written to disk.

`DELETE /entity/{database}` requests bypass schema validation (no payload is involved) and require `write` permission.

---

## Log Redaction

Logs never contain:
- Decrypted entity content.
- Encryption keys or key material.
- Bearer tokens or worker session tokens.

All security events (tamper detection, auth failures, key rotation) are logged without exposing sensitive data.

---

## Network Security Recommendations

For production deployments:

1. **TLS everywhere** — put the Main Worker behind a TLS-terminating reverse proxy (nginx, Traefik, AWS ALB).
2. **Restrict gRPC port** — the internal gRPC port (`:50051`) should not be exposed to external networks. Use network policies in Kubernetes or firewall rules in bare-metal deployments.
3. **Network policies** — in Kubernetes, use `NetworkPolicy` to ensure only Processing Workers can reach the Main Worker's gRPC port.
4. **Token TTL** — shorten `-client-ttl` for sensitive applications (e.g., `1h`).

---

## Reporting Security Issues

If you discover a security vulnerability, please report it privately by opening a GitHub Security Advisory at:

**https://github.com/DeltaRule/DeltaDatabase/security/advisories/new**

Do not open a public issue for security vulnerabilities.
