# Task 2: Cryptographic Foundation

## Objective
Implement the core encryption and key management logic used for persisting data at rest.

## Requirements
- Implement `pkg/crypto/aes_gcm.go`:
  - `Encrypt(key, plaintext) -> (ciphertext, nonce, tag)`
  - `Decrypt(key, ciphertext, nonce, tag) -> plaintext`
- Implement `pkg/crypto/key_wrap.go`:
  - Logic to wrap/unwrap keys using RSA or another public-key mechanism for the handshake.
- Ensure all cryptographic operations use the standard `crypto/` library.

## Dependencies
- Builds on: [Task 1](task_1_agent.md).
- Validated by: `tests/test_task_2.py` (Unit tests for crypto).

## Deliverables
- `pkg/crypto/` source files.
- Unit tests in `pkg/crypto/*_test.go`.
