# Task 1: Project Skeleton & Protobuf Definitions

## Objective
Initialize the Go project and define the communication contracts between all components.

## Requirements
- Initialize the Go module: `go mod init delta-db`.
- Create the directory structure as specified in `Guideline.md`.
- Define gRPC services in `api/proto/worker.proto`:
  - `Subscribe`: For Processing Workers to register with the Main Worker.
  - `Process`: For internal operations (Get, Put) between Main and Processing Workers.
- Generate Go code from the proto definitions.

## Dependencies
- Builds on: [Task 0](task_0_agent.md) (Testing foundation).
- Validated by: `tests/test_task_1.py`.

## Deliverables
- `go.mod` and `go.sum`.
- `api/proto/worker.proto`.
- Generated `.pb.go` and `_grpc.pb.go` files.
- Empty `main.go` files in `cmd/main-worker/` and `cmd/proc-worker/`.
