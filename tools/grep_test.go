package tools

import (
	"context"
	"testing"
)

func TestGrepTool_MatchSingleLine(t *testing.T) {
	text := "error: something went wrong\ninfo: all is well"

	result, err := grepExecute(context.Background(), map[string]any{
		"text":    text,
		"pattern": "error",
	})
	if err != nil {
		t.Fatalf("grepExecute failed: %v", err)
	}

	resultStr := result.(string)
	if resultStr != "error: something went wrong\n" {
		t.Errorf("expected 'error: something went wrong\\n', got %q", resultStr)
	}
}

func TestGrepTool_MatchMultipleLines(t *testing.T) {
	text := "[ERROR] failed\n[WARN] deprecated\n[INFO] ok\n[ERROR] failed again"

	result, err := grepExecute(context.Background(), map[string]any{
		"text":    text,
		"pattern": "ERROR",
	})
	if err != nil {
		t.Fatalf("grepExecute failed: %v", err)
	}

	resultStr := result.(string)
	if resultStr == "" {
		t.Errorf("expected matches, got empty string")
	}
}

func TestGrepTool_NoMatches(t *testing.T) {
	text := "line 1\nline 2\nline 3"

	result, err := grepExecute(context.Background(), map[string]any{
		"text":    text,
		"pattern": "notfound",
	})
	if err != nil {
		t.Fatalf("grepExecute failed: %v", err)
	}

	resultStr := result.(string)
	if resultStr != "" {
		t.Errorf("expected empty string for no matches, got %q", resultStr)
	}
}

func TestGrepTool_RegexPattern(t *testing.T) {
	text := "2024-01-15 error\n2024-01-16 warning\n2024-01-17 error"

	result, err := grepExecute(context.Background(), map[string]any{
		"text":    text,
		"pattern": "2024-01-1[56]",
	})
	if err != nil {
		t.Fatalf("grepExecute failed: %v", err)
	}

	resultStr := result.(string)
	if resultStr == "" {
		t.Errorf("expected regex matches, got empty string")
	}
}

func TestGrepTool_CaseInsensitiveFlag(t *testing.T) {
	text := "Error: something\nerror: another\nERROR: third"

	result, err := grepExecute(context.Background(), map[string]any{
		"text":    text,
		"pattern": "error",
		"flags":   "-i",
	})
	if err != nil {
		t.Fatalf("grepExecute failed: %v", err)
	}

	resultStr := result.(string)
	// With -i, all three lines should match
	if resultStr == "" {
		t.Errorf("expected case-insensitive matches, got empty string")
	}
}

func TestGrepTool_MissingPattern(t *testing.T) {
	_, err := grepExecute(context.Background(), map[string]any{
		"text": "some text",
	})
	if err == nil {
		t.Errorf("expected error when pattern is missing")
	}
}

func TestGrepTool_EmptyText(t *testing.T) {
	result, err := grepExecute(context.Background(), map[string]any{
		"text":    "",
		"pattern": "anything",
	})
	if err != nil {
		t.Fatalf("grepExecute failed: %v", err)
	}

	resultStr := result.(string)
	if resultStr != "" {
		t.Errorf("expected empty result for empty text, got %q", resultStr)
	}
}

func TestGrepTool_AsTools(t *testing.T) {
	tools := GrepTool{}.AsTools()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "grep" {
		t.Errorf("expected tool name 'grep', got %s", tools[0].Name)
	}
}
