package tools

import (
	"context"
	"fmt"

	pathwalk "github.com/wricardo/pathwalk"
)

// JqTool executes jq filters on JSON data using a pure Go implementation
// (github.com/itchyny/gojq). No external binary required.
type JqTool struct{}

// AsTools returns the jq tool.
func (JqTool) AsTools() []pathwalk.Tool {
	return []pathwalk.Tool{
		{
			Name:        "jq",
			Description: "Execute a jq filter on JSON data. Use jq expressions like '.user.email' or '.items | map(.id)'",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data": map[string]any{
						"description": "The JSON data to filter (object, array, string, number, etc.)",
					},
					"filter": map[string]any{
						"type":        "string",
						"description": "The jq filter expression, e.g. '.user.email' or '.[] | select(.age > 18)'",
					},
				},
				"required": []string{"data", "filter"},
			},
			Fn: jqExecute,
		},
	}
}

func jqExecute(ctx context.Context, args map[string]any) (any, error) {
	filter, ok := args["filter"].(string)
	if !ok || filter == "" {
		return nil, fmt.Errorf("jq: 'filter' argument must be a non-empty string")
	}

	data := args["data"]
	return pathwalk.RunJQ(filter, data)
}

// RunJQ is a convenience wrapper around pathwalk.RunJQ for use within the tools package.
func RunJQ(expr string, data any) (any, error) {
	return pathwalk.RunJQ(expr, data)
}
