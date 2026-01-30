# Task 5: Main Worker - Worker Management (gRPC)

## Objective
Implement the server-side logic for Processing Worker registration and key delivery.

## Requirements
- Implement the `Subscribe` gRPC handler in the Main Worker.
- Authenticate the incoming request (verify `worker_id`).
- Generate or retrieve the shared Encryption Key.
- Wrap the key with the Worker's public key (from the request).
- Return the `SubscribeResponse` with the wrapped key and a short-lived session token.

## Dependencies
- Builds on: [Task 4](task_4_agent.md).
- Validated by: `tests/test_task_5.py` (Mock Proc-Worker subscribing).

## Deliverables
- `internal/auth/` for token management.
- Initial Main Worker implementation in `cmd/main-worker/`.
- Subscription gRPC logic.
