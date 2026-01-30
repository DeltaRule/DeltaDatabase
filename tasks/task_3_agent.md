# Task 3: Shared Filesystem Layer & Locking

## Objective
Enable persistent storage of encrypted JSON blobs with metadata and concurrency control.

## Requirements
- Implement `pkg/fs/storage.go`:
  - `WriteFile(id, data, metadata)`: Writes the `.json.enc` and `.meta.json` files.
  - `ReadFile(id)`: Reads both files and returns the blob + meta.
- Implement `pkg/fs/lock.go`:
  - Advisory locking using `go-frankenlock` or standard `flock`.
  - Shared locks for reading, Exclusive locks for writing.
- Handle directory creation for `files/` and `templates/`.

## Dependencies
- Builds on: [Task 2](task_2_agent.md).
- Validated by: `tests/test_task_3.py`.

## Deliverables
- `pkg/fs/` source files.
- Manual verification of file layout on disk.
