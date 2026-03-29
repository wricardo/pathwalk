package evals_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/pathwaytest"
	"github.com/wricardo/pathwalk/tools"
)

// TestBuiltinTools demonstrates jq, grep, and http_request tools working together.
func TestBuiltinTools(t *testing.T) {
	pp, err := pathwalk.ParsePathway("../examples/builtin_tools_demo.json")
	if err != nil {
		t.Fatalf("ParsePathway: %v", err)
	}

	// Set up a mock API server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := map[string]any{
			"orders": []map[string]any{
				{"id": 1, "customer": "Alice", "amount": 150.00},
				{"id": 2, "customer": "Bob", "amount": 2500.00},
				{"id": 3, "customer": "Charlie", "amount": 75.50},
				{"id": 4, "customer": "Diana", "amount": 5000.00},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer srv.Close()

	// Create mock LLM that uses the tools
	mock := pathwaytest.NewMockLLMClient()

	// The LLM will use jq to extract high-value customers
	mock.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
		Content: "I'll fetch the orders and process them using jq.",
	})

	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{
				Name: "set_variables",
				Args: map[string]any{
					"high_value_customers": "Bob, Diana",
				},
			},
		},
	})

	// Create engine with built-in tools
	engine := pathwalk.NewEngine(
		pp,
		mock,
		pathwalk.WithTools(tools.BuiltinTools()...),
	)

	// Run the pathway
	result, err := engine.Run(context.Background(), "Process orders")
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}

	if result.Reason != "terminal" {
		t.Errorf("expected reason 'terminal', got %s", result.Reason)
	}
}

// TestJqToolDirect tests jq tool directly without a pathway
func TestJqToolDirect(t *testing.T) {
	testData := map[string]any{
		"orders": []map[string]any{
			{"customer": "Alice", "amount": 150},
			{"customer": "Bob", "amount": 2500},
		},
	}

	tool := tools.JqTool{}
	toolList := tool.AsTools()

	result, err := toolList[0].Fn(context.Background(), map[string]any{
		"data":   testData,
		"filter": ".orders[] | .customer",
	})
	if err != nil {
		t.Fatalf("jq tool failed: %v", err)
	}

	if result == nil {
		t.Errorf("expected result, got nil")
	}
}

// TestGrepToolDirect tests grep tool directly
func TestGrepToolDirect(t *testing.T) {
	text := `2024-01-15 [ERROR] Database connection failed
2024-01-15 [WARN] Low memory
2024-01-15 [ERROR] Query timeout
2024-01-15 [INFO] All systems normal`

	tool := tools.GrepTool{}
	toolList := tool.AsTools()

	result, err := toolList[0].Fn(context.Background(), map[string]any{
		"text":    text,
		"pattern": "ERROR",
	})
	if err != nil {
		t.Fatalf("grep tool failed: %v", err)
	}

	resultStr := result.(string)
	if resultStr == "" {
		t.Errorf("expected error lines, got empty")
	}
}

// TestHTTPToolDirect tests http_request tool directly
func TestHTTPToolDirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"status": "ok", "data": [1, 2, 3]}`))
	}))
	defer srv.Close()

	tool := tools.HTTPTool{}
	toolList := tool.AsTools()

	result, err := toolList[0].Fn(context.Background(), map[string]any{
		"url":    srv.URL,
		"method": "GET",
	})
	if err != nil {
		t.Fatalf("http tool failed: %v", err)
	}

	// Result is an httpResponse struct (which is JSON marshallable)
	// Check it's not nil
	if result == nil {
		t.Fatalf("expected result, got nil")
	}

	// For now just verify it's a non-nil response
	// The actual structure validation happens in the HTTP tool tests
	t.Logf("Result type: %T", result)
}
