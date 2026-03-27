package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── execute tests ──────────────────────────────────────────────────────────

func TestExecute_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("invalid JSON body: %v", err)
		}
		if _, ok := payload["query"]; !ok {
			t.Fatal("missing 'query' in request body")
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"data":{"hello":"world"}}`))
	}))
	defer srv.Close()

	gt := &GraphQLTool{Endpoint: srv.URL}
	result, err := gt.execute(context.Background(), map[string]any{"query": "{ hello }"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}
	data, _ := m["data"].(map[string]any)
	if data["hello"] != "world" {
		t.Errorf("expected hello=world, got %v", data["hello"])
	}
}

func TestExecute_WithVariables(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		json.Unmarshal(body, &payload)
		if _, ok := payload["variables"]; !ok {
			t.Error("expected 'variables' in request body")
		}
		w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	gt := &GraphQLTool{Endpoint: srv.URL}
	_, err := gt.execute(context.Background(), map[string]any{
		"query":     "query($id: ID!) { user(id: $id) { name } }",
		"variables": map[string]any{"id": "123"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_CustomHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("expected Authorization header 'Bearer tok', got %q", got)
		}
		if got := r.Header.Get("X-Custom"); got != "val" {
			t.Errorf("expected X-Custom header 'val', got %q", got)
		}
		w.Write([]byte(`{"data":{}}`))
	}))
	defer srv.Close()

	gt := &GraphQLTool{
		Endpoint: srv.URL,
		Headers:  map[string]string{"Authorization": "Bearer tok", "X-Custom": "val"},
	}
	_, err := gt.execute(context.Background(), map[string]any{"query": "{ x }"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_EmptyQuery(t *testing.T) {
	gt := &GraphQLTool{Endpoint: "http://localhost"}
	_, err := gt.execute(context.Background(), map[string]any{"query": ""})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
	if !strings.Contains(err.Error(), "'query' argument must be a non-empty string") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecute_MissingQuery(t *testing.T) {
	gt := &GraphQLTool{Endpoint: "http://localhost"}
	_, err := gt.execute(context.Background(), map[string]any{"query": 123})
	if err == nil {
		t.Fatal("expected error for non-string query")
	}
}

func TestExecute_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	gt := &GraphQLTool{Endpoint: srv.URL}
	_, err := gt.execute(context.Background(), map[string]any{"query": "{ x }"})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if !strings.Contains(err.Error(), "server returned 500") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestExecute_NonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	gt := &GraphQLTool{Endpoint: srv.URL}
	result, err := gt.execute(context.Background(), map[string]any{"query": "{ x }"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s, ok := result.(string); !ok || s != "not json" {
		t.Errorf("expected string 'not json', got %v (%T)", result, result)
	}
}

func TestExecute_NetworkError(t *testing.T) {
	gt := &GraphQLTool{Endpoint: "http://127.0.0.1:1"}
	_, err := gt.execute(context.Background(), map[string]any{"query": "{ x }"})
	if err == nil {
		t.Fatal("expected error for unreachable endpoint")
	}
	if !strings.Contains(err.Error(), "HTTP request") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── AsTools and toolName tests ─────────────────────────────────────────────

func TestAsTools_ReturnsSixTools(t *testing.T) {
	gt := &GraphQLTool{Endpoint: "http://localhost"}
	tools := gt.AsTools()
	if len(tools) != 6 {
		t.Fatalf("expected 6 tools, got %d", len(tools))
	}
	expected := map[string]bool{
		"graphql_query":     true,
		"graphql_mutation":  true,
		"graphql_queries":   true,
		"graphql_mutations": true,
		"graphql_types":     true,
		"graphql_type":      true,
	}
	for _, tool := range tools {
		if !expected[tool.Name] {
			t.Errorf("unexpected tool name: %s", tool.Name)
		}
		delete(expected, tool.Name)
	}
	for name := range expected {
		t.Errorf("missing tool: %s", name)
	}
}

func TestToolName_WithSuffix(t *testing.T) {
	gt := &GraphQLTool{Name: "sheets"}
	if got := gt.toolName("graphql_query"); got != "graphql_query_sheets" {
		t.Errorf("expected graphql_query_sheets, got %s", got)
	}
}

func TestToolName_WithoutSuffix(t *testing.T) {
	gt := &GraphQLTool{}
	if got := gt.toolName("graphql_query"); got != "graphql_query" {
		t.Errorf("expected graphql_query, got %s", got)
	}
}

func TestAsTools_NamedEndpoint(t *testing.T) {
	gt := &GraphQLTool{Endpoint: "http://localhost", Name: "sheets"}
	tools := gt.AsTools()
	for _, tool := range tools {
		if !strings.HasSuffix(tool.Name, "_sheets") {
			t.Errorf("expected _sheets suffix on %s", tool.Name)
		}
	}
}

// ── Introspection tool tests ───────────────────────────────────────────────

// newIntrospectionServer returns an httptest.Server that responds to introspection
// queries with a canned schema containing queries, mutations, and types.
func newIntrospectionServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload map[string]any
		json.Unmarshal(body, &payload)
		query, _ := payload["query"].(string)

		switch {
		case strings.Contains(query, "__type"):
			// Describe a single type
			w.Write([]byte(`{"data":{"__type":{
				"kind":"OBJECT","name":"User","description":"A user account",
				"fields":[
					{"name":"id","description":"","type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"ID","ofType":null}},"args":[]},
					{"name":"name","description":"Full name","type":{"kind":"SCALAR","name":"String","ofType":null},"args":[]},
					{"name":"orders","description":"","type":{"kind":"LIST","name":null,"ofType":{"kind":"OBJECT","name":"Order","ofType":null,
						"fields":[{"name":"id","description":"","type":{"kind":"SCALAR","name":"ID","ofType":null},"args":[]},
						           {"name":"status","description":"","type":{"kind":"ENUM","name":"OrderStatus","ofType":null,
						               "enumValues":[{"name":"PENDING","description":""},{"name":"SHIPPED","description":""}]},"args":[]}],
						"inputFields":null,"enumValues":null}},"args":[]}
				],
				"inputFields":null,"enumValues":null
			}}}`))
		case strings.Contains(query, "queryType"):
			w.Write([]byte(`{"data":{"__schema":{"queryType":{"fields":[
				{"name":"getUser","description":"Fetch a user","args":[{"name":"id","type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"ID","ofType":null}}}],"type":{"kind":"OBJECT","name":"User","ofType":null}},
				{"name":"listOrders","description":"List orders","args":[],"type":{"kind":"LIST","name":null,"ofType":{"kind":"OBJECT","name":"Order","ofType":null}}}
			]}}}}`))
		case strings.Contains(query, "mutationType"):
			w.Write([]byte(`{"data":{"__schema":{"mutationType":{"fields":[
				{"name":"createOrder","description":"Create a new order","args":[{"name":"input","type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"INPUT_OBJECT","name":"CreateOrderInput","ofType":null}}}],"type":{"kind":"OBJECT","name":"Order","ofType":null}}
			]}}}}`))
		case strings.Contains(query, "__schema") && strings.Contains(query, "types"):
			w.Write([]byte(`{"data":{"__schema":{"types":[
				{"kind":"SCALAR","name":"String","description":""},
				{"kind":"OBJECT","name":"User","description":"A user account"},
				{"kind":"OBJECT","name":"Order","description":"An order"},
				{"kind":"ENUM","name":"OrderStatus","description":"Order status enum"},
				{"kind":"INPUT_OBJECT","name":"CreateOrderInput","description":""},
				{"kind":"OBJECT","name":"__Schema","description":"introspection"}
			]}}}`))
		default:
			w.Write([]byte(`{"data":{}}`))
		}
	}))
}

func TestQueriesListTool(t *testing.T) {
	srv := newIntrospectionServer(t)
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.queriesListTool()

	result, err := tool.Fn(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s, ok := result.(string)
	if !ok {
		t.Fatalf("expected string, got %T", result)
	}
	if !strings.Contains(s, "getUser") {
		t.Errorf("expected getUser in output, got:\n%s", s)
	}
	if !strings.Contains(s, "listOrders") {
		t.Errorf("expected listOrders in output, got:\n%s", s)
	}
}

func TestQueriesListTool_Filter(t *testing.T) {
	srv := newIntrospectionServer(t)
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.queriesListTool()

	result, err := tool.Fn(context.Background(), map[string]any{"filter": "user"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := result.(string)
	if !strings.Contains(s, "getUser") {
		t.Error("expected getUser after filter")
	}
	if strings.Contains(s, "listOrders") {
		t.Error("listOrders should be filtered out")
	}
}

func TestQueriesListTool_WithDescription(t *testing.T) {
	srv := newIntrospectionServer(t)
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.queriesListTool()

	result, err := tool.Fn(context.Background(), map[string]any{"withDescription": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := result.(string)
	if !strings.Contains(s, "# Fetch a user") {
		t.Errorf("expected description comment, got:\n%s", s)
	}
}

func TestMutationsListTool(t *testing.T) {
	srv := newIntrospectionServer(t)
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.mutationsListTool()

	result, err := tool.Fn(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := result.(string)
	if !strings.Contains(s, "createOrder") {
		t.Errorf("expected createOrder in output, got:\n%s", s)
	}
}

func TestMutationsListTool_NullMutationType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"__schema":{"mutationType":null}}}`))
	}))
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.mutationsListTool()

	result, err := tool.Fn(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := result.(string)
	if s != "" {
		t.Errorf("expected empty string for null mutation type, got %q", s)
	}
}

func TestTypesListTool(t *testing.T) {
	srv := newIntrospectionServer(t)
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.typesListTool()

	result, err := tool.Fn(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := result.(string)
	if !strings.Contains(s, "User (object)") {
		t.Errorf("expected User (object), got:\n%s", s)
	}
	if !strings.Contains(s, "OrderStatus (enum)") {
		t.Errorf("expected OrderStatus (enum), got:\n%s", s)
	}
	if strings.Contains(s, "__Schema") {
		t.Error("__Schema should be excluded")
	}
	if strings.Contains(s, "String") {
		t.Error("scalar String should be excluded")
	}
}

func TestTypesListTool_Filter(t *testing.T) {
	srv := newIntrospectionServer(t)
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.typesListTool()

	result, err := tool.Fn(context.Background(), map[string]any{"filter": "order"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := result.(string)
	if !strings.Contains(s, "Order") {
		t.Error("expected Order in filtered output")
	}
	if strings.Contains(s, "User") {
		t.Error("User should be filtered out")
	}
}

func TestTypeDescribeTool(t *testing.T) {
	srv := newIntrospectionServer(t)
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.typeDescribeTool()

	result, err := tool.Fn(context.Background(), map[string]any{"name": "User"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := result.(string)
	if !strings.Contains(s, "type User {") {
		t.Errorf("expected 'type User {' in output, got:\n%s", s)
	}
	if !strings.Contains(s, "name") {
		t.Errorf("expected field 'name' in output, got:\n%s", s)
	}
}

func TestTypeDescribeTool_MissingName(t *testing.T) {
	gt := &GraphQLTool{Endpoint: "http://localhost"}
	tool := gt.typeDescribeTool()
	_, err := tool.Fn(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing name")
	}
	if !strings.Contains(err.Error(), "'name' is required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTypeDescribeTool_TypeNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":{"__type":null}}`))
	}))
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	tool := gt.typeDescribeTool()

	result, err := tool.Fn(context.Background(), map[string]any{"name": "Nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "(type not found)" {
		t.Errorf("expected '(type not found)', got %v", result)
	}
}

func TestRunIntrospection_NonMapResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`"just a string"`))
	}))
	defer srv.Close()
	gt := &GraphQLTool{Endpoint: srv.URL}
	_, err := gt.runIntrospection(context.Background(), "{ __schema { types { name } } }")
	if err == nil {
		t.Fatal("expected error for non-map response")
	}
	if !strings.Contains(err.Error(), "unexpected introspection response type") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ── Formatting helper tests ────────────────────────────────────────────────

func TestFormatTypeRef(t *testing.T) {
	tests := []struct {
		name string
		ref  *gqlTypeRef
		want string
	}{
		{"nil", nil, "?"},
		{"scalar", &gqlTypeRef{Kind: "SCALAR", Name: "String"}, "String"},
		{"object", &gqlTypeRef{Kind: "OBJECT", Name: "User"}, "User"},
		{"non_null", &gqlTypeRef{Kind: "NON_NULL", OfType: &gqlTypeRef{Kind: "SCALAR", Name: "String"}}, "String!"},
		{"list", &gqlTypeRef{Kind: "LIST", OfType: &gqlTypeRef{Kind: "SCALAR", Name: "Int"}}, "[Int]"},
		{"non_null_list", &gqlTypeRef{Kind: "NON_NULL", OfType: &gqlTypeRef{Kind: "LIST", OfType: &gqlTypeRef{Kind: "OBJECT", Name: "Order"}}}, "[Order]!"},
		{"no_name", &gqlTypeRef{Kind: "OBJECT"}, "?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatTypeRef(tt.ref)
			if got != tt.want {
				t.Errorf("formatTypeRef() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTypeRefFrom(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if typeRefFrom(nil) != nil {
			t.Error("expected nil for nil input")
		}
	})
	t.Run("non-map", func(t *testing.T) {
		if typeRefFrom("not a map") != nil {
			t.Error("expected nil for non-map input")
		}
	})
	t.Run("nested", func(t *testing.T) {
		m := map[string]any{
			"kind": "NON_NULL",
			"name": nil,
			"ofType": map[string]any{
				"kind":   "SCALAR",
				"name":   "String",
				"ofType": nil,
			},
		}
		tr := typeRefFrom(m)
		if tr == nil {
			t.Fatal("expected non-nil")
		}
		if tr.Kind != "NON_NULL" {
			t.Errorf("expected NON_NULL, got %s", tr.Kind)
		}
		if tr.OfType == nil || tr.OfType.Name != "String" {
			t.Errorf("expected nested String, got %+v", tr.OfType)
		}
	})
}

func TestStrVal(t *testing.T) {
	if got := strVal("hello"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if got := strVal(42); got != "" {
		t.Errorf("expected '', got %q", got)
	}
	if got := strVal(nil); got != "" {
		t.Errorf("expected '', got %q", got)
	}
}

func TestExtractFields(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		data := map[string]any{
			"__schema": map[string]any{
				"queryType": map[string]any{
					"fields": []any{
						map[string]any{"name": "getUser"},
						map[string]any{"name": "listOrders"},
					},
				},
			},
		}
		fields := extractFields(data, "queryType")
		if len(fields) != 2 {
			t.Fatalf("expected 2 fields, got %d", len(fields))
		}
	})
	t.Run("nil_data", func(t *testing.T) {
		if extractFields(nil, "queryType") != nil {
			t.Error("expected nil for nil data")
		}
	})
	t.Run("missing_schema", func(t *testing.T) {
		if extractFields(map[string]any{}, "queryType") != nil {
			t.Error("expected nil for missing schema")
		}
	})
	t.Run("missing_type", func(t *testing.T) {
		data := map[string]any{"__schema": map[string]any{}}
		if extractFields(data, "queryType") != nil {
			t.Error("expected nil for missing queryType")
		}
	})
}

func TestFormatFieldList(t *testing.T) {
	fields := []map[string]any{
		{
			"name": "getUser",
			"args": []any{
				map[string]any{"name": "id", "type": map[string]any{"kind": "SCALAR", "name": "ID"}},
			},
			"type":        map[string]any{"kind": "OBJECT", "name": "User"},
			"description": "Fetch user",
		},
		{
			"name":        "listOrders",
			"args":        []any{},
			"type":        map[string]any{"kind": "LIST", "name": nil, "ofType": map[string]any{"kind": "OBJECT", "name": "Order"}},
			"description": "",
		},
	}

	t.Run("no_filter", func(t *testing.T) {
		s := formatFieldList(fields, "", false)
		if !strings.Contains(s, "getUser") || !strings.Contains(s, "listOrders") {
			t.Errorf("expected both fields:\n%s", s)
		}
	})
	t.Run("filter", func(t *testing.T) {
		s := formatFieldList(fields, "user", false)
		if !strings.Contains(s, "getUser") {
			t.Error("expected getUser")
		}
		if strings.Contains(s, "listOrders") {
			t.Error("listOrders should be filtered out")
		}
	})
	t.Run("case_insensitive_filter", func(t *testing.T) {
		s := formatFieldList(fields, "USER", false)
		if !strings.Contains(s, "getUser") {
			t.Error("expected case-insensitive match")
		}
	})
	t.Run("with_description", func(t *testing.T) {
		s := formatFieldList(fields, "", true)
		if !strings.Contains(s, "# Fetch user") {
			t.Errorf("expected description comment:\n%s", s)
		}
	})
	t.Run("args_format", func(t *testing.T) {
		s := formatFieldList(fields, "getUser", false)
		if !strings.Contains(s, "(id: ID)") {
			t.Errorf("expected arg format, got:\n%s", s)
		}
	})
}

func TestFormatTypesList(t *testing.T) {
	data := map[string]any{
		"__schema": map[string]any{
			"types": []any{
				map[string]any{"kind": "SCALAR", "name": "String", "description": ""},
				map[string]any{"kind": "OBJECT", "name": "User", "description": "A user"},
				map[string]any{"kind": "ENUM", "name": "Status", "description": ""},
				map[string]any{"kind": "OBJECT", "name": "__Schema", "description": ""},
			},
		},
	}

	t.Run("filters_builtins", func(t *testing.T) {
		s := formatTypesList(data, "", false)
		if strings.Contains(s, "String") {
			t.Error("scalar should be excluded")
		}
		if strings.Contains(s, "__Schema") {
			t.Error("__ prefix should be excluded")
		}
		if !strings.Contains(s, "User (object)") {
			t.Error("expected User (object)")
		}
		if !strings.Contains(s, "Status (enum)") {
			t.Error("expected Status (enum)")
		}
	})
	t.Run("with_description", func(t *testing.T) {
		s := formatTypesList(data, "", true)
		if !strings.Contains(s, "# A user") {
			t.Errorf("expected description comment:\n%s", s)
		}
	})
	t.Run("nil_data", func(t *testing.T) {
		if got := formatTypesList(nil, "", false); got != "(no data)" {
			t.Errorf("expected '(no data)', got %q", got)
		}
	})
	t.Run("nil_schema", func(t *testing.T) {
		if got := formatTypesList(map[string]any{}, "", false); got != "(no schema)" {
			t.Errorf("expected '(no schema)', got %q", got)
		}
	})
}

func TestFormatTypeDef(t *testing.T) {
	t.Run("object", func(t *testing.T) {
		data := map[string]any{
			"__type": map[string]any{
				"kind": "OBJECT", "name": "User",
				"fields": []any{
					map[string]any{"name": "id", "type": map[string]any{"kind": "SCALAR", "name": "ID"}, "args": []any{}},
					map[string]any{"name": "name", "type": map[string]any{"kind": "SCALAR", "name": "String"}, "args": []any{}},
				},
			},
		}
		s := formatTypeDef(data)
		if !strings.Contains(s, "type User {") {
			t.Errorf("expected 'type User {', got:\n%s", s)
		}
	})
	t.Run("enum", func(t *testing.T) {
		data := map[string]any{
			"__type": map[string]any{
				"kind": "ENUM", "name": "Status",
				"enumValues": []any{
					map[string]any{"name": "ACTIVE"},
					map[string]any{"name": "INACTIVE"},
				},
			},
		}
		s := formatTypeDef(data)
		if !strings.Contains(s, "enum Status { ACTIVE INACTIVE }") {
			t.Errorf("unexpected enum format:\n%s", s)
		}
	})
	t.Run("input_object", func(t *testing.T) {
		data := map[string]any{
			"__type": map[string]any{
				"kind": "INPUT_OBJECT", "name": "CreateInput",
				"inputFields": []any{
					map[string]any{"name": "name", "type": map[string]any{"kind": "SCALAR", "name": "String"}},
				},
			},
		}
		s := formatTypeDef(data)
		if !strings.Contains(s, "input CreateInput {") {
			t.Errorf("expected 'input CreateInput {', got:\n%s", s)
		}
	})
	t.Run("with_description", func(t *testing.T) {
		data := map[string]any{
			"__type": map[string]any{
				"kind": "OBJECT", "name": "User", "description": "A user account",
				"fields": []any{},
			},
		}
		s := formatTypeDef(data)
		if !strings.HasPrefix(s, "# A user account") {
			t.Errorf("expected description prefix, got:\n%s", s)
		}
	})
	t.Run("nil_data", func(t *testing.T) {
		if got := formatTypeDef(nil); got != "(no data)" {
			t.Errorf("expected '(no data)', got %q", got)
		}
	})
	t.Run("nil_type", func(t *testing.T) {
		if got := formatTypeDef(map[string]any{}); got != "(type not found)" {
			t.Errorf("expected '(type not found)', got %q", got)
		}
	})
}

func TestFormatTypeDefDeep(t *testing.T) {
	t.Run("expands_nested", func(t *testing.T) {
		data := map[string]any{
			"__type": map[string]any{
				"kind": "OBJECT", "name": "User",
				"fields": []any{
					map[string]any{
						"name": "orders",
						"type": map[string]any{
							"kind": "LIST", "ofType": map[string]any{
								"kind": "OBJECT", "name": "Order",
								"fields": []any{
									map[string]any{"name": "id", "type": map[string]any{"kind": "SCALAR", "name": "ID"}},
									map[string]any{
										"name": "status",
										"type": map[string]any{
											"kind":       "ENUM",
											"name":       "OrderStatus",
											"enumValues": []any{map[string]any{"name": "PENDING"}, map[string]any{"name": "SHIPPED"}},
										},
									},
								},
							},
						},
						"args": []any{},
					},
				},
			},
		}
		s := formatTypeDefDeep(data)
		if !strings.Contains(s, "type User {") {
			t.Error("expected User block")
		}
		if !strings.Contains(s, "type Order {") {
			t.Error("expected Order block (level 1)")
		}
		if !strings.Contains(s, "enum OrderStatus") {
			t.Error("expected OrderStatus block (level 2)")
		}
	})
	t.Run("deduplicates", func(t *testing.T) {
		data := map[string]any{
			"__type": map[string]any{
				"kind": "OBJECT", "name": "Root",
				"fields": []any{
					map[string]any{"name": "a", "type": map[string]any{"kind": "OBJECT", "name": "Child", "fields": []any{}}, "args": []any{}},
					map[string]any{"name": "b", "type": map[string]any{"kind": "OBJECT", "name": "Child", "fields": []any{}}, "args": []any{}},
				},
			},
		}
		s := formatTypeDefDeep(data)
		count := strings.Count(s, "type Child {")
		if count != 1 {
			t.Errorf("expected Child to appear once, appeared %d times:\n%s", count, s)
		}
	})
	t.Run("nil_data", func(t *testing.T) {
		if got := formatTypeDefDeep(nil); got != "(no data)" {
			t.Errorf("expected '(no data)', got %q", got)
		}
	})
	t.Run("nil_type", func(t *testing.T) {
		if got := formatTypeDefDeep(map[string]any{"__type": nil}); got != "(type not found)" {
			t.Errorf("expected '(type not found)', got %q", got)
		}
	})
}

func TestNamedTypeObj(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if namedTypeObj(nil) != nil {
			t.Error("expected nil")
		}
	})
	t.Run("scalar_returns_nil", func(t *testing.T) {
		if namedTypeObj(map[string]any{"kind": "SCALAR", "name": "String"}) != nil {
			t.Error("expected nil for scalar")
		}
	})
	t.Run("object", func(t *testing.T) {
		m := map[string]any{"kind": "OBJECT", "name": "User"}
		got := namedTypeObj(m)
		if got == nil || strVal(got["name"]) != "User" {
			t.Error("expected same map for object")
		}
	})
	t.Run("unwraps_non_null", func(t *testing.T) {
		inner := map[string]any{"kind": "OBJECT", "name": "User"}
		m := map[string]any{"kind": "NON_NULL", "ofType": inner}
		got := namedTypeObj(m)
		if got == nil || strVal(got["name"]) != "User" {
			t.Errorf("expected inner object, got %v", got)
		}
	})
	t.Run("unwraps_list", func(t *testing.T) {
		inner := map[string]any{"kind": "ENUM", "name": "Status"}
		m := map[string]any{"kind": "LIST", "ofType": inner}
		got := namedTypeObj(m)
		if got == nil || strVal(got["name"]) != "Status" {
			t.Errorf("expected inner enum, got %v", got)
		}
	})
	t.Run("non_null_list_scalar", func(t *testing.T) {
		m := map[string]any{
			"kind": "NON_NULL",
			"ofType": map[string]any{
				"kind": "LIST",
				"ofType": map[string]any{
					"kind": "SCALAR", "name": "String",
				},
			},
		}
		if namedTypeObj(m) != nil {
			t.Error("expected nil for wrapped scalar")
		}
	})
}

func TestRenderGroupedFields(t *testing.T) {
	fields := []any{
		map[string]any{"name": "id", "type": map[string]any{"kind": "SCALAR", "name": "ID"}, "args": []any{}},
		map[string]any{"name": "name", "type": map[string]any{"kind": "SCALAR", "name": "String"}, "args": []any{}},
		map[string]any{"name": "email", "type": map[string]any{"kind": "SCALAR", "name": "String"}, "args": []any{}},
		map[string]any{
			"name": "orders",
			"type": map[string]any{"kind": "LIST", "name": nil, "ofType": map[string]any{"kind": "OBJECT", "name": "Order"}},
			"args": []any{
				map[string]any{"name": "limit", "type": map[string]any{"kind": "SCALAR", "name": "Int"}},
			},
		},
	}
	s := renderGroupedFields(fields)
	// String fields should be grouped
	if !strings.Contains(s, "email name: String") && !strings.Contains(s, "name email: String") {
		t.Errorf("expected String fields grouped, got:\n%s", s)
	}
	// Field with args should be standalone
	if !strings.Contains(s, "orders(limit: Int)") {
		t.Errorf("expected orders with args, got:\n%s", s)
	}
}

func TestAllFieldMaps(t *testing.T) {
	typeObj := map[string]any{
		"fields":      []any{map[string]any{"name": "id"}, map[string]any{"name": "name"}},
		"inputFields": []any{map[string]any{"name": "email"}},
	}
	result := allFieldMaps(typeObj)
	if len(result) != 3 {
		t.Errorf("expected 3 field maps, got %d", len(result))
	}
}
