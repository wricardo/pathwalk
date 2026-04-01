// Package tools provides built-in tool implementations for use with the
// pathwalk [pathwalk.Engine]. The primary type is [GraphQLTool],
// which exposes tools to the LLM for GraphQL execution, schema exploration,
// and batch operations with optional jq filtering.
package tools

import (
	"bufio"
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
//
// When Name is non-empty, all tool names get a "_<Name>" suffix so multiple
// endpoints can coexist in the same engine:
//
//	graphql_query_sheets, graphql_mutation_sheets, graphql_queries_sheets …
type GraphQLTool struct {
	Endpoint string
	Headers  map[string]string
	Name     string // optional suffix for multi-endpoint pathways
}

// toolName returns the full tool name, appending "_<Name>" when set.
func (t *GraphQLTool) toolName(base string) string {
	if t.Name != "" {
		return base + "_" + t.Name
	}
	return base
}

// queryTool returns the engine Tool for executing GraphQL queries.
func (t *GraphQLTool) queryTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name:        t.toolName("graphql_query"),
		Description: "Execute a GraphQL query against the configured endpoint. Returns the JSON response. Use the optional 'jq' parameter to filter the response server-side and reduce output size.",
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
				"jq": map[string]any{
					"type":        "string",
					"description": "Optional jq expression to filter the response. Applied server-side if supported, otherwise client-side. Receives the full {\"data\":...} envelope. Examples: '.data.users[].name', '.data.orders | length'",
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
		Name:        t.toolName("graphql_mutation"),
		Description: "Execute a GraphQL mutation against the configured endpoint. Returns the JSON response. Use the optional 'jq' parameter to filter the response.",
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
				"jq": map[string]any{
					"type":        "string",
					"description": "Optional jq expression to filter the response",
				},
			},
			"required": []string{"query"},
		},
		Fn: t.execute,
	}
}

// batchTool returns the engine Tool for executing multiple GraphQL operations
// in a single HTTP request using NDJSON.
func (t *GraphQLTool) batchTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name: t.toolName("graphql_batch"),
		Description: "Execute multiple GraphQL operations in a single request. " +
			"Each operation can have its own query, variables, and jq filter. " +
			"Returns an array of results (one per operation). " +
			"Use this when you need to fetch data from multiple queries at once to reduce round trips.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operations": map[string]any{
					"type":        "array",
					"description": "Array of GraphQL operations to execute",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "string",
								"description": "The GraphQL query or mutation string",
							},
							"variables": map[string]any{
								"type":        "object",
								"description": "Optional variables",
							},
							"jq": map[string]any{
								"type":        "string",
								"description": "Optional jq filter for this operation's response",
							},
						},
						"required": []string{"query"},
					},
				},
			},
			"required": []string{"operations"},
		},
		Fn: t.executeBatch,
	}
}

// exploreTool returns the engine Tool for batched schema exploration.
func (t *GraphQLTool) exploreTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name: t.toolName("graphql_explore"),
		Description: "Explore the GraphQL schema efficiently. Fetches the list of available " +
			"query/mutation fields and optionally describes specific types — all in a single request. " +
			"Use this at the start of a task to understand what operations and types are available.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"types": map[string]any{
					"type":        "array",
					"description": "Optional list of type names to describe (e.g. [\"User\", \"CreateUserInput\"]). Each type's fields and their types are returned.",
					"items":       map[string]any{"type": "string"},
				},
				"include_queries": map[string]any{
					"type":        "boolean",
					"description": "Include all available Query fields (default: true)",
				},
				"include_mutations": map[string]any{
					"type":        "boolean",
					"description": "Include all available Mutation fields (default: false)",
				},
			},
		},
		Fn: t.executeExplore,
	}
}

func (t *GraphQLTool) execute(ctx context.Context, args map[string]any) (any, error) {
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("graphql tool: 'query' argument must be a non-empty string")
	}

	jqExpr, _ := args["jq"].(string)

	payload := map[string]any{
		"query": query,
	}
	if variables, ok := args["variables"]; ok && variables != nil {
		payload["variables"] = variables
	}
	if jqExpr != "" {
		payload["jq"] = jqExpr
	}

	result, err := t.doRequest(ctx, "application/json", payload)
	if err != nil {
		return nil, err
	}

	// If the server didn't apply jq (no jq support), apply client-side.
	if jqExpr != "" {
		if m, ok := result.(map[string]any); ok {
			if _, hasData := m["data"]; hasData {
				// Response still has data envelope — server didn't apply jq.
				filtered, jqErr := RunJQ(jqExpr, result)
				if jqErr != nil {
					return result, nil // Return unfiltered on jq error
				}
				return filtered, nil
			}
		}
	}

	return result, nil
}

// batchOp is a single operation in a batch request.
type batchOp struct {
	Query     string `json:"query"`
	Variables any    `json:"variables,omitempty"`
	JQ        string `json:"jq,omitempty"`
}

