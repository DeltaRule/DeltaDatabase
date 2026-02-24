package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to create a temporary templates directory
func setupTestValidator(t *testing.T) (*Validator, string) {
	tempDir := t.TempDir()
	templatesPath := filepath.Join(tempDir, "templates")

	validator, err := NewValidator(templatesPath)
	require.NoError(t, err)
	require.NotNil(t, validator)

	return validator, templatesPath
}

// Helper function to create a simple user schema
func createUserSchema() []byte {
	schema := map[string]interface{}{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id":     "user.v1",
		"type":    "object",
		"properties": map[string]interface{}{
			"id": map[string]interface{}{
				"type": "string",
			},
			"name": map[string]interface{}{
				"type": "string",
			},
			"email": map[string]interface{}{
				"type":   "string",
				"format": "email",
			},
			"age": map[string]interface{}{
				"type":    "integer",
				"minimum": 0,
			},
		},
		"required": []string{"id", "email"},
	}

	data, _ := json.MarshalIndent(schema, "", "  ")
	return data
}

// Helper function to create a chat schema
func createChatSchema() []byte {
	schema := map[string]interface{}{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"$id":     "chat.v1",
		"type":    "object",
		"properties": map[string]interface{}{
			"chat": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"type": map[string]interface{}{
							"type": "string",
							"enum": []string{"user", "assistant", "system"},
						},
						"text": map[string]interface{}{
							"type": "string",
						},
					},
					"required": []string{"type", "text"},
				},
			},
		},
		"required": []string{"chat"},
	}

	data, _ := json.MarshalIndent(schema, "", "  ")
	return data
}

func TestNewValidator(t *testing.T) {
	t.Run("creates validator with valid path", func(t *testing.T) {
		tempDir := t.TempDir()
		validator, err := NewValidator(tempDir)

		assert.NoError(t, err)
		assert.NotNil(t, validator)
		assert.Equal(t, tempDir, validator.templatesPath)
	})

	t.Run("creates templates directory if not exists", func(t *testing.T) {
		tempDir := t.TempDir()
		templatesPath := filepath.Join(tempDir, "templates", "nested")

		validator, err := NewValidator(templatesPath)

		assert.NoError(t, err)
		assert.NotNil(t, validator)
		assert.DirExists(t, templatesPath)
	})

	t.Run("returns error for empty path", func(t *testing.T) {
		validator, err := NewValidator("")

		assert.Error(t, err)
		assert.Nil(t, validator)
		assert.Contains(t, err.Error(), "cannot be empty")
	})
}

func TestLoadTemplate(t *testing.T) {
	t.Run("loads valid schema template", func(t *testing.T) {
		validator, templatesPath := setupTestValidator(t)

		// Create a template file
		schemaData := createUserSchema()
		err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), schemaData, 0644)
		require.NoError(t, err)

		// Load the template
		err = validator.LoadTemplate("user.v1")
		assert.NoError(t, err)

		// Verify it's loaded
		schemas := validator.GetLoadedSchemas()
		assert.Contains(t, schemas, "user.v1")
	})

	t.Run("returns error for missing template", func(t *testing.T) {
		validator, _ := setupTestValidator(t)

		err := validator.LoadTemplate("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
	})

	t.Run("returns error for empty schema ID", func(t *testing.T) {
		validator, _ := setupTestValidator(t)

		err := validator.LoadTemplate("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid schema ID")
	})

	t.Run("returns error for invalid JSON in template", func(t *testing.T) {
		validator, templatesPath := setupTestValidator(t)

		// Create an invalid JSON file
		invalidJSON := []byte("{invalid json")
		err := os.WriteFile(filepath.Join(templatesPath, "invalid.json"), invalidJSON, 0644)
		require.NoError(t, err)

		err = validator.LoadTemplate("invalid")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not valid JSON")
	})

	t.Run("loads template only once", func(t *testing.T) {
		validator, templatesPath := setupTestValidator(t)

		schemaData := createUserSchema()
		err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), schemaData, 0644)
		require.NoError(t, err)

		// Load twice
		err = validator.LoadTemplate("user.v1")
		assert.NoError(t, err)

		err = validator.LoadTemplate("user.v1")
		assert.NoError(t, err)

		// Should still have only one schema
		schemas := validator.GetLoadedSchemas()
		assert.Len(t, schemas, 1)
	})
}

