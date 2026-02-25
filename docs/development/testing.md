
# Testing

DeltaDatabase has two complementary test suites:

1. **Go unit tests** — fast, isolated tests for each `pkg/` module.
2. **Python integration tests** — end-to-end tests that run both workers and verify behaviour through the REST API.

---

## Go Unit Tests

Run all Go unit tests from the repository root:

```bash
go test ./...
```

With the race detector enabled (recommended during development):

```bash
go test -race ./...
```

With coverage report:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Test Coverage Requirements

Every `pkg/` module must maintain **100% unit test coverage**. Tests use `github.com/stretchr/testify` for assertions and `testify/mock` for interface mocks.

---

## Python Integration Tests

### Setup (once)

```bash
cd tests
pip install -r requirements.txt
```

### Individual Test Suites

| Suite | Command | What it tests |
|-------|---------|---------------|
| Authentication | `pytest tests/test_authentication.py -v` | Login, token expiry, invalid tokens |
| Encryption | `pytest tests/test_encryption.py -v` | AES-GCM round-trips, tamper detection |
| Security | `pytest tests/test_e2e_security.py -v` | Injection, path traversal, unauthorized access |
| Benchmarks | `pytest tests/test_benchmarks.py -v --benchmark-sort=mean` | Throughput and latency measurements |
| Full E2E | `pytest tests/test_whole.py -v` | Complete product behaviour |

### Running the Full Suite

The full end-to-end suite requires both workers to be running before you start pytest:

```bash
# Terminal 1 — start the Main Worker
./bin/main-worker -grpc-addr=127.0.0.1:50051 -rest-addr=127.0.0.1:8080 -shared-fs=./shared/db

# Terminal 2 — start a Processing Worker
./bin/proc-worker -main-addr=127.0.0.1:50051 -worker-id=proc-1 -grpc-addr=127.0.0.1:50052 -shared-fs=./shared/db

# Terminal 3 — run the tests
pytest tests/test_whole.py -v
```

### Benchmark Results

See the [Benchmark Results](../usage/benchmarks) page for measured numbers.

---

## Test File Overview

| File | Description |
|------|-------------|
| `tests/test_authentication.py` | Verifies login, token lifecycle, invalid credentials |
| `tests/test_encryption.py` | Confirms data is encrypted on disk and correctly decrypted on read |
| `tests/test_e2e_security.py` | Attacks: path traversal in keys, oversized payloads, token replay |
| `tests/test_benchmarks.py` | Measures PUT/GET throughput and latency under sequential and concurrent load |
| `tests/test_whole.py` | Full product test: auth → schema → write → read → cache hit → concurrency |

---

## Continuous Integration

All tests run automatically on every pull request via GitHub Actions. The pipeline:

1. Builds both workers with `go build`.
2. Runs `go test -race ./...`.
3. Starts both workers, runs `pytest tests/test_whole.py -v`.
4. Reports coverage.