func (t *GraphQLTool) executeBatch(ctx context.Context, args map[string]any) (any, error) {
	opsRaw, ok := args["operations"].([]any)
	if !ok || len(opsRaw) == 0 {
		return nil, fmt.Errorf("graphql_batch: 'operations' must be a non-empty array")
	}

	// Build NDJSON body
	var buf bytes.Buffer
	var ops []batchOp

	for i, raw := range opsRaw {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("graphql_batch: operations[%d] must be an object", i)
		}
		q, _ := m["query"].(string)
		if q == "" {
			return nil, fmt.Errorf("graphql_batch: operations[%d] missing 'query'", i)
		}
		op := batchOp{Query: q, Variables: m["variables"]}
		if jq, ok := m["jq"].(string); ok {
			op.JQ = jq
		}
		ops = append(ops, op)
	}

	enc := json.NewEncoder(&buf)
	for _, op := range ops {
		if err := enc.Encode(op); err != nil {
			return nil, fmt.Errorf("graphql_batch: encode: %w", err)
		}
	}

	// Send as NDJSON
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("graphql_batch: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphql_batch: HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("graphql_batch: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("graphql_batch: server returned %d: %s", resp.StatusCode, respBody)
	}

	// Parse NDJSON response — one JSON object per line
	var results []any
	scanner := bufio.NewScanner(bytes.NewReader(respBody))
	i := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var item any
		if err := json.Unmarshal(line, &item); err != nil {
			results = append(results, string(line))
		} else {
			// Client-side jq fallback for servers without jq support
			if i < len(ops) && ops[i].JQ != "" {
				if m, ok := item.(map[string]any); ok {
					if _, hasData := m["data"]; hasData {
						filtered, jqErr := RunJQ(ops[i].JQ, item)
						if jqErr == nil {
							item = filtered
						}
					}
				}
			}
			results = append(results, item)
		}
		i++
	}

	// If server doesn't support NDJSON (returned a single JSON response),
	// fall back to sequential execution.
	if len(results) == 0 {
		var singleResult any
		if err := json.Unmarshal(respBody, &singleResult); err == nil {
			// Server returned single response — NDJSON not supported.
			// Fall back to individual requests.
			return t.executeBatchFallback(ctx, ops)
		}
	}

	return results, nil
}

// executeBatchFallback executes operations one at a time when the server
// doesn't support NDJSON.
func (t *GraphQLTool) executeBatchFallback(ctx context.Context, ops []batchOp) (any, error) {
	var results []any
	for _, op := range ops {
		args := map[string]any{"query": op.Query}
		if op.Variables != nil {
			args["variables"] = op.Variables
		}
		if op.JQ != "" {
			args["jq"] = op.JQ
		}
		result, err := t.execute(ctx, args)
		if err != nil {
			results = append(results, map[string]any{"error": err.Error()})
		} else {
			results = append(results, result)
		}
	}
	return results, nil
}

func (t *GraphQLTool) executeExplore(ctx context.Context, args map[string]any) (any, error) {
	includeQueries := true
	if v, ok := args["include_queries"].(bool); ok {
		includeQueries = v
	}
	includeMutations := false
	if v, ok := args["include_mutations"].(bool); ok {
		includeMutations = v
	}

	typeNames, _ := args["types"].([]any)

	// Build batch operations
	type batchOp struct {
		Query string `json:"query"`
		JQ    string `json:"jq,omitempty"`
	}
	var ops []batchOp

	const typeRefFragment = `{ kind name ofType { kind name ofType { kind name ofType { kind name } } } }`

	if includeQueries {
		ops = append(ops, batchOp{
			Query: fmt.Sprintf(`{ __type(name: "Query") { fields { name args { name type %s } type %s } } }`, typeRefFragment, typeRefFragment),
			JQ:    `.data.__type.fields | map({name, args: (.args // [] | map(.name + ": " + (.type | .name // .ofType.name // ""))), returns: (.type | .name // .ofType.name // "")})`,
		})
	}

	if includeMutations {
		ops = append(ops, batchOp{
			Query: fmt.Sprintf(`{ __type(name: "Mutation") { fields { name args { name type %s } type %s } } }`, typeRefFragment, typeRefFragment),
			JQ:    `.data.__type.fields | map({name, args: (.args // [] | map(.name + ": " + (.type | .name // .ofType.name // ""))), returns: (.type | .name // .ofType.name // "")})`,
		})
	}

	for _, name := range typeNames {
		n, ok := name.(string)
		if !ok || n == "" {
			continue
		}
		ops = append(ops, batchOp{
			Query: fmt.Sprintf(`{ __type(name: "%s") { name kind fields { name type %s } inputFields { name type %s } enumValues { name } } }`, n, typeRefFragment, typeRefFragment),
			JQ:    ".data.__type",
		})
	}

	if len(ops) == 0 {
		return "no exploration requested", nil
	}

	// Try NDJSON batch
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, op := range ops {
		_ = enc.Encode(op)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, &buf)
	if err != nil {
		return nil, fmt.Errorf("graphql_explore: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("graphql_explore: HTTP request: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("graphql_explore: read response: %w", err)
	}

	// Parse NDJSON response
	result := map[string]any{}
	scanner := bufio.NewScanner(bytes.NewReader(respBody))
	i := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var item any
		if err := json.Unmarshal(line, &item); err != nil {
			continue
		}

		// Client-side jq fallback
		if i < len(ops) && ops[i].JQ != "" {
			if m, ok := item.(map[string]any); ok {
				if _, hasData := m["data"]; hasData {
					filtered, jqErr := RunJQ(ops[i].JQ, item)
					if jqErr == nil {
						item = filtered
					}
				}
			}
		}

		// Label results
		switch {
		case includeQueries && i == 0:
			result["queries"] = item
		case includeMutations && ((includeQueries && i == 1) || (!includeQueries && i == 0)):
			result["mutations"] = item
		default:
			// Type descriptions
			if types, ok := result["types"].([]any); ok {
				result["types"] = append(types, item)
			} else {
				result["types"] = []any{item}
			}
		}
		i++
	}

	return result, nil
}

// doRequest sends a single JSON payload to the GraphQL endpoint.
func (t *GraphQLTool) doRequest(ctx context.Context, contentType string, payload any) (any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("graphql tool: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("graphql tool: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
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
