# Task 10: Main Worker - REST API & Client Auth

## Objective
Expose the system to external clients through a secure REST gateway.

## Requirements
- Use `github.com/gin-gonic/gin`.
- Implement endpoints:
  - `GET /entity/:db?key=:key`
  - `PUT /entity/:db`
- Implement Bearer Token authentication for clients.
- Logic to route the request to a healthy, subscribed Processing Worker.

## Dependencies
- Builds on: [Task 9](task_9_agent.md).
- Validated by: `tests/test_task_10.py` (REST client simulation).

## Deliverables
- `api/rest/` handlers.
- Main Worker routing logic (Round Robin or Random).
