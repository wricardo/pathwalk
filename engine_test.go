package pathwalk_test

import (
	"context"
	"encoding/json"
	"testing"

	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/pathwaytest"
)

// minimalPathway is a simple two-node pathway: Default → End Call.
// It is embedded inline so tests have no external file dependency.
const minimalPathwayJSON = `{
  "nodes": [
    {
      "id": "n1",
      "type": "Default",
      "data": {
        "name": "Greet",
        "isStart": true,
        "prompt": "Greet the user.",
        "condition": "Exit after greeting."
      }
    },
    {
      "id": "n2",
      "type": "End Call",
      "data": { "name": "Done", "text": "Goodbye!" }
    }
  ],
  "edges": [
    {
      "id": "e1",
      "source": "n1",
      "target": "n2",
      "data": { "label": "continue", "description": "" }
    }
  ]
}`

// extractVarsPathway has a Default node that should extract a variable.
const extractVarsPathwayJSON = `{
  "nodes": [
    {
      "id": "classify",
      "type": "Default",
      "data": {
        "name": "Classify",
        "isStart": true,
        "prompt": "Classify the request.",
        "extractVars": [
          ["operation_type", "string", "The operation category", true]
        ]
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Done." }
    }
  ],
  "edges": [
    {
      "id": "e1",
      "source": "classify",
      "target": "end",
      "data": { "label": "done", "description": "" }
    }
  ]
}`

