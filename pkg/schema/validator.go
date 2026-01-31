package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/xeipuuv/gojsonschema"
)

// Validator handles JSON schema validation against templates.
type Validator struct {
	templatesPath string
	schemas       map[string]*gojsonschema.Schema
	mu            sync.RWMutex
}

// ValidationError represents a detailed validation error.
type ValidationError struct {
	Field       string `json:"field"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ValidationResult contains the validation outcome.
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
}

// NewValidator creates a new schema validator with the given templates path.
func NewValidator(templatesPath string) (*Validator, error) {
	if templatesPath == "" {
		return nil, fmt.Errorf("templates path cannot be empty")
	}

	// Ensure templates directory exists
	if _, err := os.Stat(templatesPath); os.IsNotExist(err) {
		if err := os.MkdirAll(templatesPath, 0755); err != nil {
			return nil, fmt.Errorf("failed to create templates directory: %w", err)
		}
	}

	v := &Validator{
		templatesPath: templatesPath,
		schemas:       make(map[string]*gojsonschema.Schema),
	}

	return v, nil
}

// LoadTemplate loads a schema template from disk by schema ID.
// The template file is expected to be at <templatesPath>/<schemaID>.json
func (v *Validator) LoadTemplate(schemaID string) error {
	if schemaID == "" {
		return fmt.Errorf("schema ID cannot be empty")
	}

	v.mu.Lock()
	defer v.mu.Unlock()

	// Check if already loaded
	if _, exists := v.schemas[schemaID]; exists {
		return nil
	}

	templatePath := filepath.Join(v.templatesPath, schemaID+".json")

	// Check if template file exists
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		return fmt.Errorf("schema template not found: %s", schemaID)
	}

	// Read template file
	templateData, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template file: %w", err)
	}

	// Validate that the template is valid JSON
	var schemaDoc interface{}
	if err := json.Unmarshal(templateData, &schemaDoc); err != nil {
		return fmt.Errorf("template is not valid JSON: %w", err)
	}

	// Load schema
	schemaLoader := gojsonschema.NewBytesLoader(templateData)
	schema, err := gojsonschema.NewSchema(schemaLoader)
	if err != nil {
		return fmt.Errorf("failed to load schema: %w", err)
	}

	v.schemas[schemaID] = schema
	return nil
}

// Validate validates the given JSON data against the specified schema ID.
// Returns a ValidationResult with detailed error information.
func (v *Validator) Validate(schemaID string, jsonData []byte) (*ValidationResult, error) {
	if schemaID == "" {
		return nil, fmt.Errorf("schema ID cannot be empty")
	}

	if len(jsonData) == 0 {
		return nil, fmt.Errorf("JSON data cannot be empty")
	}

	// Ensure schema is loaded
	v.mu.RLock()
	schema, exists := v.schemas[schemaID]
	v.mu.RUnlock()

	if !exists {
		// Try to load the schema
		if err := v.LoadTemplate(schemaID); err != nil {
			return nil, fmt.Errorf("schema not loaded and failed to load: %w", err)
		}
		v.mu.RLock()
		schema = v.schemas[schemaID]
		v.mu.RUnlock()
	}

	// Validate that jsonData is valid JSON
	var doc interface{}
	if err := json.Unmarshal(jsonData, &doc); err != nil {
		return &ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{
					Field:       "(root)",
					Type:        "invalid_json",
					Description: fmt.Sprintf("Invalid JSON: %v", err),
				},
			},
		}, nil
	}

	// Perform validation
	documentLoader := gojsonschema.NewBytesLoader(jsonData)
	result, err := schema.Validate(documentLoader)
	if err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Build validation result
	validationResult := &ValidationResult{
		Valid:  result.Valid(),
		Errors: make([]ValidationError, 0),
	}

	if !result.Valid() {
		for _, err := range result.Errors() {
			validationResult.Errors = append(validationResult.Errors, ValidationError{
				Field:       err.Field(),
				Type:        err.Type(),
				Description: err.Description(),
			})
		}
	}

	return validationResult, nil
}

// ValidateStrict validates JSON data and returns an error if validation fails.
// This is a convenience method for cases where you want to fail fast on invalid data.
func (v *Validator) ValidateStrict(schemaID string, jsonData []byte) error {
	result, err := v.Validate(schemaID, jsonData)
	if err != nil {
		return err
	}

	if !result.Valid {
		return fmt.Errorf("validation failed: %d errors", len(result.Errors))
	}

	return nil
}

// GetLoadedSchemas returns a list of currently loaded schema IDs.
func (v *Validator) GetLoadedSchemas() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	schemas := make([]string, 0, len(v.schemas))
	for id := range v.schemas {
		schemas = append(schemas, id)
	}
	return schemas
}

// ReloadTemplate forces a reload of a specific schema template.
func (v *Validator) ReloadTemplate(schemaID string) error {
	if schemaID == "" {
		return fmt.Errorf("schema ID cannot be empty")
	}

	v.mu.Lock()
	delete(v.schemas, schemaID)
	v.mu.Unlock()

	return v.LoadTemplate(schemaID)
}

// ListAvailableTemplates returns all available template files in the templates directory.
func (v *Validator) ListAvailableTemplates() ([]string, error) {
	entries, err := os.ReadDir(v.templatesPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read templates directory: %w", err)
	}

	templates := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			// Remove .json extension to get schema ID
			schemaID := entry.Name()[:len(entry.Name())-5]
			templates = append(templates, schemaID)
		}
	}

	return templates, nil
}

// SaveTemplate saves a schema template to disk.
// This is useful for creating or updating templates programmatically.
func (v *Validator) SaveTemplate(schemaID string, schemaData []byte) error {
	if schemaID == "" {
		return fmt.Errorf("schema ID cannot be empty")
	}

	// Validate that schemaData is valid JSON
	var schemaDoc interface{}
	if err := json.Unmarshal(schemaData, &schemaDoc); err != nil {
		return fmt.Errorf("schema data is not valid JSON: %w", err)
	}

	// Try to load it as a schema (gojsonschema is permissive and accepts most JSON)
	// This helps catch egregious errors but won't validate full JSON Schema correctness
	schemaLoader := gojsonschema.NewBytesLoader(schemaData)
	_, err := gojsonschema.NewSchema(schemaLoader)
	if err != nil {
		return fmt.Errorf("invalid JSON schema: %w", err)
	}

	templatePath := filepath.Join(v.templatesPath, schemaID+".json")

	// Write to temp file first for atomic operation
	tempPath := templatePath + ".tmp"
	if err := os.WriteFile(tempPath, schemaData, 0644); err != nil {
		return fmt.Errorf("failed to write template file: %w", err)
	}

	// Rename to final location
	if err := os.Rename(tempPath, templatePath); err != nil {
		os.Remove(tempPath) // Clean up temp file
		return fmt.Errorf("failed to save template: %w", err)
	}

	// Reload the template into cache
	return v.ReloadTemplate(schemaID)
}
