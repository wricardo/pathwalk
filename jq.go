package pathwalk

import (
	"encoding/json"
	"fmt"

	"github.com/itchyny/gojq"
)

// RunJQ applies a jq expression to data and returns the result.
// If the expression produces multiple values, they are returned as a slice.
// If it produces a single value, that value is returned directly.
func RunJQ(expr string, data any) (any, error) {
	query, err := gojq.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("jq parse error: %w", err)
	}

	code, err := gojq.Compile(query)
	if err != nil {
		return nil, fmt.Errorf("jq compile error: %w", err)
	}

	input := normalizeJQInput(data)

	var results []any
	iter := code.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			return nil, fmt.Errorf("jq: %w", err)
		}
		results = append(results, v)
	}

	if len(results) == 0 {
		return nil, nil
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

// normalizeJQInput ensures the input uses only types that gojq can traverse:
// nil, bool, int, float64, string, []any, and map[string]any.
// Go-native types like []map[string]any are round-tripped through JSON.
func normalizeJQInput(data any) any {
	switch v := data.(type) {
	case json.RawMessage:
		var parsed any
		if err := json.Unmarshal(v, &parsed); err != nil {
			return string(v)
		}
		return parsed
	case []byte:
		var parsed any
		if err := json.Unmarshal(v, &parsed); err != nil {
			return string(v)
		}
		return parsed
	case string:
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err != nil {
			return v
		}
		return parsed
	case nil:
		return nil
	default:
		// Round-trip through JSON to convert any Go types
		// (including map[string]any with nested typed slices like
		// []map[string]any) into the generic types gojq expects
		// (map[string]any with []any).
		b, err := json.Marshal(v)
		if err != nil {
			return v
		}
		var parsed any
		if err := json.Unmarshal(b, &parsed); err != nil {
			return v
		}
		return parsed
	}
}
