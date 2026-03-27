package pathwalk

import (
	_ "embed"
	"fmt"

	"github.com/xeipuuv/gojsonschema"
)

//go:embed pathway.schema.json
var schemaBytes []byte

// ValidationError is a single JSON schema violation.
type ValidationError struct {
	Field   string // JSON path to the failing field
	Message string // Human-readable description
}

func (e ValidationError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s: %s", e.Field, e.Message)
	}
	return e.Message
}

// ValidationResult holds the outcome of a schema + parse validation.
type ValidationResult struct {
	SchemaErrors []ValidationError // JSON schema violations
	ParseError   error             // Structural error from ParsePathwayBytes
}

// Valid returns true when there are no schema errors and no parse error.
func (r *ValidationResult) Valid() bool {
	return len(r.SchemaErrors) == 0 && r.ParseError == nil
}

// Errors returns all errors as a flat slice, schema errors first then parse error.
func (r *ValidationResult) Errors() []error {
	var out []error
	for _, e := range r.SchemaErrors {
		out = append(out, e)
	}
	if r.ParseError != nil {
		out = append(out, r.ParseError)
	}
	return out
}

// ValidatePathwayBytes validates pathway JSON against the bundled JSON schema
// and then attempts to parse it. Both checks run independently so all errors
// are visible in a single call.
func ValidatePathwayBytes(data []byte) *ValidationResult {
	result := &ValidationResult{}

	// JSON schema check
	schemaLoader := gojsonschema.NewBytesLoader(schemaBytes)
	docLoader := gojsonschema.NewBytesLoader(data)
	sr, err := gojsonschema.Validate(schemaLoader, docLoader)
	if err != nil {
		result.SchemaErrors = []ValidationError{{Message: fmt.Sprintf("schema validation failed: %v", err)}}
		return result
	}
	for _, e := range sr.Errors() {
		result.SchemaErrors = append(result.SchemaErrors, ValidationError{
			Field:   e.Field(),
			Message: e.Description(),
		})
	}

	// Structural parse check
	_, parseErr := ParsePathwayBytes(data)
	if parseErr != nil {
		result.ParseError = parseErr
	}

	return result
}
