package pathwalk

// Internal tests for unexported functions in executor.go and router.go.
// Uses package pathwalk (not _test) to access unexported symbols.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubLLM is a minimal LLMClient used only in internal executor tests.
type stubLLM struct {
	resp *CompletionResponse
	err  error
}

func (s *stubLLM) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.resp == nil {
		return &CompletionResponse{}, nil
	}
	return s.resp, nil
}

func TestParseChannelDirective(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantTool     string
		wantArgKey   string
		wantArgValue string
		wantOK       bool
	}{
		{
			name:         "graphql mutation",
			input:        `<|channel|>commentary to=graphql <|constrain|>json<|message|>{"query":"mutation { createOrder }"}`,
			wantTool:     "graphql",
			wantArgKey:   "query",
			wantArgValue: "mutation { createOrder }",
			wantOK:       true,
		},
		{
			name:         "custom tool name",
			input:        `<|channel|>to=my_tool<|message|>{"key":"val"}`,
			wantTool:     "my_tool",
			wantArgKey:   "key",
			wantArgValue: "val",
			wantOK:       true,
		},
		{
			name:   "missing channel tag",
			input:  `<|message|>{"query":"x"}`,
			wantOK: false,
		},
		{
			name:   "missing message tag",
			input:  `<|channel|>to=graphql {"query":"x"}`,
			wantOK: false,
		},
		{
			name:   "message before channel",
			input:  `<|message|>{"q":"x"}<|channel|>to=graphql`,
			wantOK: false,
		},
		{
			name:   "no to= attribute",
			input:  `<|channel|>commentary<|message|>{"q":"x"}`,
			wantOK: false,
		},
		{
			name:   "invalid JSON payload",
			input:  `<|channel|>to=graphql<|message|>not-json`,
			wantOK: false,
		},
		{
			name:   "plain text, no directive",
			input:  `The order has been created successfully.`,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolName, args, ok := parseChannelDirective(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if toolName != tc.wantTool {
				t.Errorf("toolName=%q, want %q", toolName, tc.wantTool)
			}
			if tc.wantArgKey != "" {
				got, exists := args[tc.wantArgKey]
				if !exists {
					t.Errorf("arg %q missing; args=%v", tc.wantArgKey, args)
				} else if got != tc.wantArgValue {
					t.Errorf("args[%q]=%q, want %q", tc.wantArgKey, got, tc.wantArgValue)
				}
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("short string: got %q", got)
	}
	if got := truncate("exact", 5); got != "exact" {
		t.Errorf("exact length: got %q", got)
	}
	if got := truncate("hello world", 5); got != "hello..." {
		t.Errorf("long string: got %q, want %q", got, "hello...")
	}
}

func TestResolveTemplate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		vars  map[string]any
		want  string
	}{
		{"no placeholders", "hello world", map[string]any{"x": "1"}, "hello world"},
		{"single var", "order {{order_id}}", map[string]any{"order_id": "42"}, "order 42"},
		{"multiple vars", "{{a}} and {{b}}", map[string]any{"a": "foo", "b": "bar"}, "foo and bar"},
		{"unknown var left as-is", "{{missing}}", map[string]any{"x": "1"}, "{{missing}}"},
		{"int var coerced to string", "count {{n}}", map[string]any{"n": 7}, "count 7"},
		{"empty vars", "{{x}}", map[string]any{}, "{{x}}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTemplate(tc.input, tc.vars)
			if got != tc.want {
				t.Errorf("resolveTemplate=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveBody(t *testing.T) {
	vars := map[string]any{"user": "Alice", "qty": 3}

	// string — template substitution
	if got := resolveBody("hello {{user}}", vars); got != "hello Alice" {
		t.Errorf("string: got %q, want %q", got, "hello Alice")
	}

	// map — recursively resolved
	m, ok := resolveBody(map[string]any{"msg": "hi {{user}}", "n": 99}, vars).(map[string]any)
	if !ok || m["msg"] != "hi Alice" || m["n"] != 99 {
		t.Errorf("map: unexpected result %v", m)
	}

	// slice — each element resolved
	s, ok := resolveBody([]any{"{{user}}", 42}, vars).([]any)
	if !ok || s[0] != "Alice" || s[1] != 42 {
		t.Errorf("slice: unexpected result %v", s)
	}

	// non-string/map/slice types pass through unchanged
	if got := resolveBody(123, vars); got != 123 {
		t.Errorf("int passthrough: got %v", got)
	}
	if got := resolveBody(nil, vars); got != nil {
		t.Errorf("nil passthrough: got %v", got)
	}
}

func TestConditionMet(t *testing.T) {
	tests := []struct {
		name string
		cond RouteCondition
		vars map[string]any
		want bool
	}{
		// is / equals
		{"is match", RouteCondition{"status", "is", "active"}, map[string]any{"status": "active"}, true},
		{"is mismatch", RouteCondition{"status", "is", "inactive"}, map[string]any{"status": "active"}, false},
		{"is case-insensitive", RouteCondition{"status", "IS", "ACTIVE"}, map[string]any{"status": "active"}, true},
		{"equals alias", RouteCondition{"x", "equals", "1"}, map[string]any{"x": "1"}, true},
		{"== alias", RouteCondition{"x", "==", "1"}, map[string]any{"x": "1"}, true},
		// is not
		{"is not mismatch→true", RouteCondition{"s", "is not", "inactive"}, map[string]any{"s": "active"}, true},
		{"is not match→false", RouteCondition{"s", "is not", "active"}, map[string]any{"s": "active"}, false},
		{"not equals alias", RouteCondition{"x", "not equals", "2"}, map[string]any{"x": "1"}, true},
		{"!= alias", RouteCondition{"x", "!=", "2"}, map[string]any{"x": "1"}, true},
		// contains
		{"contains yes", RouteCondition{"msg", "contains", "hello"}, map[string]any{"msg": "say hello world"}, true},
		{"contains no", RouteCondition{"msg", "contains", "goodbye"}, map[string]any{"msg": "say hello"}, false},
		{"contains case-insensitive", RouteCondition{"msg", "contains", "HELLO"}, map[string]any{"msg": "say hello"}, true},
		// not contains
		{"not contains yes", RouteCondition{"msg", "not contains", "bye"}, map[string]any{"msg": "say hello"}, true},
		{"not contains no", RouteCondition{"msg", "not contains", "hello"}, map[string]any{"msg": "say hello"}, false},
		// numeric comparisons
		{"greater yes", RouteCondition{"score", ">", "50"}, map[string]any{"score": "100"}, true},
		{"greater no", RouteCondition{"score", ">", "100"}, map[string]any{"score": "50"}, false},
		{"greater equal yes", RouteCondition{"score", ">=", "100"}, map[string]any{"score": "100"}, true},
		{"greater equal no", RouteCondition{"score", ">=", "101"}, map[string]any{"score": "100"}, false},
		{"less yes", RouteCondition{"score", "<", "100"}, map[string]any{"score": "50"}, true},
		{"less no", RouteCondition{"score", "<", "50"}, map[string]any{"score": "100"}, false},
		{"less equal yes", RouteCondition{"score", "<=", "50"}, map[string]any{"score": "50"}, true},
		{"less equal no", RouteCondition{"score", "<=", "49"}, map[string]any{"score": "50"}, false},
		{"non-numeric >→false", RouteCondition{"x", ">", "foo"}, map[string]any{"x": "bar"}, false},
		// missing field
		{"missing field is→false", RouteCondition{"missing", "is", "x"}, map[string]any{}, false},
		{"missing field is not→true", RouteCondition{"missing", "is not", "x"}, map[string]any{}, true},
		{"missing field not contains→true", RouteCondition{"missing", "not contains", "x"}, map[string]any{}, true},
		{"missing field contains→false", RouteCondition{"missing", "contains", "x"}, map[string]any{}, false},
		// unknown operator
		{"unknown operator→false", RouteCondition{"x", "like", "foo"}, map[string]any{"x": "foo"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := conditionMet(tc.cond, tc.vars)
			if got != tc.want {
				t.Errorf("conditionMet=%v, want %v (cond=%+v, vars=%v)", got, tc.want, tc.cond, tc.vars)
			}
		})
	}
}

func TestParseFloatPair(t *testing.T) {
	a, b, ok := parseFloatPair("3.5", "2.0")
	if !ok || a != 3.5 || b != 2.0 {
		t.Errorf("valid pair: got (%v, %v, %v)", a, b, ok)
	}
	if _, _, ok := parseFloatPair("not-a-number", "2.0"); ok {
		t.Error("expected false for non-numeric first arg")
	}
	if _, _, ok := parseFloatPair("2.0", "not-a-number"); ok {
		t.Error("expected false for non-numeric second arg")
	}
}

func TestParseSelectRoute(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]any
		maxRoutes  int
		wantRoute  int
		wantReason string
	}{
		{"float64", map[string]any{"route": float64(2)}, 3, 2, ""},
		{"int", map[string]any{"route": 1}, 3, 1, ""},
		{"string", map[string]any{"route": "3"}, 3, 3, ""},
		{"json.Number", map[string]any{"route": json.Number("2")}, 3, 2, ""},
		{"with reason", map[string]any{"route": float64(1), "reason": "best match"}, 3, 1, "best match"},
		{"out of bounds high→1", map[string]any{"route": float64(5)}, 3, 1, ""},
		{"zero→1", map[string]any{"route": float64(0)}, 3, 1, ""},
		{"missing key→1", map[string]any{}, 3, 1, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			route, reason := parseSelectRoute(tc.args, tc.maxRoutes)
			if route != tc.wantRoute {
				t.Errorf("route=%d, want %d", route, tc.wantRoute)
			}
			if reason != tc.wantReason {
				t.Errorf("reason=%q, want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestParseIntArg(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		key  string
		want int
	}{
		{"float64", map[string]any{"n": float64(5)}, "n", 5},
		{"int", map[string]any{"n": 3}, "n", 3},
		{"string", map[string]any{"n": "7"}, "n", 7},
		{"json.Number", map[string]any{"n": json.Number("9")}, "n", 9},
		{"missing key", map[string]any{}, "n", 0},
		{"unhandled type", map[string]any{"n": true}, "n", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseIntArg(tc.args, tc.key)
			if got != tc.want {
				t.Errorf("parseIntArg=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	state := newState("overall task")

	// With prompt and condition
	n := &Node{Name: "Step1", Prompt: "Do the thing.", Condition: "When done."}
	got := buildSystemPrompt(n, state)
	if !strings.Contains(got, "Step1") {
		t.Error("missing step name")
	}
	if !strings.Contains(got, "Do the thing.") {
		t.Error("missing prompt")
	}
	if !strings.Contains(got, "overall task") {
		t.Error("missing task")
	}
	if !strings.Contains(got, "When done.") {
		t.Error("missing condition")
	}

	// Without condition — no exit condition line
	n2 := &Node{Name: "Step2", Prompt: "p"}
	got2 := buildSystemPrompt(n2, state)
	if strings.Contains(got2, "Exit condition") {
		t.Error("should not include Exit condition when Condition is empty")
	}

	// Prompt empty → falls back to Text
	n3 := &Node{Name: "Step3", Text: "from text"}
	got3 := buildSystemPrompt(n3, state)
	if !strings.Contains(got3, "from text") {
		t.Error("should fall back to Text when Prompt is empty")
	}
}

func TestBuildUserMessage(t *testing.T) {
	// No vars, no steps
	minimal := newState("my task")
	got := buildUserMessage(minimal)
	if !strings.Contains(got, "my task") {
		t.Error("missing task in user message")
	}
	if strings.Contains(got, "Current variables:") {
		t.Error("should not include variables section when empty")
	}
	if strings.Contains(got, "Previous steps:") {
		t.Error("should not include steps section when empty")
	}

	// With variables
	withVars := newState("t")
	withVars.Variables["key"] = "val"
	if !strings.Contains(buildUserMessage(withVars), "Current variables:") {
		t.Error("missing variables section")
	}

	// With steps
	withSteps := newState("t")
	withSteps.Steps = []Step{{NodeName: "n1", Output: "output text"}}
	if !strings.Contains(buildUserMessage(withSteps), "Previous steps:") {
		t.Error("missing steps section")
	}
}

func TestConditionSummary(t *testing.T) {
	tests := []struct {
		name   string
		conds  []RouteCondition
		want   string
	}{
		{
			name:  "empty conditions",
			conds: []RouteCondition{},
			want:  "conditions matched",
		},
		{
			name:  "nil conditions",
			conds: nil,
			want:  "conditions matched",
		},
		{
			name: "single condition",
			conds: []RouteCondition{
				{Field: "operation_type", Operator: "is", Value: "order_mgmt"},
			},
			want: `operation_type is "order_mgmt"`,
		},
		{
			name: "multiple conditions",
			conds: []RouteCondition{
				{Field: "score", Operator: ">=", Value: "100"},
				{Field: "status", Operator: "is", Value: "active"},
			},
			want: `score >= "100" (+1)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := conditionSummary(tc.conds)
			if got != tc.want {
				t.Errorf("conditionSummary=%q, want %q", got, tc.want)
			}
		})
	}
}

// TestExecuteNodeUnsupportedType verifies that executeNode returns an error for
// an unrecognised node type. The engine's Run loop skips them before calling
// executeNode, but a direct call must not silently succeed.
func TestExecuteNodeUnsupportedType(t *testing.T) {
	node := &Node{ID: "x", Type: NodeType("Unknown"), Name: "BadNode"}
	state := newState("test")
	_, err := executeNode(context.Background(), node, state, &stubLLM{}, nil)
	if err == nil {
		t.Fatal("expected error for unsupported node type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported node type") {
		t.Errorf("unexpected error text: %v", err)
	}
}

// TestExecuteNodeExtractVarsError verifies that when the extract_vars LLM call
// fails inside executeLLM, the error is non-fatal and executeNode still returns
// without error (just no extracted vars).
func TestExecuteNodeExtractVarsErrorInternal(t *testing.T) {
	node := &Node{
		ID:    "n",
		Type:  NodeTypeLLM,
		Name:  "ExtractNode",
		Prompt: "extract things",
		ExtractVars: []VariableDef{
			{Name: "status", Type: "string", Description: "the status"},
		},
	}
	state := newState("test")

	calls := 0
	llm := &stubLLM{}
	// First call (execute): return content; second call (extract_vars): return error.
	llm.resp = &CompletionResponse{Content: "some output"}
	errLLM := &sequenceLLM{
		responses: []*CompletionResponse{{Content: "some output"}},
		errors:    []error{nil, errors.New("extract_vars unavailable")},
	}
	_ = calls
	_ = llm

	out, err := executeNode(context.Background(), node, state, errLLM, nil)
	if err != nil {
		t.Fatalf("expected non-fatal, got error: %v", err)
	}
	if out.Text != "some output" {
		t.Errorf("expected output text, got %q", out.Text)
	}
	if out.Vars != nil {
		t.Errorf("expected no vars after extract error, got %v", out.Vars)
	}
}

// sequenceLLM returns scripted responses in order. Either slice may be shorter;
// missing entries default to &CompletionResponse{} / nil respectively.
type sequenceLLM struct {
	responses []*CompletionResponse
	errors    []error
	idx       int
}

func (s *sequenceLLM) Complete(_ context.Context, _ CompletionRequest) (*CompletionResponse, error) {
	i := s.idx
	s.idx++
	var resp *CompletionResponse
	var err error
	if i < len(s.responses) {
		resp = s.responses[i]
	}
	if i < len(s.errors) {
		err = s.errors[i]
	}
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return &CompletionResponse{}, nil
	}
	return resp, nil
}

// TestChooseNextNodeDefaultCase covers the default: branch in chooseNextNode,
// which fires when node.Type is not Route, LLM, or Webhook but has outgoing edges.
// This path is unreachable through the engine (executeNode errors first), so we
// call chooseNextNode directly.
func TestChooseNextNodeDefaultCase(t *testing.T) {
	node := &Node{ID: "x", Type: NodeType("CustomType")}
	edge := &Edge{ID: "e1", Source: "x", Target: "y"}
	state := newState("test")
	out := &nodeOutput{Text: "output"}

	targetID, reason, err := chooseNextNode(context.Background(), node, out, state, []*Edge{edge}, &stubLLM{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if targetID != "y" {
		t.Errorf("expected target=y, got %q", targetID)
	}
	if reason != "default" {
		t.Errorf("expected reason=default, got %q", reason)
	}
}

// TestCheckGlobalNodeEmptyGlobals covers the len(globals)==0 early return in
// checkGlobalNode. The engine never calls checkGlobalNode with an empty slice,
// so we invoke it directly.
func TestCheckGlobalNodeEmptyGlobals(t *testing.T) {
	got, err := checkGlobalNode(context.Background(), nil, newState("test"), &stubLLM{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil node for empty globals, got %v", got)
	}
}

// TestExecuteWebhookBodyMarshalError covers the json.Marshal error path in
// executeWebhook by setting WebhookBody to a type that json.Marshal cannot handle.
func TestExecuteWebhookBodyMarshalError(t *testing.T) {
	node := &Node{
		ID:            "wh",
		Type:          NodeTypeWebhook,
		Name:          "TestWebhook",
		WebhookURL:    "http://localhost/",
		WebhookMethod: "POST",
		WebhookBody:   make(chan int), // chan is not JSON-serializable
	}
	state := newState("test")
	_, err := executeWebhook(context.Background(), node, state)
	if err == nil {
		t.Fatal("expected error marshaling chan body, got nil")
	}
	if !strings.Contains(err.Error(), "webhook body marshal") {
		t.Errorf("expected 'webhook body marshal' in error, got: %v", err)
	}
}

// TestParseChannelDirectiveEmptyToolName verifies that "to=" with only
// whitespace following it returns ok=false.
func TestParseChannelDirectiveEmptyToolName(t *testing.T) {
	input := `<|channel|>to= <|message|>{}`
	_, _, ok := parseChannelDirective(input)
	if ok {
		t.Error("expected ok=false when tool name is empty after 'to=', got true")
	}
}

// TestExecuteWebhookStatus400 verifies that executeWebhook returns an error when
// the server responds with a 4xx status code (covers the resp.StatusCode >= 400 branch).
func TestExecuteWebhookStatus400(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, "bad request body")
	}))
	defer ts.Close()

	node := &Node{
		ID:            "wh",
		Type:          NodeTypeWebhook,
		Name:          "TestWebhook",
		WebhookURL:    ts.URL,
		WebhookMethod: "POST",
	}
	state := newState("test")
	_, err := executeWebhook(context.Background(), node, state)
	if err == nil {
		t.Fatal("expected error for 4xx status code, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("expected error to mention status 400, got: %v", err)
	}
}

// TestExecuteWebhookHTTPError verifies that executeWebhook returns an error when
// the HTTP request itself fails (covers the http.DefaultClient.Do error path).
// The server is closed before the request is sent to guarantee connection refused.
func TestExecuteWebhookHTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := ts.URL
	ts.Close() // connection refused on all subsequent requests

	node := &Node{
		ID:            "wh",
		Type:          NodeTypeWebhook,
		Name:          "TestWebhook",
		WebhookURL:    addr,
		WebhookMethod: "POST",
	}
	state := newState("test")
	_, err := executeWebhook(context.Background(), node, state)
	if err == nil {
		t.Fatal("expected error for connection refused, got nil")
	}
}

// TestExecuteWebhookInvalidURL verifies that executeWebhook returns an error
// when the node URL is malformed (covers the http.NewRequestWithContext error path).
func TestExecuteWebhookInvalidURL(t *testing.T) {
	node := &Node{
		ID:            "wh",
		Type:          NodeTypeWebhook,
		Name:          "TestWebhook",
		WebhookURL:    "http://[::1", // unclosed IPv6 bracket → url.Parse fails
		WebhookMethod: "POST",
	}
	state := newState("test")
	_, err := executeWebhook(context.Background(), node, state)
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

// TestExecuteWebhookDefaultMethod verifies that executeWebhook defaults the HTTP
// method to POST when Node.WebhookMethod is empty. ParsePathwayBytes normally
// sets this, but this tests the defensive in-function fallback directly.
func TestExecuteWebhookDefaultMethod(t *testing.T) {
	var gotMethod string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()

	node := &Node{
		ID:            "wh",
		Type:          NodeTypeWebhook,
		Name:          "TestWebhook",
		WebhookURL:    ts.URL,
		WebhookMethod: "", // intentionally empty to trigger the default
	}
	state := newState("test")
	_, err := executeWebhook(context.Background(), node, state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("expected method=POST, got %q", gotMethod)
	}
}
