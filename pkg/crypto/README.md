# pkg/crypto

AES-GCM encryption helpers and key management utilities for DeltaDatabase.

## Overview

This package provides cryptographic primitives for the DeltaDatabase project, implementing:
- **AES-GCM encryption/decryption**: Authenticated encryption for data at rest
- **RSA key wrapping**: Secure distribution of symmetric keys between workers

## Components

### aes_gcm.go

Implements AES-GCM (Galois/Counter Mode) authenticated encryption:

- `Encrypt(key, plaintext)`: Encrypts plaintext and returns ciphertext, nonce, and authentication tag
- `Decrypt(key, ciphertext, nonce, tag)`: Decrypts ciphertext after verifying authentication tag
- `GenerateKey(keySize)`: Generates a random AES key (128, 192, or 256 bits)

**Security Features:**
- Authenticated encryption (confidentiality + integrity)
- Unique nonce per encryption operation
- Tamper detection via authentication tag
- Constant-time operations where possible

### key_wrap.go

Implements RSA-OAEP key wrapping for secure key distribution:

- `WrapKey(publicKey, keyToWrap)`: Wraps a symmetric key using RSA-OAEP
- `UnwrapKey(privateKey, wrappedKey)`: Unwraps a symmetric key using RSA-OAEP
- `GenerateRSAKeyPair(bits)`: Generates an RSA key pair (minimum 2048 bits)
- PEM encoding/decoding utilities for key serialization

**Use Case:**
The Main Worker uses `WrapKey` to securely distribute AES encryption keys to Processing Workers during the subscription handshake. Each Processing Worker uses `UnwrapKey` with its private key to obtain the shared encryption key.

## Dependencies

- Standard library only:
  - `crypto/aes`: AES cipher implementation
  - `crypto/cipher`: GCM mode
  - `crypto/rsa`: RSA public-key cryptography
  - `crypto/rand`: Cryptographically secure random number generation
  - `crypto/sha256`: SHA-256 hashing for RSA-OAEP
  - `crypto/x509`: Key encoding/decoding
  - `encoding/pem`: PEM format encoding/decoding

## Usage Example

```go
// Generate a 256-bit AES key
key, err := crypto.GenerateKey(32)
if err != nil {
    log.Fatal(err)
}

// Encrypt data
plaintext := []byte("sensitive data")
result, err := crypto.Encrypt(key, plaintext)
if err != nil {
    log.Fatal(err)
}

// Decrypt data
decrypted, err := crypto.Decrypt(key, result.Ciphertext, result.Nonce, result.Tag)
if err != nil {
    log.Fatal(err)
}

// Key wrapping example
privateKey, publicKey, err := crypto.GenerateRSAKeyPair(2048)
if err != nil {
    log.Fatal(err)
}

wrappedKey, err := crypto.WrapKey(publicKey, key)
if err != nil {
    log.Fatal(err)
}

unwrappedKey, err := crypto.UnwrapKey(privateKey, wrappedKey)
if err != nil {
    log.Fatal(err)
}
```

## Security Considerations

1. **Key Storage**: Encryption keys MUST be stored in volatile memory only. Never persist plaintext keys to disk.
2. **Nonce Uniqueness**: Each encryption operation generates a unique nonce. Never reuse nonces with the same key.
3. **Tag Verification**: Decryption fails if the authentication tag doesn't match (tamper detection).
4. **Key Size**: Use AES-256 (32-byte keys) for maximum security. For RSA, use at least 2048 bits (4096 recommended for long-term security).
5. **Error Handling**: Cryptographic failures MUST be treated as security events and logged appropriately (without leaking sensitive data).

## Testing

All functions have comprehensive unit tests with 100% coverage:
- `aes_gcm_test.go`: Tests for encryption, decryption, key generation, and error cases
- `key_wrap_test.go`: Tests for key wrapping, unwrapping, PEM encoding, and error cases

Run tests with:
```bash
go test -v ./pkg/crypto/...
```

Run benchmarks:
```bash
go test -bench=. ./pkg/crypto/...
```
