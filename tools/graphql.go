// Package tools provides built-in tool implementations for use with the
// pathwalk [pathwalk.Engine]. The primary type is [GraphQLTool],
// which exposes six tools to the LLM: graphql_query and graphql_mutation for
// execution, graphql_queries and graphql_mutations to list available operations,
// and graphql_types and graphql_type for schema exploration.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	pathwalk "github.com/wricardo/pathwalk"
)

// GraphQLTool executes GraphQL queries and mutations against a configured endpoint.
// The endpoint is set once at construction; per-call overrides are not supported.
type GraphQLTool struct {
	Endpoint string
	Headers  map[string]string
}

// queryTool returns the engine Tool for executing GraphQL queries.
func (t *GraphQLTool) queryTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name:        "graphql_query",
		Description: "Execute a GraphQL query against the configured endpoint. Returns the JSON response.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The GraphQL query string",
				},
				"variables": map[string]any{
					"type":        "object",
					"description": "Optional variables for the query",
				},
			},
			"required": []string{"query"},
		},
		Fn: t.execute,
	}
}

// mutationTool returns the engine Tool for executing GraphQL mutations.
func (t *GraphQLTool) mutationTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name:        "graphql_mutation",
		Description: "Execute a GraphQL mutation against the configured endpoint. Returns the JSON response.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "The GraphQL mutation string",
				},
				"variables": map[string]any{
					"type":        "object",
					"description": "Optional variables for the mutation",
				},
			},
			"required": []string{"query"},
		},
		Fn: t.execute,
	}
}

func (t *GraphQLTool) execute(ctx context.Context, args map[string]any) (any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("graphql tool: 'query' argument must be a non-empty string")
	}

	payload := map[string]any{
		"query": query,
	}
	if variables, ok := args["variables"]; ok && variables != nil {
		payload["variables"] = variables
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("graphql tool: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("graphql tool: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphql tool: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("graphql tool: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("graphql tool: server returned %d: %s", resp.StatusCode, respBody)
	}

	var result any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return string(respBody), nil
	}
	return result, nil
}
