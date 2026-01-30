# Task 7: Processing Worker - Lifecycle & Handshake

## Objective
Implement the initial startup phase of the Processing Worker.

## Requirements
- Background process that runs on startup.
- Connect to Main Worker via gRPC.
- Call `Subscribe` and handle the response.
- Unwrap the received Encryption Key and store it in a secure, memory-only variable.
- Keep the session token for future `Process` RPCs.

## Dependencies
- Builds on: [Task 6](task_6_agent.md).
- Validated by: `tests/test_task_7.py`.

## Deliverables
- `cmd/proc-worker/main.go` logic for handshake.
- Integration test checking if Proc Worker becomes "Available" in Main Worker's registry.
