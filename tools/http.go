package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	pathwalk "github.com/wricardo/pathwalk"
)

// HTTPTool executes HTTP requests (REST API calls).
type HTTPTool struct {
	// DefaultHeaders are applied to all requests and can be overridden per-call.
	DefaultHeaders map[string]string
}

// AsTools returns the http_request tool.
func (t HTTPTool) AsTools() []pathwalk.Tool {
	return []pathwalk.Tool{
		{
			Name:        "http_request",
			Description: "Make an HTTP request (GET, POST, PUT, DELETE, PATCH). Returns status code, response body, and headers.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url": map[string]any{
						"type":        "string",
						"description": "The URL to request, e.g. 'https://api.example.com/users'",
					},
					"method": map[string]any{
						"type":        "string",
						"description": "HTTP method: GET, POST, PUT, DELETE, PATCH. Defaults to GET.",
					},
					"headers": map[string]any{
						"type":        "object",
						"description": "HTTP headers as key-value pairs, e.g. {'Authorization': 'Bearer token'}",
					},
					"body": map[string]any{
						"description": "Request body (string or object that will be JSON-encoded)",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Request timeout in seconds. Defaults to 30.",
					},
				},
				"required": []string{"url"},
			},
			Fn: t.execute,
		},
	}
}

type httpResponse struct {
	StatusCode int               `json:"status_code"`
	Body       string            `json:"body"`
	Headers    map[string]string `json:"headers"`
}

func (t HTTPTool) execute(ctx context.Context, args map[string]any) (any, error) {
	url, ok := args["url"].(string)
	if !ok || url == "" {
		return nil, fmt.Errorf("http_request: 'url' is required")
	}

	method := "GET"
	if m, ok := args["method"].(string); ok && m != "" {
		method = m
	}

	// Build request body
	var body io.Reader
	if bodyArg, ok := args["body"]; ok && bodyArg != nil {
		switch b := bodyArg.(type) {
		case string:
			body = bytes.NewReader([]byte(b))
		default:
			// If it's an object/array, JSON-encode it
			bodyBytes, err := json.Marshal(b)
			if err != nil {
				return nil, fmt.Errorf("http_request: encode body: %w", err)
			}
			body = bytes.NewReader(bodyBytes)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("http_request: build request: %w", err)
	}

	// Apply default headers first
	for k, v := range t.DefaultHeaders {
		req.Header.Set(k, v)
	}

	// Override with per-call headers
	if headers, ok := args["headers"].(map[string]any); ok {
		for k, v := range headers {
			if vStr, ok := v.(string); ok {
				req.Header.Set(k, vStr)
			}
		}
	}

	// Set timeout
	timeout := 30 * time.Second
	if to, ok := args["timeout"].(float64); ok && to > 0 {
		timeout = time.Duration(to) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http_request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http_request: read response: %w", err)
	}

	// Collect response headers
	respHeaders := make(map[string]string)
	for k, v := range resp.Header {
		if len(v) > 0 {
			respHeaders[k] = v[0]
		}
	}

	return httpResponse{
		StatusCode: resp.StatusCode,
		Body:       string(respBody),
		Headers:    respHeaders,
	}, nil
}
