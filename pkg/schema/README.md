# pkg/schema

JSON Schema validation logic and helpers for the DeltaDatabase system.

## Overview

The `schema` package provides JSON Schema validation capabilities to ensure data integrity before any write operation. It uses JSON Schema (draft-07) templates to validate JSON documents against predefined schemas.

## Features

- **Template Management**: Load and manage JSON Schema templates from disk
- **Validation**: Validate JSON data against schema templates with detailed error reporting
- **Caching**: Automatically cache loaded schemas for performance
- **Thread-Safe**: Concurrent validation and template loading
- **Auto-Loading**: Automatically load schemas on first use
- **Template CRUD**: Save, reload, and list available templates

## Architecture

```
┌─────────────────┐
│   Validator     │
├─────────────────┤
│ - templates map │ (in-memory cache)
│ - mutex         │ (thread-safe)
└─────────────────┘
        │
        ├─── LoadTemplate()
        ├─── Validate()
        ├─── ValidateStrict()
        ├─── SaveTemplate()
        └─── ReloadTemplate()
        
shared/db/templates/
├── user.v1.json
├── chat.v1.json
└── document.v1.json
```

## Usage

### Creating a Validator

```go
import "delta-db/pkg/schema"

// Create validator with templates directory
validator, err := schema.NewValidator("/path/to/shared/db/templates")
if err != nil {
    log.Fatal(err)
}
```

### Loading Templates

```go
// Explicitly load a template
err := validator.LoadTemplate("user.v1")
if err != nil {
    log.Fatal(err)
}

// Or let Validate() auto-load it on first use
result, err := validator.Validate("user.v1", jsonData)
```

### Validating JSON Data

```go
jsonData := []byte(`{
    "id": "123",
    "email": "user@example.com",
    "name": "John Doe"
}`)

// Validate and get detailed results
result, err := validator.Validate("user.v1", jsonData)
if err != nil {
    log.Fatal(err)
}

if !result.Valid {
    for _, e := range result.Errors {
        fmt.Printf("Field: %s, Type: %s, Description: %s\n",
            e.Field, e.Type, e.Description)
    }
}

// Or use strict validation (fail fast)
err = validator.ValidateStrict("user.v1", jsonData)
if err != nil {
    log.Fatal(err)
}
```

### Managing Templates

```go
// List available templates in directory
templates, err := validator.ListAvailableTemplates()
for _, tmpl := range templates {
    fmt.Println(tmpl)
}

// Get currently loaded schemas
loaded := validator.GetLoadedSchemas()

// Save a new template
schemaData := []byte(`{
    "$schema": "http://json-schema.org/draft-07/schema#",
    "type": "object",
    "properties": {
        "id": {"type": "string"}
    }
}`)
err = validator.SaveTemplate("myschema.v1", schemaData)

// Reload a template (e.g., after external modification)
err = validator.ReloadTemplate("user.v1")
```

## JSON Schema Format

Templates must be valid JSON Schema (draft-07 or compatible). Example:

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "$id": "user.v1",
  "type": "object",
  "properties": {
    "id": {
      "type": "string"
    },
    "email": {
      "type": "string",
      "format": "email"
    },
    "age": {
      "type": "integer",
      "minimum": 0
    }
  },
  "required": ["id", "email"]
}
```

## Validation Errors

The `ValidationResult` structure provides detailed error information:

```go
type ValidationError struct {
    Field       string // e.g., "email", "user.age"
    Type        string // e.g., "required", "invalid_type", "format"
    Description string // Human-readable description
}

type ValidationResult struct {
    Valid  bool
    Errors []ValidationError
}
```

## Performance Considerations

- **Caching**: Loaded schemas are cached in memory. First validation loads the schema; subsequent validations use the cached version.
- **Thread-Safety**: All operations are thread-safe using read-write locks.
- **Template Loading**: Templates are loaded lazily on first use unless explicitly pre-loaded.

## Error Handling

The validator distinguishes between:
- **System Errors**: Template not found, invalid schema file, I/O errors (returned as `error`)
- **Validation Errors**: JSON doesn't match schema (returned in `ValidationResult`)

```go
result, err := validator.Validate("user.v1", data)
if err != nil {
    // System error (template missing, I/O error, etc.)
    log.Fatal(err)
}

if !result.Valid {
    // Validation error (data doesn't match schema)
    for _, e := range result.Errors {
        fmt.Printf("Validation failed: %s\n", e.Description)
    }
}
```

## Integration with DeltaDatabase

The schema validator is used by Processing Workers to validate JSON before encryption and storage:

1. Client sends data to Main Worker
2. Main Worker routes to Processing Worker
3. Processing Worker validates against schema using this package
4. If valid: encrypt and store; if invalid: return validation errors

## Testing

Run unit tests:
```bash
go test ./pkg/schema/
go test ./pkg/schema/ -v              # Verbose
go test ./pkg/schema/ -cover          # With coverage
go test ./pkg/schema/ -bench=.        # Benchmarks
```

## Dependencies

- `github.com/xeipuuv/gojsonschema`: JSON Schema validation library
- Standard library: `encoding/json`, `os`, `path/filepath`, `sync`
