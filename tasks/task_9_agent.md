# Task 9: Processing Worker - Write Operations (Put)

## Objective
Implement the logic to create or update data entities with validation and encryption.

## Requirements
- Implement the `Process` gRPC handler for `PUT` operations.
- Flow:
  1. Validate incoming JSON against the correct schema.
  2. Obtain exclusive lock via `pkg/fs`.
  3. Increment version in metadata.
  4. Encrypt JSON into a new blob.
  5. Atomic write to disk (Write temp -> rename).
  6. Update Cache and release lock.

## Dependencies
- Builds on: [Task 8](task_8_agent.md).
- Validated by: `tests/test_task_9.py`.

## Functional Tests
- [tests/test_data_integrity.py](../tests/test_data_integrity.py)
- [tests/test_encryption.py](../tests/test_encryption.py)
- [tests/test_concurrency.py](../tests/test_concurrency.py)

## Deliverables
- `PUT` logic in Processing Worker.
- FS exclusive locking integration.
- Schema validation integration.
