# Task 8: Processing Worker - Read Operations (Get)

## Objective
Implement the logic to retrieve and decrypt data entities.

## Requirements
- Implement the `Process` gRPC handler for `GET` operations.
- Flow:
  1. Check `pkg/cache`. If hit and version match, return metadata + data.
  2. If miss, obtain shared lock via `pkg/fs`.
  3. Load `.meta.json` and `.json.enc`.
  4. Decrypt using memory-resident key from Task 7.
  5. Store in Cache and release lock.
  6. Return JSON to caller.

## Dependencies
- Builds on: [Task 7](task_7_agent.md).
- Validated by: `tests/test_task_8.py`.

## Functional Tests
- [tests/test_apis.py](../tests/test_apis.py)
- [tests/test_caching.py](../tests/test_caching.py)
- [tests/test_data_integrity.py](../tests/test_data_integrity.py)

## Deliverables
- `GET` logic in Processing Worker.
- FS shared locking integration.