func TestValidate(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	// Setup user schema
	schemaData := createUserSchema()
	err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), schemaData, 0644)
	require.NoError(t, err)

	t.Run("validates correct data", func(t *testing.T) {
		validData := []byte(`{"id": "123", "name": "John Doe", "email": "john@example.com", "age": 30}`)

		result, err := validator.Validate("user.v1", validData)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.Valid)
		assert.Empty(t, result.Errors)
	})

	t.Run("validates data with only required fields", func(t *testing.T) {
		validData := []byte(`{"id": "123", "email": "john@example.com"}`)

		result, err := validator.Validate("user.v1", validData)

		assert.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("returns validation errors for missing required field", func(t *testing.T) {
		invalidData := []byte(`{"id": "123", "name": "John Doe"}`)

		result, err := validator.Validate("user.v1", invalidData)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)

		// Check that email is mentioned in errors
		found := false
		for _, e := range result.Errors {
			if e.Field == "email" || e.Type == "required" {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected error about missing email field")
	})

	t.Run("returns validation errors for wrong type", func(t *testing.T) {
		invalidData := []byte(`{"id": "123", "email": "john@example.com", "age": "thirty"}`)

		result, err := validator.Validate("user.v1", invalidData)

		assert.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("returns validation errors for invalid email format", func(t *testing.T) {
		invalidData := []byte(`{"id": "123", "email": "not-an-email"}`)

		result, err := validator.Validate("user.v1", invalidData)

		assert.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("returns error for empty schema ID", func(t *testing.T) {
		validData := []byte(`{"id": "123"}`)

		result, err := validator.Validate("", validData)

		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns error for empty JSON data", func(t *testing.T) {
		result, err := validator.Validate("user.v1", []byte{})

		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("returns validation result for invalid JSON", func(t *testing.T) {
		invalidJSON := []byte(`{invalid}`)

		result, err := validator.Validate("user.v1", invalidJSON)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
		assert.Equal(t, "invalid_json", result.Errors[0].Type)
	})

	t.Run("auto-loads schema if not loaded", func(t *testing.T) {
		// Create a new validator that hasn't loaded the schema yet
		validator2, templatesPath2 := setupTestValidator(t)
		err := os.WriteFile(filepath.Join(templatesPath2, "user.v1.json"), schemaData, 0644)
		require.NoError(t, err)

		validData := []byte(`{"id": "123", "email": "john@example.com"}`)

		result, err := validator2.Validate("user.v1", validData)

		assert.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("returns error for non-existent schema", func(t *testing.T) {
		validData := []byte(`{"id": "123"}`)

		result, err := validator.Validate("nonexistent", validData)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "not found")
	})
}

func TestValidateStrict(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	schemaData := createUserSchema()
	err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), schemaData, 0644)
	require.NoError(t, err)

	t.Run("returns nil for valid data", func(t *testing.T) {
		validData := []byte(`{"id": "123", "email": "john@example.com"}`)

		err := validator.ValidateStrict("user.v1", validData)
		assert.NoError(t, err)
	})

	t.Run("returns error for invalid data", func(t *testing.T) {
		invalidData := []byte(`{"id": "123"}`)

		err := validator.ValidateStrict("user.v1", invalidData)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "validation failed")
	})
}

func TestGetLoadedSchemas(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	t.Run("returns empty list initially", func(t *testing.T) {
		schemas := validator.GetLoadedSchemas()
		assert.Empty(t, schemas)
	})

	t.Run("returns loaded schemas", func(t *testing.T) {
		// Create and load multiple schemas
		userSchema := createUserSchema()
		chatSchema := createChatSchema()

		err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), userSchema, 0644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(templatesPath, "chat.v1.json"), chatSchema, 0644)
		require.NoError(t, err)

		err = validator.LoadTemplate("user.v1")
		require.NoError(t, err)
		err = validator.LoadTemplate("chat.v1")
		require.NoError(t, err)

		schemas := validator.GetLoadedSchemas()
		assert.Len(t, schemas, 2)
		assert.Contains(t, schemas, "user.v1")
		assert.Contains(t, schemas, "chat.v1")
	})
}

func TestReloadTemplate(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	t.Run("reloads existing template", func(t *testing.T) {
		schemaData := createUserSchema()
		err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), schemaData, 0644)
		require.NoError(t, err)

		// Load initially
		err = validator.LoadTemplate("user.v1")
		require.NoError(t, err)

		// Modify the schema file
		modifiedSchema := createChatSchema()
		err = os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), modifiedSchema, 0644)
		require.NoError(t, err)

		// Reload
		err = validator.ReloadTemplate("user.v1")
		assert.NoError(t, err)

		// Verify new schema is loaded (test with chat data)
		chatData := []byte(`{"chat": [{"type": "user", "text": "Hello"}]}`)
		result, err := validator.Validate("user.v1", chatData)
		assert.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("returns error for empty schema ID", func(t *testing.T) {
		err := validator.ReloadTemplate("")
		assert.Error(t, err)
	})
}

func TestListAvailableTemplates(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	t.Run("returns empty list for empty directory", func(t *testing.T) {
		templates, err := validator.ListAvailableTemplates()

		assert.NoError(t, err)
		assert.Empty(t, templates)
	})

	t.Run("lists available templates", func(t *testing.T) {
		// Create template files
		userSchema := createUserSchema()
		chatSchema := createChatSchema()

		err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), userSchema, 0644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(templatesPath, "chat.v1.json"), chatSchema, 0644)
		require.NoError(t, err)

		// Create a non-JSON file (should be ignored)
		err = os.WriteFile(filepath.Join(templatesPath, "readme.txt"), []byte("test"), 0644)
		require.NoError(t, err)

		templates, err := validator.ListAvailableTemplates()

		assert.NoError(t, err)
		assert.Len(t, templates, 2)
		assert.Contains(t, templates, "user.v1")
		assert.Contains(t, templates, "chat.v1")
		assert.NotContains(t, templates, "readme")
	})
}