// routePathway has a Route node that branches based on operation_type.
const routePathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "Classify the task.",
        "extractVars": [
          ["operation_type", "string", "The operation category", true]
        ]
      }
    },
    {
      "id": "router",
      "type": "Route",
      "data": {
        "name": "Route",
        "routes": [
          {
            "conditions": [{ "field": "operation_type", "value": "orders", "operator": "is" }],
            "targetNodeId": "orders-node"
          },
          {
            "conditions": [{ "field": "operation_type", "value": "reporting", "operator": "is" }],
            "targetNodeId": "reporting-node"
          }
        ],
        "fallbackNodeId": "end"
      }
    },
    {
      "id": "orders-node",
      "type": "Default",
      "data": { "name": "Orders", "prompt": "Handle orders." }
    },
    {
      "id": "reporting-node",
      "type": "Default",
      "data": { "name": "Reporting", "prompt": "Generate report." }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "router", "data": { "label": "continue" } },
    { "id": "e2", "source": "router", "target": "orders-node", "data": { "label": "orders" } },
    { "id": "e3", "source": "router", "target": "reporting-node", "data": { "label": "reporting" } },
    { "id": "e4", "source": "router", "target": "end", "data": { "label": "fallback" } },
    { "id": "e5", "source": "orders-node", "target": "end", "data": { "label": "done" } },
    { "id": "e6", "source": "reporting-node", "target": "end", "data": { "label": "done" } }
  ]
}`

func mustParsePathway(t *testing.T, raw string) *pathwalk.Pathway {
	t.Helper()
	pp, err := pathwalk.ParsePathwayBytes([]byte(raw))
	if err != nil {
		t.Fatalf("ParsePathwayBytes: %v", err)
	}
	return pp
}

// TestMinimalPathway verifies that a simple Default→EndCall pathway runs to
// completion and returns the End Call text.
func TestMinimalPathway(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)

	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("n1", pathwaytest.MockResponse{Content: "Hello! How can I help you today?"})

	engine := pathwalk.NewEngine(pp, mock)
	result, err := engine.Run(context.Background(), "Say hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Reason != "terminal" {
		t.Errorf("expected reason=terminal, got %q", result.Reason)
	}
	if result.Output != "Goodbye!" {
		t.Errorf("expected output %q, got %q", "Goodbye!", result.Output)
	}
	if mock.CallCount("n1") != 1 {
		t.Errorf("expected 1 LLM call at n1, got %d", mock.CallCount("n1"))
	}
}

// TestExtractVars verifies that variables extracted by the mock tool call are
// merged into the result.
func TestExtractVars(t *testing.T) {
	pp := mustParsePathway(t, extractVarsPathwayJSON)

	mock := pathwaytest.NewMockLLMClient()

	mock.OnNodePurpose("classify", "execute", pathwaytest.MockResponse{
		Content: "This is an inventory management request.",
	})
	mock.OnNodePurpose("classify", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{
				Name: "set_variables",
				Args: map[string]any{"operation_type": "inventory_mgmt"},
			},
		},
	})

	engine := pathwalk.NewEngine(pp, mock)
	result, err := engine.Run(context.Background(), "Check inventory levels")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Reason != "terminal" {
		t.Errorf("expected reason=terminal, got %q", result.Reason)
	}
	if got, ok := result.Variables["operation_type"]; !ok || got != "inventory_mgmt" {
		t.Errorf("expected operation_type=inventory_mgmt, got %v", got)
	}
}

// TestRouteNode verifies that a Route node correctly branches based on a variable.
func TestRouteNode(t *testing.T) {
	cases := []struct {
		operationType string
		expectedVisit string // node name that should be visited after routing
	}{
		{"orders", "Orders"},
		{"reporting", "Reporting"},
		{"unknown", "Done"}, // fallback → End node
	}

	for _, tc := range cases {
		t.Run(tc.operationType, func(t *testing.T) {
			pp := mustParsePathway(t, routePathwayJSON)
			mock := pathwaytest.NewMockLLMClient()

			mock.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "Identified operation: " + tc.operationType,
			})
			mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					{Name: "set_variables", Args: map[string]any{"operation_type": tc.operationType}},
				},
			})

			mock.SetDefault(pathwaytest.MockResponse{Content: "handled"})

			engine := pathwalk.NewEngine(pp, mock)
			result, err := engine.Run(context.Background(), "Do something")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Reason != "terminal" {
				t.Errorf("expected terminal, got %q", result.Reason)
			}

			var visitedNames []string
			for _, s := range result.Steps {
				visitedNames = append(visitedNames, s.NodeName)
			}
			found := false
			for _, name := range visitedNames {
				if name == tc.expectedVisit {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected step %q to be visited; steps: %v", tc.expectedVisit, visitedNames)
			}
		})
	}
}

// TestToolCallInNode verifies that a tool registered with the engine is called
// when the mock scripts a tool call.
func TestToolCallInNode(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()

	toolCalled := false
	myTool := pathwalk.Tool{
		Name:        "my_tool",
		Description: "A test tool",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"msg": map[string]any{"type": "string"}},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			toolCalled = true
			return "tool result: " + args["msg"].(string), nil
		},
	}

	mock.OnNode("n1", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "my_tool", Args: map[string]any{"msg": "hello"}},
		},
		Content: "Tool was called successfully.",
	})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithTools(myTool))
	result, err := engine.Run(context.Background(), "Use the tool")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !toolCalled {
		t.Error("expected my_tool to be called, but it wasn't")
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
}

// TestMockCallCount verifies that CallCount correctly tracks per-node calls.
func TestMockCallCount(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	engine := pathwalk.NewEngine(pp, mock)
	_, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if mock.CallCount("n1") != 1 {
		t.Errorf("expected 1 call for n1, got %d", mock.CallCount("n1"))
	}
	if mock.CallCount("n2") != 0 {
		t.Errorf("expected 0 calls for n2 (End Call node), got %d", mock.CallCount("n2"))
	}
}

// TestParsePizzeria verifies that the pizzeria example pathway parses without error.
func TestParsePizzeria(t *testing.T) {
	pp, err := pathwalk.ParsePathway("examples/pizzeria_ops.json")
	if err != nil {
		t.Fatalf("ParsePathway: %v", err)
	}
	if pp.StartNode == nil {
		t.Fatal("no start node found")
	}
	if pp.StartNode.ID != "start" {
		t.Errorf("expected start node id=start, got %q", pp.StartNode.ID)
	}

	expectedIDs := []string{"start", "route-op", "order-mgmt", "menu-mgmt", "inventory-mgmt", "reporting", "end"}
	for _, id := range expectedIDs {
		if _, ok := pp.NodeByID[id]; !ok {
			t.Errorf("node %q not found in NodeByID", id)
		}
	}

	if pp.GraphQLEndpoint != "http://localhost:4000/graphql" {
		t.Errorf("expected GraphQLEndpoint=%q, got %q", "http://localhost:4000/graphql", pp.GraphQLEndpoint)
	}
}

// TestParsePathwayBytesGraphQLEndpoint verifies that graphqlEndpoint is parsed from JSON.
func TestParsePathwayBytesGraphQLEndpoint(t *testing.T) {
	raw := []byte(`{
		"graphqlEndpoint": "https://api.example.com/graphql",
		"nodes": [
			{"id":"n1","type":"Default","data":{"name":"Start","isStart":true}},
			{"id":"n2","type":"End Call","data":{"name":"End","text":"done"}}
		],
		"edges":[{"id":"e1","source":"n1","target":"n2","data":{}}]
	}`)
	pp, err := pathwalk.ParsePathwayBytes(raw)
	if err != nil {
		t.Fatalf("ParsePathwayBytes: %v", err)
	}
	if pp.GraphQLEndpoint != "https://api.example.com/graphql" {
		t.Errorf("GraphQLEndpoint=%q, want %q", pp.GraphQLEndpoint, "https://api.example.com/graphql")
	}
}

// TestParsePathwayBytesNoGraphQLEndpoint verifies that omitting graphqlEndpoint gives empty string.
func TestParsePathwayBytesNoGraphQLEndpoint(t *testing.T) {
	raw := []byte(`{
		"nodes": [
			{"id":"n1","type":"Default","data":{"name":"Start","isStart":true}},
			{"id":"n2","type":"End Call","data":{"name":"End","text":"done"}}
		],
		"edges":[{"id":"e1","source":"n1","target":"n2","data":{}}]
	}`)
	pp, err := pathwalk.ParsePathwayBytes(raw)
	if err != nil {
		t.Fatalf("ParsePathwayBytes: %v", err)
	}
	if pp.GraphQLEndpoint != "" {
		t.Errorf("expected empty GraphQLEndpoint, got %q", pp.GraphQLEndpoint)
	}
}

// TestPizzeriaRouting tests the full pizzeria pathway with mocked LLM responses,
// verifying that each operation type reaches the correct handler node.
func TestPizzeriaRouting(t *testing.T) {
	pp, err := pathwalk.ParsePathway("examples/pizzeria_ops.json")
	if err != nil {
		t.Fatalf("ParsePathway: %v", err)
	}

	cases := []struct {
		task         string
		opType       string
		expectedNode string
	}{
		{"Create an order for John: 2x Margherita", "order_mgmt", "Order Management"},
		{"Show me the pizza menu", "menu_mgmt", "Menu Management"},
		{"Check and restock inventory", "inventory_mgmt", "Inventory Management"},
		{"Give me today's revenue summary", "reporting", "Reporting"},
	}

	for _, tc := range cases {
		t.Run(tc.opType, func(t *testing.T) {
			mock := pathwaytest.NewMockLLMClient()

			mock.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "Operation: " + tc.opType,
			})
			mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					{Name: "set_variables", Args: map[string]any{"operation_type": tc.opType}},
				},
			})

			handlerNodes := []string{"order-mgmt", "menu-mgmt", "inventory-mgmt", "reporting"}
			for _, id := range handlerNodes {
				mock.OnNodePurpose(id, "execute", pathwaytest.MockResponse{
					Content: "Operation completed successfully.",
				})
			}

			var gqlCalls []map[string]any
			fakeTool := pathwalk.Tool{
				Name:        "graphql",
				Description: "Fake GraphQL",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
				Fn: func(ctx context.Context, args map[string]any) (any, error) {
					gqlCalls = append(gqlCalls, args)
					return map[string]any{"data": "ok"}, nil
				},
			}

			engine := pathwalk.NewEngine(pp, mock, pathwalk.WithTools(fakeTool))
			result, err := engine.Run(context.Background(), tc.task)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if result.Reason != "terminal" {
				t.Errorf("expected terminal, got %q", result.Reason)
			}

			var visitedNames []string
			for _, s := range result.Steps {
				visitedNames = append(visitedNames, s.NodeName)
			}
			found := false
			for _, name := range visitedNames {
				if name == tc.expectedNode {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected %q to be visited; got steps: %v", tc.expectedNode, visitedNames)
			}
		})
	}
}

// TestRouteConditionEvaluation tests the pure-Go Route node condition logic.
func TestRouteConditionEvaluation(t *testing.T) {
	tests := []struct {
		vars     map[string]any
		expected string // End Call text
	}{
		{map[string]any{"score": "150", "status": "active"}, "high-active"},
		{map[string]any{"score": "50", "status": "active"}, "fallback"}, // score < 100
		{map[string]any{"score": "200", "status": "inactive"}, "inactive"},
		{map[string]any{}, "fallback"},
	}

	for _, tc := range tests {
		t.Run(tc.expected, func(t *testing.T) {
			innerJSON := `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "p",
        "extractVars": [
          ["score",  "string", "score",  false],
          ["status", "string", "status", false]
        ]
      }
    },
    {
      "id": "router",
      "type": "Route",
      "data": {
        "name": "Route",
        "routes": [
          {
            "conditions": [
              { "field": "score",  "value": "100",      "operator": ">=" },
              { "field": "status", "value": "active",   "operator": "is" }
            ],
            "targetNodeId": "high-active"
          },
          {
            "conditions": [{ "field": "status", "value": "inactive", "operator": "is" }],
            "targetNodeId": "inactive"
          }
        ],
        "fallbackNodeId": "fallback"
      }
    },
    { "id": "high-active", "type": "End Call", "data": { "name": "HighActive", "text": "high-active" } },
    { "id": "inactive",    "type": "End Call", "data": { "name": "Inactive",   "text": "inactive"    } },
    { "id": "fallback",    "type": "End Call", "data": { "name": "Fallback",   "text": "fallback"    } }
  ],
  "edges": [
    { "id": "e1", "source": "start",  "target": "router",      "data": {} },
    { "id": "e2", "source": "router", "target": "high-active", "data": {} },
    { "id": "e3", "source": "router", "target": "inactive",    "data": {} },
    { "id": "e4", "source": "router", "target": "fallback",    "data": {} }
  ]
}`
			innerPP := mustParsePathway(t, innerJSON)
			innerMock := pathwaytest.NewMockLLMClient()
			innerMock.OnNodePurpose("start", "execute", pathwaytest.MockResponse{Content: "ok"})
			innerMock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					{Name: "set_variables", Args: tc.vars},
				},
			})

			engine := pathwalk.NewEngine(innerPP, innerMock)
			result, err := engine.Run(context.Background(), "test")
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Output != tc.expected {
				t.Errorf("expected output=%q, got %q (vars=%v)", tc.expected, result.Output, tc.vars)
			}
		})
	}
}

// TestMaxSteps verifies the engine stops after maxSteps and returns "max_steps".
func TestMaxSteps(t *testing.T) {
	const loopJSON = `{
  "nodes": [
    {
      "id": "n1",
      "type": "Default",
      "data": { "name": "Loop", "isStart": true, "prompt": "loop" }
    }
  ],
  "edges": [
    { "id": "e1", "source": "n1", "target": "n1", "data": { "label": "loop back" } }
  ]
}`
	pp := mustParsePathway(t, loopJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "looping"})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithMaxSteps(3))
	result, err := engine.Run(context.Background(), "loop forever")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "max_steps" {
		t.Errorf("expected reason=max_steps, got %q", result.Reason)
	}
	if len(result.Steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(result.Steps))
	}
}

// TestRecordedCalls verifies the Calls slice captures request context correctly.
func TestRecordedCalls(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "hi"})

	engine := pathwalk.NewEngine(pp, mock)
	_, _ = engine.Run(context.Background(), "test")

	if len(mock.Calls) == 0 {
		t.Fatal("expected at least one recorded call")
	}
	if mock.Calls[0].NodeID != "n1" {
		t.Errorf("expected first call node=n1, got %q", mock.Calls[0].NodeID)
	}
	if mock.Calls[0].Purpose != "execute" {
		t.Errorf("expected first call purpose=execute, got %q", mock.Calls[0].Purpose)
	}
}

// TestMockErrorPropagation verifies that a mock error causes Run to return an error.
func TestMockErrorPropagation(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("n1", pathwaytest.MockResponse{Error: errTest})

	engine := pathwalk.NewEngine(pp, mock)
	_, err := engine.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

// TestParsePathwayBytes verifies field parsing from the pathway format.
func TestParsePathwayBytes(t *testing.T) {
	raw := []byte(`{
    "nodes": [
      {
        "id": "abc",
        "type": "Default",
        "data": {
          "name": "Test Node",
          "isStart": true,
          "prompt": "You are a test.",
          "condition": "exit condition",
          "extractVars": [
            ["foo", "string", "a foo var", true],
            ["bar", "integer", "a bar var", false]
          ],
          "modelOptions": { "newTemperature": 0.7 }
        }
      },
      { "id": "xyz", "type": "End Call", "data": { "name": "End", "text": "bye" } }
    ],
    "edges": [
      { "id": "e1", "source": "abc", "target": "xyz", "data": { "label": "lbl", "description": "desc" } }
    ]
  }`)

	pp, err := pathwalk.ParsePathwayBytes(raw)
	if err != nil {
		t.Fatalf("ParsePathwayBytes: %v", err)
	}

	n := pp.NodeByID["abc"]
	if n == nil {
		t.Fatal("node abc not found")
	}
	if n.Prompt != "You are a test." {
		t.Errorf("prompt mismatch: %q", n.Prompt)
	}
	if n.Temperature != 0.7 {
		t.Errorf("temperature mismatch: %f", n.Temperature)
	}
	if len(n.ExtractVars) != 2 {
		t.Fatalf("expected 2 extractVars, got %d", len(n.ExtractVars))
	}
	if n.ExtractVars[0].Name != "foo" || !n.ExtractVars[0].Required {
		t.Errorf("first extractVar wrong: %+v", n.ExtractVars[0])
	}
	if n.ExtractVars[1].Name != "bar" || n.ExtractVars[1].Type != "integer" {
		t.Errorf("second extractVar wrong: %+v", n.ExtractVars[1])
	}

	edges := pp.EdgesFrom["abc"]
	if len(edges) != 1 || edges[0].Label != "lbl" || edges[0].Desc != "desc" {
		t.Errorf("edge parsing wrong: %+v", edges)
	}
}

// TestJSONRoundTrip verifies RunResult can be marshalled to JSON cleanly.
func TestJSONRoundTrip(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "hi"})

	engine := pathwalk.NewEngine(pp, mock)
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out pathwalk.RunResult
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if out.Reason != result.Reason {
		t.Errorf("reason mismatch after round-trip: %q vs %q", out.Reason, result.Reason)
	}
}

// errTest is a sentinel error used in TestMockErrorPropagation.
type sentinelError struct{ msg string }

func (e sentinelError) Error() string { return e.msg }

var errTest = sentinelError{"intentional test error"}
