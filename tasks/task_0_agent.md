# Task 0: Test Suite Foundation (Python)

## Objective
Establish a comprehensive Python-based testing framework to validate the system's behavior at every stage of development.

## Requirements
- Use `pytest` for the testing framework.
- Use `requests` for REST API testing.
- Use `grpcio` and `grpcio-tools` for gRPC client implementations in tests.
- Implement specialized test scripts for each upcoming task (`test_task_1.py` through `test_task_11.py`).
- Implement `test_whole.py` for end-to-end integration testing.

## Deliverables
- `tests/requirements.txt`: Python dependencies.
- `tests/conftest.py`: Shared fixtures (e.g., mock shared FS paths, environment variables).
- `tests/test_task_1.py`: Basic connectivity and environment check.
- Placeholder scripts for `tests/test_task_N.py`.
- `tests/test_whole.py`: Test the whole functionallity, so authentication, Is it possible to Subscribe to the main without the right key, filling the database, updating the database, getting from the database, the speed of the database and behaivour of the database as whole.

## Logic
1. **Environment Setup**: Ensure the Python environment can reach the Go binaries once they are built.
2. **Mocking**: Provide utilities to inspect the `/shared/db/files/` directory to verify encrypted blobs independently of the Go code.
3. **Validation**: Tests should check status codes, JSON response bodies, and gRPC status codes.