func TestSaveTemplate(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	t.Run("saves valid schema", func(t *testing.T) {
		schemaData := createUserSchema()

		err := validator.SaveTemplate("user.v1", schemaData)
		assert.NoError(t, err)

		// Verify file exists
		templatePath := filepath.Join(templatesPath, "user.v1.json")
		assert.FileExists(t, templatePath)

		// Verify schema is loaded
		schemas := validator.GetLoadedSchemas()
		assert.Contains(t, schemas, "user.v1")
	})

	t.Run("overwrites existing template", func(t *testing.T) {
		userSchema := createUserSchema()
		chatSchema := createChatSchema()

		// Save initial schema
		err := validator.SaveTemplate("test.v1", userSchema)
		require.NoError(t, err)

		// Overwrite with different schema
		err = validator.SaveTemplate("test.v1", chatSchema)
		assert.NoError(t, err)

		// Verify new schema is active
		chatData := []byte(`{"chat": [{"type": "user", "text": "Hello"}]}`)
		result, err := validator.Validate("test.v1", chatData)
		assert.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("returns error for empty schema ID", func(t *testing.T) {
		schemaData := createUserSchema()

		err := validator.SaveTemplate("", schemaData)
		assert.Error(t, err)
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		invalidJSON := []byte(`{invalid}`)

		err := validator.SaveTemplate("test", invalidJSON)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not valid JSON")
	})

	t.Run("handles arbitrary JSON as schema", func(t *testing.T) {
		// gojsonschema accepts any valid JSON as a potential schema
		// So this won't error - it's a design choice of the library
		arbitraryJSON := []byte(`{"random": "data"}`)

		err := validator.SaveTemplate("test", arbitraryJSON)
		// This should succeed because gojsonschema is permissive
		assert.NoError(t, err)
	})
}

func TestChatSchemaValidation(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	chatSchema := createChatSchema()
	err := os.WriteFile(filepath.Join(templatesPath, "chat.v1.json"), chatSchema, 0644)
	require.NoError(t, err)

	t.Run("validates correct chat data", func(t *testing.T) {
		validData := []byte(`{
			"chat": [
				{"type": "user", "text": "Hello"},
				{"type": "assistant", "text": "Hi there!"},
				{"type": "system", "text": "Conversation started"}
			]
		}`)

		result, err := validator.Validate("chat.v1", validData)

		assert.NoError(t, err)
		assert.True(t, result.Valid)
	})

	t.Run("rejects invalid message type", func(t *testing.T) {
		invalidData := []byte(`{
			"chat": [
				{"type": "invalid_type", "text": "Hello"}
			]
		}`)

		result, err := validator.Validate("chat.v1", invalidData)

		assert.NoError(t, err)
		assert.False(t, result.Valid)
		assert.NotEmpty(t, result.Errors)
	})

	t.Run("rejects missing required field", func(t *testing.T) {
		invalidData := []byte(`{
			"chat": [
				{"type": "user"}
			]
		}`)

		result, err := validator.Validate("chat.v1", invalidData)

		assert.NoError(t, err)
		assert.False(t, result.Valid)
	})
}

func TestConcurrentValidation(t *testing.T) {
	validator, templatesPath := setupTestValidator(t)

	schemaData := createUserSchema()
	err := os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), schemaData, 0644)
	require.NoError(t, err)

	t.Run("handles concurrent validation", func(t *testing.T) {
		var wg sync.WaitGroup
		errors := make(chan error, 100)

		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				// Create properly formatted JSON with string ID
				data := []byte(fmt.Sprintf(`{"id": "user_%d", "email": "test@example.com"}`, id))
				result, err := validator.Validate("user.v1", data)

				if err != nil {
					errors <- err
					return
				}

				if !result.Valid {
					errors <- fmt.Errorf("validation failed for id %d", id)
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			assert.NoError(t, err)
		}
	})

	t.Run("handles concurrent template loading", func(t *testing.T) {
		validator2, templatesPath2 := setupTestValidator(t)

		// Create multiple schemas
		for i := 0; i < 10; i++ {
			schemaID := filepath.Join(templatesPath2, string(rune('a'+i))+".json")
			err := os.WriteFile(schemaID, createUserSchema(), 0644)
			require.NoError(t, err)
		}

		var wg sync.WaitGroup
		errors := make(chan error, 10)

		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				schemaID := string(rune('a' + id))
				err := validator2.LoadTemplate(schemaID)
				if err != nil {
					errors <- err
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			assert.NoError(t, err)
		}

		schemas := validator2.GetLoadedSchemas()
		assert.Len(t, schemas, 10)
	})
}

