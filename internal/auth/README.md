# internal/auth

Authentication and authorization logic for DeltaDatabase.

## Overview

The `auth` package provides authentication and token management for both Processing Workers and REST API clients. It implements:

- **Token Management**: Generate, validate, and revoke short-lived session tokens
- **Worker Authentication**: Register and authenticate Processing Workers
- **Token Types**: Separate token types for workers (gRPC) and clients (REST API)
- **Automatic Cleanup**: Background cleanup of expired tokens

## Components

### TokenManager

Manages session tokens for authenticated entities:

```go
tm := auth.NewTokenManager(
    1*time.Hour,   // Worker token TTL
    24*time.Hour,  // Client token TTL
)

// Generate worker token
workerToken, err := tm.GenerateWorkerToken("worker-1", "key-id", tags)

// Validate token
token, err := tm.ValidateWorkerToken(tokenString)

// Revoke token
err = tm.RevokeWorkerToken(tokenString)
```

### WorkerAuthenticator

Handles Processing Worker registration and credential verification:

```go
wa := auth.NewWorkerAuthenticator()

// Register worker
err := wa.RegisterWorker("worker-1", "password", tags)

// Authenticate worker
creds, err := wa.AuthenticateWorker("worker-1", "password")

// Disable/enable workers
err = wa.DisableWorker("worker-1")
err = wa.EnableWorker("worker-1")
```

## Token Lifecycle

1. **Generation**: Token created with TTL and associated metadata
2. **Validation**: Token verified on each request
3. **Expiration**: Automatic expiration after TTL
4. **Cleanup**: Background goroutine removes expired tokens every 5 minutes
5. **Revocation**: Manual revocation supported

## Security Features

- Cryptographically secure random tokens (32 bytes, base64-encoded)
- SHA-256 password hashing
- Thread-safe operations with read-write locks
- Automatic token expiration
- Worker enable/disable functionality

## Testing

Run unit tests:
```bash
go test ./internal/auth/
go test ./internal/auth/ -v
go test ./internal/auth/ -cover
```
