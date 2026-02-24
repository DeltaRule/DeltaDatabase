# Building DeltaDatabase from Source

> **Note:** For most use-cases, the [Docker / Kubernetes deployment](README.md#recommended-deployment-docker--kubernetes)
> is the recommended way to run DeltaDatabase.  Build from source only when
> you need to develop, modify, or test the code locally.

## Prerequisites

| Tool   | Minimum version | Notes                      |
|--------|-----------------|----------------------------|
| Go     | 1.25            |                            |
| Git    | any             |                            |
| Python | 3.9+            | Integration tests only     |

No external databases, message brokers, or container runtimes are required for
development.

## Build

```bash
git clone https://github.com/DeltaRule/DeltaDatabase.git
cd DeltaDatabase

# Build both workers
go build -o bin/main-worker ./cmd/main-worker/
go build -o bin/proc-worker ./cmd/proc-worker/

# Verify
./bin/main-worker --help
./bin/proc-worker --help
```

## Run Unit Tests

```bash
go test ./...
```

## Run Integration Tests

Install Python dependencies once:

```bash
cd tests
pip install -r requirements.txt
```

Run individual test suites:

```bash
# Authentication tests
pytest tests/test_authentication.py -v

# Encryption tests
pytest tests/test_encryption.py -v

# Security / hacking-technique tests
pytest tests/test_e2e_security.py -v

# Performance benchmarks
pytest tests/test_benchmarks.py -v --benchmark-sort=mean

# Full end-to-end suite (requires both workers running)
pytest tests/test_whole.py -v
```
