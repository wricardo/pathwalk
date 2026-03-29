package tools

import (
	"context"
	"testing"
)

func TestJqTool_ExtractField(t *testing.T) {
	data := map[string]any{
		"user": map[string]any{
			"email": "john@example.com",
			"age":   30,
		},
	}

	result, err := jqExecute(context.Background(), map[string]any{
		"data":   data,
		"filter": ".user.email",
	})
	if err != nil {
		t.Fatalf("jqExecute failed: %v", err)
	}

	if result != "john@example.com" {
		t.Errorf("expected 'john@example.com', got %v", result)
	}
}

func TestJqTool_FilterArray(t *testing.T) {
	data := []any{
		map[string]any{"id": 1, "name": "Alice"},
		map[string]any{"id": 2, "name": "Bob"},
		map[string]any{"id": 3, "name": "Charlie"},
	}

	result, err := jqExecute(context.Background(), map[string]any{
		"data":   data,
		"filter": ".[] | .name",
	})
	if err != nil {
		t.Fatalf("jqExecute failed: %v", err)
	}

	// jq outputs multiple values separated by newlines
	if result == nil {
		t.Errorf("expected result, got nil")
	}
}

func TestJqTool_InvalidFilter(t *testing.T) {
	data := map[string]any{"key": "value"}

	_, err := jqExecute(context.Background(), map[string]any{
		"data":   data,
		"filter": ".invalid(",
	})
	if err == nil {
		t.Errorf("expected error for invalid filter, got nil")
	}
}

func TestJqTool_MissingFilter(t *testing.T) {
	_, err := jqExecute(context.Background(), map[string]any{
		"data": map[string]any{"key": "value"},
	})
	if err == nil {
		t.Errorf("expected error when filter is missing")
	}
}

func TestJqTool_AsTools(t *testing.T) {
	tools := JqTool{}.AsTools()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "jq" {
		t.Errorf("expected tool name 'jq', got %s", tools[0].Name)
	}
}

func TestJqTool_ParseJSON(t *testing.T) {
	data := map[string]any{
		"numbers": []any{1, 2, 3},
	}

	result, err := jqExecute(context.Background(), map[string]any{
		"data":   data,
		"filter": ".numbers",
	})
	if err != nil {
		t.Fatalf("jqExecute failed: %v", err)
	}

	// jq should return the array as JSON
	if result == nil {
		t.Errorf("expected result, got nil")
	}
}
