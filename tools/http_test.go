package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPTool_GET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer srv.Close()

	tool := HTTPTool{}
	result, err := tool.execute(context.Background(), map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	resp := result.(httpResponse)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
	if resp.Body != `{"status": "ok"}` {
		t.Errorf("unexpected body: %s", resp.Body)
	}
}

func TestHTTPTool_POST_WithBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"name": "test"}` {
			t.Errorf("unexpected body: %s", string(body))
		}

		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id": 123}`))
	}))
	defer srv.Close()

	tool := HTTPTool{}
	result, err := tool.execute(context.Background(), map[string]any{
		"url":    srv.URL,
		"method": "POST",
		"body":   `{"name": "test"}`,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	resp := result.(httpResponse)
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}
}

func TestHTTPTool_CustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom-Header") != "custom-value" {
			t.Errorf("custom header not found")
		}
		if r.Header.Get("Authorization") != "Bearer token123" {
			t.Errorf("authorization header not found")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tool := HTTPTool{}
	result, err := tool.execute(context.Background(), map[string]any{
		"url": srv.URL,
		"headers": map[string]any{
			"X-Custom-Header": "custom-value",
			"Authorization":   "Bearer token123",
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	resp := result.(httpResponse)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestHTTPTool_DefaultHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Default") != "default-value" {
			t.Errorf("default header not found")
		}
		if r.Header.Get("X-Override") != "overridden" {
			t.Errorf("header override failed, expected 'overridden'")
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tool := HTTPTool{
		DefaultHeaders: map[string]string{
			"X-Default":  "default-value",
			"X-Override": "original",
		},
	}
	result, err := tool.execute(context.Background(), map[string]any{
		"url": srv.URL,
		"headers": map[string]any{
			"X-Override": "overridden",
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	resp := result.(httpResponse)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestHTTPTool_ResponseHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Response-Header", "response-value")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	tool := HTTPTool{}
	result, err := tool.execute(context.Background(), map[string]any{
		"url": srv.URL,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	resp := result.(httpResponse)
	if resp.Headers["X-Response-Header"] != "response-value" {
		t.Errorf("response header not captured")
	}
}

func TestHTTPTool_BodyAsObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		// Should be JSON-encoded
		var data map[string]any
		if err := json.Unmarshal(body, &data); err != nil {
			t.Errorf("body should be valid JSON: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tool := HTTPTool{}
	_, err := tool.execute(context.Background(), map[string]any{
		"url":    srv.URL,
		"method": "POST",
		"body": map[string]any{
			"key": "value",
			"num": 42,
		},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
}

func TestHTTPTool_MissingURL(t *testing.T) {
	tool := HTTPTool{}
	_, err := tool.execute(context.Background(), map[string]any{})
	if err == nil {
		t.Errorf("expected error for missing URL")
	}
}

func TestHTTPTool_AsTools(t *testing.T) {
	tool := HTTPTool{}
	tools := tool.AsTools()
	if len(tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "http_request" {
		t.Errorf("expected tool name 'http_request', got %s", tools[0].Name)
	}
}