func TestComplexSchemas(t *testing.T) {
	validator, _ := setupTestValidator(t)

	t.Run("validates nested objects", func(t *testing.T) {
		schema := map[string]interface{}{
			"$schema": "http://json-schema.org/draft-07/schema#",
			"type":    "object",
			"properties": map[string]interface{}{
				"user": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name": map[string]string{"type": "string"},
						"address": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"street": map[string]string{"type": "string"},
								"city":   map[string]string{"type": "string"},
							},
							"required": []string{"city"},
						},
					},
					"required": []string{"name"},
				},
			},
		}

		schemaData, _ := json.Marshal(schema)
		err := validator.SaveTemplate("nested", schemaData)
		require.NoError(t, err)

		validData := []byte(`{
			"user": {
				"name": "John",
				"address": {
					"street": "123 Main St",
					"city": "New York"
				}
			}
		}`)

		result, err := validator.Validate("nested", validData)
		assert.NoError(t, err)
		assert.True(t, result.Valid)

		// Missing required nested field
		invalidData := []byte(`{
			"user": {
				"name": "John",
				"address": {
					"street": "123 Main St"
				}
			}
		}`)

		result, err = validator.Validate("nested", invalidData)
		assert.NoError(t, err)
		assert.False(t, result.Valid)
	})
}

// Benchmark tests
func BenchmarkValidate(b *testing.B) {
	tempDir := b.TempDir()
	templatesPath := filepath.Join(tempDir, "templates")
	validator, _ := NewValidator(templatesPath)

	schemaData := createUserSchema()
	os.WriteFile(filepath.Join(templatesPath, "user.v1.json"), schemaData, 0644)
	validator.LoadTemplate("user.v1")

	validData := []byte(`{"id": "123", "email": "john@example.com", "name": "John Doe", "age": 30}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.Validate("user.v1", validData)
	}
}

func BenchmarkValidateLargeDocument(b *testing.B) {
	tempDir := b.TempDir()
	templatesPath := filepath.Join(tempDir, "templates")
	validator, _ := NewValidator(templatesPath)

	schemaData := createChatSchema()
	os.WriteFile(filepath.Join(templatesPath, "chat.v1.json"), schemaData, 0644)
	validator.LoadTemplate("chat.v1")

	// Create a large chat document with 1000 messages
	messages := make([]map[string]string, 1000)
	for i := 0; i < 1000; i++ {
		messages[i] = map[string]string{
			"type": "user",
			"text": "This is message number " + string(rune(i)),
		}
	}

	doc := map[string]interface{}{"chat": messages}
	validData, _ := json.Marshal(doc)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		validator.Validate("chat.v1", validData)
	}
}
