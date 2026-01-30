# Task 4: JSON Schema Engine

## Objective
Provide schema validation to ensure data integrity before any write operation.

## Requirements
- Implement `pkg/schema/validator.go`:
  - Load templates from `shared/db/templates/`.
  - Validate a raw JSON byte slice against a specific `schema_id`.
- Use the library `github.com/xeipuuv/gojsonschema`.
- Return detailed error messages if validation fails.

## Dependencies
- Builds on: [Task 3](task_3_agent.md).
- Validated by: `tests/test_task_4.py`.

## Deliverables
- `pkg/schema/` source files.
- Basic schema templates in `shared/db/templates/`.
