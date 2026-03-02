package pathwalk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
// completion and reaches the End Call node.
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
	if got := result.Steps[len(result.Steps)-1].NodeName; got != "Done" {
		t.Errorf("expected terminal node name %q, got %q", "Done", got)
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
		vars         map[string]any
		label        string // sub-test label (End Call text)
		expectedNode string // terminal node name to check
	}{
		{map[string]any{"score": "150", "status": "active"}, "high-active", "HighActive"},
		{map[string]any{"score": "50", "status": "active"}, "fallback", "Fallback"}, // score < 100
		{map[string]any{"score": "200", "status": "inactive"}, "inactive", "Inactive"},
		{map[string]any{}, "fallback", "Fallback"},
	}

	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
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
			if got := result.Steps[len(result.Steps)-1].NodeName; got != tc.expectedNode {
				t.Errorf("expected terminal node=%q, got %q (vars=%v)", tc.expectedNode, got, tc.vars)
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

// TestLLMRouteMultiEdge verifies that when an LLM node has two outgoing edges
// the engine makes a "route" LLM call and follows the selected edge.
func TestLLMRouteMultiEdge(t *testing.T) {
	const multiEdgeJSON = `{
  "nodes": [
    { "id": "start", "type": "Default", "data": { "name": "Start", "isStart": true, "prompt": "classify", "condition": "Route to correct path." } },
    { "id": "path-a", "type": "End Call", "data": { "name": "PathA", "text": "path-a" } },
    { "id": "path-b", "type": "End Call", "data": { "name": "PathB", "text": "path-b" } }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "path-a", "data": { "label": "route A", "description": "A tasks" } },
    { "id": "e2", "source": "start", "target": "path-b", "data": { "label": "route B", "description": "B tasks" } }
  ]
}`
	pp := mustParsePathway(t, multiEdgeJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("start", "execute", pathwaytest.MockResponse{Content: "Classified as B."})
	mock.OnNodePurpose("start", "route", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "select_route", Args: map[string]any{"route": float64(2), "reason": "is a B task"}},
		},
	})

	engine := pathwalk.NewEngine(pp, mock)
	result, err := engine.Run(context.Background(), "route me to B")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if got := result.Steps[len(result.Steps)-1].NodeName; got != "PathB" {
		t.Errorf("expected terminal node name %q, got %q", "PathB", got)
	}
}

// TestWebhookNode verifies that a Webhook node makes an HTTP call and extracts
// variables from the JSON response body.
func TestWebhookNode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"order_id":"42","status":"created"}`)
	}))
	defer ts.Close()

	webhookJSON := fmt.Sprintf(`{
  "nodes": [
    {
      "id": "wh",
      "type": "Webhook",
      "data": {
        "name": "CreateOrder",
        "isStart": true,
        "url": %q,
        "method": "POST",
        "extractVars": [["order_id", "string", "Order ID", false]]
      }
    },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "wh", "target": "end", "data": {} }]
}`, ts.URL)

	pp := mustParsePathway(t, webhookJSON)
	engine := pathwalk.NewEngine(pp, pathwaytest.NewMockLLMClient())
	result, err := engine.Run(context.Background(), "create order")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if result.Variables["order_id"] != "42" {
		t.Errorf("expected order_id=42, got %v", result.Variables["order_id"])
	}
}

// TestWebhookResolveBody verifies that {{variable}} placeholders in the webhook
// body are substituted with state variables before the HTTP call is made.
func TestWebhookResolveBody(t *testing.T) {
	var receivedBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()

	pathwayJSON := fmt.Sprintf(`{
  "nodes": [
    {
      "id": "n1",
      "type": "Default",
      "data": {
        "name": "Collect",
        "isStart": true,
        "prompt": "collect",
        "extractVars": [["customer", "string", "customer name", true]]
      }
    },
    {
      "id": "wh",
      "type": "Webhook",
      "data": {
        "name": "SendOrder",
        "url": %q,
        "method": "POST",
        "body": {"customer": "{{customer}}"}
      }
    },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [
    { "id": "e1", "source": "n1", "target": "wh",  "data": {} },
    { "id": "e2", "source": "wh", "target": "end", "data": {} }
  ]
}`, ts.URL)

	pp := mustParsePathway(t, pathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("n1", "execute", pathwaytest.MockResponse{Content: "ok"})
	mock.OnNodePurpose("n1", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"customer": "Alice"}},
		},
	})

	engine := pathwalk.NewEngine(pp, mock)
	result, err := engine.Run(context.Background(), "send order")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if !strings.Contains(string(receivedBody), "Alice") {
		t.Errorf("expected request body to contain Alice, got: %s", receivedBody)
	}
}

// TestWebhookErrorStatus verifies that a 4xx/5xx HTTP response causes Run to
// return an error.
func TestWebhookErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	webhookJSON := fmt.Sprintf(`{
  "nodes": [
    { "id": "wh", "type": "Webhook", "data": { "name": "Bad", "isStart": true, "url": %q, "method": "GET" } },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "wh", "target": "end", "data": {} }]
}`, ts.URL)

	pp := mustParsePathway(t, webhookJSON)
	_, err := engine_run(t, pp)
	if err == nil {
		t.Fatal("expected an error for 404 response, got nil")
	}
}

// engine_run is a small helper used in webhook error tests to avoid boilerplate.
func engine_run(t *testing.T, pp *pathwalk.Pathway) (*pathwalk.RunResult, error) {
	t.Helper()
	return pathwalk.NewEngine(pp, pathwaytest.NewMockLLMClient()).Run(context.Background(), "test")
}

// TestGlobalNodeInterception verifies that when the global-check LLM call
// selects a global node, the engine redirects execution to it.
func TestGlobalNodeInterception(t *testing.T) {
	const globalJSON = `{
  "nodes": [
    { "id": "start",     "type": "Default",  "data": { "name": "Start",     "isStart": true, "prompt": "handle" } },
    { "id": "cancel",    "type": "Default",  "data": { "name": "Cancel",    "isGlobal": true, "globalLabel": "Cancel the order", "prompt": "cancel" } },
    { "id": "end",       "type": "End Call", "data": { "name": "Done",      "text": "done" } },
    { "id": "cancelled", "type": "End Call", "data": { "name": "Cancelled", "text": "cancelled" } }
  ],
  "edges": [
    { "id": "e1", "source": "start",  "target": "end",       "data": {} },
    { "id": "e2", "source": "cancel", "target": "cancelled", "data": {} }
  ]
}`
	pp := mustParsePathway(t, globalJSON)
	mock := pathwaytest.NewMockLLMClient()

	// First global check selects the cancel node (index 1).
	mock.OnNodePurpose(pathwalk.GlobalCheckNodeID, "check_global", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "select_global_node", Args: map[string]any{"node": float64(1)}},
		},
	})
	// Cancel node execution.
	mock.OnNode("cancel", pathwaytest.MockResponse{Content: "Order cancelled."})
	// Subsequent global checks return no match (default empty response).
	mock.SetDefault(pathwaytest.MockResponse{Content: ""})

	engine := pathwalk.NewEngine(pp, mock)
	result, err := engine.Run(context.Background(), "cancel my order")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if got := result.Steps[len(result.Steps)-1].NodeName; got != "Cancelled" {
		t.Errorf("expected terminal node name %q, got %q", "Cancelled", got)
	}
}

// TestWithVerbose verifies that verbose mode does not affect the run result.
func TestWithVerbose(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "hi"})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithVerbose(true))
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
}

// TestWithGlobalNodeCheckDisabled verifies that WithGlobalNodeCheck(false)
// prevents the global-check LLM call even when the pathway has global nodes.
func TestWithGlobalNodeCheckDisabled(t *testing.T) {
	const withGlobalJSON = `{
  "nodes": [
    { "id": "start",   "type": "Default", "data": { "name": "Start",  "isStart": true, "prompt": "go" } },
    { "id": "global1", "type": "Default", "data": { "name": "Cancel", "isGlobal": true, "globalLabel": "Cancel", "prompt": "cancel" } },
    { "id": "end",     "type": "End Call","data": { "name": "Done",   "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "start", "target": "end", "data": {} }]
}`
	pp := mustParsePathway(t, withGlobalJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithGlobalNodeCheck(false))
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if mock.CallCount(pathwalk.GlobalCheckNodeID) != 0 {
		t.Errorf("expected 0 global check calls, got %d", mock.CallCount(pathwalk.GlobalCheckNodeID))
	}
}

// TestDeadEnd verifies that a node with no outgoing edges causes the run to
// stop with reason "dead_end".
func TestDeadEnd(t *testing.T) {
	const deadEndJSON = `{
  "nodes": [
    { "id": "n1", "type": "Default", "data": { "name": "Orphan", "isStart": true, "prompt": "do something" } }
  ],
  "edges": []
}`
	pp := mustParsePathway(t, deadEndJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "dead_end" {
		t.Errorf("expected dead_end, got %q", result.Reason)
	}
}

// TestUnsupportedNodeType verifies that a node with an unrecognised type is
// skipped and execution follows its first outgoing edge.
func TestUnsupportedNodeType(t *testing.T) {
	const unknownTypeJSON = `{
  "nodes": [
    { "id": "start", "type": "Default",     "data": { "name": "Start", "isStart": true, "prompt": "go" } },
    { "id": "weird", "type": "UnknownType", "data": { "name": "Weird" } },
    { "id": "end",   "type": "End Call",    "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "weird", "data": {} },
    { "id": "e2", "source": "weird", "target": "end",   "data": {} }
  ]
}`
	pp := mustParsePathway(t, unknownTypeJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal after skipping unknown node, got %q", result.Reason)
	}
	if result.Output != "ok" {
		t.Errorf("expected output=ok (last LLM step output), got %q", result.Output)
	}
}

// TestMaxNodeVisits verifies that a looping pathway stops with "max_node_visits"
// once a node exceeds its allowed visit count.
//
// Topology: NodeA → NodeB → NodeA → ... (cycle)
// maxVisitsPerNode: 2 — NodeA may execute at most twice; the 3rd visit is blocked.
func TestMaxNodeVisits(t *testing.T) {
	const loopPathwayJSON = `{
  "maxVisitsPerNode": 2,
  "nodes": [
    { "id": "nA", "type": "Default", "data": { "name": "NodeA", "isStart": true, "prompt": "Do A." } },
    { "id": "nB", "type": "Default", "data": { "name": "NodeB", "prompt": "Do B." } }
  ],
  "edges": [
    { "id": "e1", "source": "nA", "target": "nB", "data": {} },
    { "id": "e2", "source": "nB", "target": "nA", "data": {} }
  ]
}`
	pp := mustParsePathway(t, loopPathwayJSON)
	if pp.MaxVisitsPerNode != 2 {
		t.Fatalf("expected MaxVisitsPerNode=2, got %d", pp.MaxVisitsPerNode)
	}

	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	// Use a high engine step cap so only the per-node limit triggers.
	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithMaxSteps(100))
	result, err := engine.Run(context.Background(), "loop test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Reason != "max_node_visits" {
		t.Errorf("expected reason=max_node_visits, got %q", result.Reason)
	}
	if result.FailedNode != "NodeA" {
		t.Errorf("expected FailedNode=NodeA, got %q", result.FailedNode)
	}
}

// TestMaxNodeVisitsPerNodeOverride verifies that a node-level maxVisits overrides
// the pathway-level MaxVisitsPerNode default.
//
// NodeA has maxVisits:1 (stops after 1 visit), pathway default is 5.
func TestMaxNodeVisitsPerNodeOverride(t *testing.T) {
	const overridePathwayJSON = `{
  "maxVisitsPerNode": 5,
  "nodes": [
    { "id": "nA", "type": "Default", "data": { "name": "NodeA", "isStart": true, "prompt": "Do A.", "maxVisits": 1 } },
    { "id": "nB", "type": "Default", "data": { "name": "NodeB", "prompt": "Do B." } }
  ],
  "edges": [
    { "id": "e1", "source": "nA", "target": "nB", "data": {} },
    { "id": "e2", "source": "nB", "target": "nA", "data": {} }
  ]
}`
	pp := mustParsePathway(t, overridePathwayJSON)

	nodeA := pp.NodeByID["nA"]
	if nodeA == nil {
		t.Fatal("nA not found in pathway")
	}
	if nodeA.MaxVisits != 1 {
		t.Fatalf("expected node MaxVisits=1, got %d", nodeA.MaxVisits)
	}

	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithMaxSteps(100))
	result, err := engine.Run(context.Background(), "override test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// NodeA is capped at 1 visit. After NodeA executes once → NodeB executes once
	// → back to NodeA → blocked on visit 2.
	if result.Reason != "max_node_visits" {
		t.Errorf("expected reason=max_node_visits, got %q", result.Reason)
	}
	if result.FailedNode != "NodeA" {
		t.Errorf("expected FailedNode=NodeA, got %q", result.FailedNode)
	}
}

// TestMaxTurns verifies that a pathway-level maxTurns JSON field overrides the
// engine's default step cap.
//
// A 5-node chain with maxTurns:3 should stop after visiting only 3 nodes.
func TestMaxTurns(t *testing.T) {
	const chainPathwayJSON = `{
  "maxTurns": 3,
  "nodes": [
    { "id": "n1", "type": "Default", "data": { "name": "N1", "isStart": true, "prompt": "step 1" } },
    { "id": "n2", "type": "Default", "data": { "name": "N2", "prompt": "step 2" } },
    { "id": "n3", "type": "Default", "data": { "name": "N3", "prompt": "step 3" } },
    { "id": "n4", "type": "Default", "data": { "name": "N4", "prompt": "step 4" } },
    { "id": "n5", "type": "End Call", "data": { "name": "End", "text": "done" } }
  ],
  "edges": [
    { "id": "e1", "source": "n1", "target": "n2", "data": {} },
    { "id": "e2", "source": "n2", "target": "n3", "data": {} },
    { "id": "e3", "source": "n3", "target": "n4", "data": {} },
    { "id": "e4", "source": "n4", "target": "n5", "data": {} }
  ]
}`
	pp := mustParsePathway(t, chainPathwayJSON)
	if pp.MaxTurns != 3 {
		t.Fatalf("expected MaxTurns=3, got %d", pp.MaxTurns)
	}

	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	// Engine default is 50; pathway maxTurns:3 should take precedence.
	engine := pathwalk.NewEngine(pp, mock)
	result, err := engine.Run(context.Background(), "chain test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Reason != "max_steps" {
		t.Errorf("expected reason=max_steps, got %q", result.Reason)
	}
	// Only 3 steps should have been recorded (n1, n2, n3).
	if len(result.Steps) != 3 {
		t.Errorf("expected 3 steps recorded, got %d", len(result.Steps))
	}
}

// ── ParsePathway / ParsePathwayBytes error paths ──────────────────────────────

// TestParsePathwayFileNotFound verifies that ParsePathway returns an error
// for a nonexistent file path.
func TestParsePathwayFileNotFound(t *testing.T) {
	_, err := pathwalk.ParsePathway("/nonexistent/pathway_file.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file, got nil")
	}
}

// TestParsePathwayBytesInvalidJSON verifies that malformed JSON returns an error.
func TestParsePathwayBytesInvalidJSON(t *testing.T) {
	_, err := pathwalk.ParsePathwayBytes([]byte("not valid json {{{"))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// TestParsePathwayBytesNoStartNode verifies that a valid JSON pathway with no
// node marked isStart:true returns an error (not a nil StartNode).
func TestParsePathwayBytesNoStartNode(t *testing.T) {
	raw := []byte(`{
		"nodes": [{"id":"n1","type":"Default","data":{"name":"Orphan"}}],
		"edges": []
	}`)
	_, err := pathwalk.ParsePathwayBytes(raw)
	if err == nil {
		t.Fatal("expected error when no start node is present, got nil")
	}
}

// ── Engine Run edge cases ─────────────────────────────────────────────────────

// TestRunMissingStartNode verifies that an engine whose Pathway.StartNode is nil
// immediately returns Reason="missing_node". ParsePathwayBytes rejects pathways
// without a start node, so this test constructs the struct directly.
func TestRunMissingStartNode(t *testing.T) {
	pp := &pathwalk.Pathway{
		NodeByID:  make(map[string]*pathwalk.Node),
		EdgesFrom: make(map[string][]*pathwalk.Edge),
	}
	mock := pathwaytest.NewMockLLMClient()
	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Reason != "missing_node" {
		t.Errorf("expected reason=missing_node, got %q", result.Reason)
	}
}

// TestUnsupportedNodeTypeDeadEnd verifies that when an unsupported node type has
// no outgoing edges, the run ends with "dead_end" (not an error).
func TestUnsupportedNodeTypeDeadEnd(t *testing.T) {
	const unknownNoEdgesJSON = `{
  "nodes": [
    { "id": "start", "type": "Default",     "data": { "name": "Start", "isStart": true, "prompt": "go" } },
    { "id": "weird", "type": "UnknownType", "data": { "name": "Weird" } }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "weird", "data": {} }
  ]
}`
	pp := mustParsePathway(t, unknownNoEdgesJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "dead_end" {
		t.Errorf("expected dead_end after unsupported node with no edges, got %q", result.Reason)
	}
}

// TestLLMRouteFallback verifies that when the routing LLM call returns no
// select_route tool call, llmRoute falls back to the first outgoing edge.
func TestLLMRouteFallback(t *testing.T) {
	const multiEdgeJSON = `{
  "nodes": [
    { "id": "start",  "type": "Default",  "data": { "name": "Start", "isStart": true, "prompt": "go" } },
    { "id": "path-a", "type": "End Call", "data": { "name": "PathA", "text": "path-a" } },
    { "id": "path-b", "type": "End Call", "data": { "name": "PathB", "text": "path-b" } }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "path-a", "data": { "label": "A" } },
    { "id": "e2", "source": "start", "target": "path-b", "data": { "label": "B" } }
  ]
}`
	pp := mustParsePathway(t, multiEdgeJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("start", "execute", pathwaytest.MockResponse{Content: "classified"})
	// Route call returns no select_route → fallback to first edge (path-a).
	mock.OnNodePurpose("start", "route", pathwaytest.MockResponse{Content: ""})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := result.Steps[len(result.Steps)-1].NodeName; got != "PathA" {
		t.Errorf("expected fallback to first edge, terminal node %q, got %q", "PathA", got)
	}
}

// TestRouteNodeNoFallback verifies that a Route node with no matching rules and
// no FallbackNodeID causes the run to end with "dead_end".
func TestRouteNodeNoFallback(t *testing.T) {
	const noFallbackJSON = `{
  "nodes": [
    { "id": "start",  "type": "Default", "data": { "name": "Start", "isStart": true, "prompt": "p" } },
    {
      "id": "router", "type": "Route",
      "data": {
        "name": "Router",
        "routes": [{"conditions":[{"field":"x","value":"y","operator":"is"}],"targetNodeId":"end"}]
      }
    },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [
    { "id": "e1", "source": "start",  "target": "router", "data": {} },
    { "id": "e2", "source": "router", "target": "end",    "data": {} }
  ]
}`
	pp := mustParsePathway(t, noFallbackJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// No variables set, condition x=y won't match, no fallback → dead_end.
	if result.Reason != "dead_end" {
		t.Errorf("expected dead_end, got %q", result.Reason)
	}
}

// TestRunRoutingError verifies that when the routing LLM call itself returns an
// error, Run propagates it.
func TestRunRoutingError(t *testing.T) {
	const multiEdgeJSON = `{
  "nodes": [
    { "id": "start",  "type": "Default",  "data": { "name": "Start", "isStart": true, "prompt": "go" } },
    { "id": "path-a", "type": "End Call", "data": { "name": "PathA", "text": "a" } },
    { "id": "path-b", "type": "End Call", "data": { "name": "PathB", "text": "b" } }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "path-a", "data": { "label": "A" } },
    { "id": "e2", "source": "start", "target": "path-b", "data": { "label": "B" } }
  ]
}`
	pp := mustParsePathway(t, multiEdgeJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("start", "execute", pathwaytest.MockResponse{Content: "done"})
	mock.OnNodePurpose("start", "route", pathwaytest.MockResponse{
		Error: errors.New("routing LLM unavailable"),
	})

	_, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error from routing failure, got nil")
	}
}

// TestRunNextNodeNotFound verifies that when the route returns a node ID that
// does not exist in the pathway, Run returns an error.
//
// The Route node must have at least one outgoing edge so that chooseNextNode
// does not short-circuit on "no outgoing edges" before calling evaluateRouteNode.
// evaluateRouteNode then returns the FallbackNodeID ("ghost"), which is absent
// from NodeByID, triggering the not-found error path.
func TestRunNextNodeNotFound(t *testing.T) {
	const badFallbackJSON = `{
  "nodes": [
    { "id": "start",  "type": "Default",  "data": { "name": "Start",  "isStart": true, "prompt": "p" } },
    { "id": "router", "type": "Route",    "data": { "name": "Router", "routes": [], "fallbackNodeId": "ghost" } },
    { "id": "real",   "type": "End Call", "data": { "name": "Real",   "text": "real" } }
  ],
  "edges": [
    { "id": "e1", "source": "start",  "target": "router", "data": {} },
    { "id": "e2", "source": "router", "target": "real",   "data": {} }
  ]
}`
	pp := mustParsePathway(t, badFallbackJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	_, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error when next node is not found in NodeByID, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected error to mention 'ghost', got: %v", err)
	}
}

// ── Global node check edge cases ──────────────────────────────────────────────

// TestCheckGlobalNodeLLMError verifies that a failed global-check LLM call is
// non-fatal: the engine logs a warning and continues executing the current node.
// Using WithVerbose(true) also covers the verbose warn-log path.
func TestCheckGlobalNodeLLMError(t *testing.T) {
	const withGlobalJSON = `{
  "nodes": [
    { "id": "start",   "type": "Default", "data": { "name": "Start",  "isStart": true, "prompt": "go" } },
    { "id": "global1", "type": "Default", "data": { "name": "Cancel", "isGlobal": true, "globalLabel": "Cancel", "prompt": "cancel" } },
    { "id": "end",     "type": "End Call","data": { "name": "Done",   "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "start", "target": "end", "data": {} }]
}`
	pp := mustParsePathway(t, withGlobalJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose(pathwalk.GlobalCheckNodeID, "check_global", pathwaytest.MockResponse{
		Error: errors.New("check_global unavailable"),
	})
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	// WithVerbose covers the "if e.verbose { log warn }" branch inside the error block.
	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithVerbose(true))
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run should not fail on global-check error: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
}

// TestVerboseGlobalInterception verifies that WithVerbose(true) produces the
// "[global] intercepted" log when a global node fires.
func TestVerboseGlobalInterception(t *testing.T) {
	const globalJSON = `{
  "nodes": [
    { "id": "start",     "type": "Default",  "data": { "name": "Start",     "isStart": true, "prompt": "handle" } },
    { "id": "cancel",    "type": "Default",  "data": { "name": "Cancel",    "isGlobal": true, "globalLabel": "Cancel order", "prompt": "cancel" } },
    { "id": "end",       "type": "End Call", "data": { "name": "Done",      "text": "done" } },
    { "id": "cancelled", "type": "End Call", "data": { "name": "Cancelled", "text": "cancelled" } }
  ],
  "edges": [
    { "id": "e1", "source": "start",  "target": "end",       "data": {} },
    { "id": "e2", "source": "cancel", "target": "cancelled", "data": {} }
  ]
}`
	pp := mustParsePathway(t, globalJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose(pathwalk.GlobalCheckNodeID, "check_global", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "select_global_node", Args: map[string]any{"node": float64(1)}},
		},
	})
	mock.OnNode("cancel", pathwaytest.MockResponse{Content: "Cancelled."})
	mock.SetDefault(pathwaytest.MockResponse{Content: ""})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithVerbose(true))
	result, err := engine.Run(context.Background(), "cancel my order")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := result.Steps[len(result.Steps)-1].NodeName; got != "Cancelled" {
		t.Errorf("expected terminal node name %q, got %q", "Cancelled", got)
	}
}

// ── Webhook edge cases ────────────────────────────────────────────────────────

// TestWebhookNonJSONResponse verifies that a plain-text HTTP response is stored
// as a string in the node output rather than causing an error.
func TestWebhookNonJSONResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "plain text response")
	}))
	defer ts.Close()

	webhookJSON := fmt.Sprintf(`{
  "nodes": [
    { "id": "wh",  "type": "Webhook",  "data": { "name": "Fetch", "isStart": true, "url": %q, "method": "GET" } },
    { "id": "end", "type": "End Call", "data": { "name": "Done",  "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "wh", "target": "end", "data": {} }]
}`, ts.URL)

	pp := mustParsePathway(t, webhookJSON)
	result, err := pathwalk.NewEngine(pp, pathwaytest.NewMockLLMClient()).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if !strings.Contains(result.Steps[0].Output, "plain text response") {
		t.Errorf("expected plain text in output, got %q", result.Steps[0].Output)
	}
}

// TestWebhookCustomHeaders verifies that headers defined in the node data are
// forwarded in the HTTP request.
func TestWebhookCustomHeaders(t *testing.T) {
	var gotHeader string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Api-Token")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()

	webhookJSON := fmt.Sprintf(`{
  "nodes": [
    {
      "id": "wh", "type": "Webhook",
      "data": {
        "name": "Req", "isStart": true,
        "url": %q, "method": "POST",
        "headers": {"X-Api-Token": "secret123"}
      }
    },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "wh", "target": "end", "data": {} }]
}`, ts.URL)

	pp := mustParsePathway(t, webhookJSON)
	_, err := pathwalk.NewEngine(pp, pathwaytest.NewMockLLMClient()).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gotHeader != "secret123" {
		t.Errorf("expected X-Api-Token=secret123, got %q", gotHeader)
	}
}

// ── LLM execution edge cases ──────────────────────────────────────────────────

// TestExtractVarsIntegerBooleanTypes verifies that extractVars correctly builds
// the JSON schema for "integer" and "boolean" variable types (those switch
// branches exist so tools can receive properly typed schemas).
func TestExtractVarsIntegerBooleanTypes(t *testing.T) {
	const pathwayJSONStr = `{
  "nodes": [
    {
      "id": "n1", "type": "Default",
      "data": {
        "name": "Extract", "isStart": true, "prompt": "extract",
        "extractVars": [
          ["count",  "integer", "item count",  false],
          ["active", "boolean", "active flag", false]
        ]
      }
    },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "n1", "target": "end", "data": {} }]
}`
	pp := mustParsePathway(t, pathwayJSONStr)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("n1", "execute", pathwaytest.MockResponse{Content: "count=3, active=true"})
	mock.OnNodePurpose("n1", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"count": float64(3), "active": true}},
		},
	})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Variables["count"] != float64(3) {
		t.Errorf("expected count=3, got %v", result.Variables["count"])
	}
}

// TestExtractVarsLLMError verifies that when the extract_vars LLM call fails,
// the error is non-fatal: the run completes normally but no variables are stored.
func TestExtractVarsLLMError(t *testing.T) {
	const pathwayJSONStr = `{
  "nodes": [
    {
      "id": "n1", "type": "Default",
      "data": {
        "name": "Extract", "isStart": true, "prompt": "extract",
        "extractVars": [["status", "string", "status", false]]
      }
    },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "n1", "target": "end", "data": {} }]
}`
	pp := mustParsePathway(t, pathwayJSONStr)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("n1", "execute", pathwaytest.MockResponse{Content: "output text"})
	mock.OnNodePurpose("n1", "extract_vars", pathwaytest.MockResponse{
		Error: errors.New("LLM unavailable"),
	})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if _, ok := result.Variables["status"]; ok {
		t.Error("expected no variables extracted after error, but status was set")
	}
}

// TestChannelDirectiveExecution verifies that when the LLM returns a
// <|channel|> directive (no native tool calls), the matching registered tool
// is invoked. WithVerbose also covers the channel directive verbose log lines.
func TestChannelDirectiveExecution(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()

	toolCalled := false
	channelTool := pathwalk.Tool{
		Name:        "my_action",
		Description: "action via channel directive",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			toolCalled = true
			return map[string]any{"done": true}, nil
		},
	}
	mock.OnNode("n1", pathwaytest.MockResponse{
		// No ToolCalls — triggers the channel directive parsing path.
		Content: `<|channel|>to=my_action<|message|>{"key":"value"}`,
	})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithVerbose(true), pathwalk.WithTools(channelTool))
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !toolCalled {
		t.Error("expected my_action to be called via channel directive")
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
}

// TestChannelDirectiveToolError verifies that a tool invoked via a channel
// directive is non-fatal when it returns an error: the run still completes.
func TestChannelDirectiveToolError(t *testing.T) {
	pp := mustParsePathway(t, minimalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()

	errorTool := pathwalk.Tool{
		Name:        "fail_tool",
		Description: "always fails",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			return nil, errors.New("tool always fails")
		},
	}
	mock.OnNode("n1", pathwaytest.MockResponse{
		Content: `<|channel|>to=fail_tool<|message|>{}`,
	})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithTools(errorTool))
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run should not fail on channel tool error: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
}

// TestVerboseLLMWithTemperatureAndToolCalls verifies that the verbose log paths
// for node temperature and tool-call results execute without error.
func TestVerboseLLMWithTemperatureAndToolCalls(t *testing.T) {
	const hotNodeJSON = `{
  "nodes": [
    {
      "id": "n1", "type": "Default",
      "data": {
        "name": "HotNode", "isStart": true, "prompt": "do hot stuff",
        "modelOptions": { "newTemperature": 0.9 }
      }
    },
    { "id": "end", "type": "End Call", "data": { "name": "Done", "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "n1", "target": "end", "data": {} }]
}`
	pp := mustParsePathway(t, hotNodeJSON)
	mock := pathwaytest.NewMockLLMClient()

	noopTool := pathwalk.Tool{
		Name:        "noop",
		Description: "no-op",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		Fn:          func(ctx context.Context, args map[string]any) (any, error) { return "ok", nil },
	}
	mock.OnNode("n1", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{{Name: "noop", Args: map[string]any{}}},
		Content:   "done",
	})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithVerbose(true), pathwalk.WithTools(noopTool))
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
}

// TestVerboseExtractVars verifies that when verbose mode is on and variables are
// successfully extracted, the "[vars] extracted" log path executes.
func TestVerboseExtractVars(t *testing.T) {
	pp := mustParsePathway(t, extractVarsPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("classify", "execute", pathwaytest.MockResponse{Content: "inventory request"})
	mock.OnNodePurpose("classify", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"operation_type": "inventory_mgmt"}},
		},
	})

	engine := pathwalk.NewEngine(pp, mock, pathwalk.WithVerbose(true))
	result, err := engine.Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if result.Variables["operation_type"] != "inventory_mgmt" {
		t.Errorf("expected operation_type=inventory_mgmt, got %v", result.Variables["operation_type"])
	}
}

// TestParsePathwayInvalidExtractVars verifies that malformed extractVars tuples
// (wrong type or too few elements) are silently skipped via the continue branch
// in ParsePathwayBytes.
func TestParsePathwayInvalidExtractVars(t *testing.T) {
	// 123 is not a JSON array → json.Unmarshal fails → continue
	// ["a","b"] has only 2 elements → len(tuple) < 3 → continue
	// ["my_var","string","desc"] is valid and should be preserved
	raw := `{
  "nodes": [
    {
      "id": "n1",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "do something",
        "extractVars": [123, ["a","b"], ["my_var","string","description"]]
      }
    },
    {"id": "end", "type": "End Call", "data": {"name": "Done", "text": "done"}}
  ],
  "edges": [{"id": "e1", "source": "n1", "target": "end", "data": {}}]
}`
	pp, err := pathwalk.ParsePathwayBytes([]byte(raw))
	if err != nil {
		t.Fatalf("ParsePathwayBytes: %v", err)
	}
	n := pp.NodeByID["n1"]
	if len(n.ExtractVars) != 1 {
		t.Errorf("expected 1 valid extractVar, got %d", len(n.ExtractVars))
	}
	if len(n.ExtractVars) > 0 && n.ExtractVars[0].Name != "my_var" {
		t.Errorf("expected name=my_var, got %q", n.ExtractVars[0].Name)
	}
}

// TestParsePathwayWebhookDefaultMethod verifies that a Webhook node with no
// "method" field in JSON gets WebhookMethod defaulted to "POST" at parse time.
func TestParsePathwayWebhookDefaultMethod(t *testing.T) {
	raw := `{
  "nodes": [
    {"id": "start", "type": "Default", "data": {"name": "Start", "isStart": true, "prompt": "go"}},
    {"id": "wh", "type": "Webhook", "data": {"name": "Webhook", "url": "http://example.com/"}},
    {"id": "end", "type": "End Call", "data": {"name": "Done", "text": "done"}}
  ],
  "edges": [
    {"id": "e1", "source": "start", "target": "wh", "data": {}},
    {"id": "e2", "source": "wh", "target": "end", "data": {}}
  ]
}`
	pp, err := pathwalk.ParsePathwayBytes([]byte(raw))
	if err != nil {
		t.Fatalf("ParsePathwayBytes: %v", err)
	}
	wh := pp.NodeByID["wh"]
	if wh.WebhookMethod != "POST" {
		t.Errorf("expected WebhookMethod=POST after parse, got %q", wh.WebhookMethod)
	}
}

// TestLLMRouteEmptyEdgeLabel verifies that edges with no label cause llmRoute
// to fall back to the "Route N" placeholder format.
func TestLLMRouteEmptyEdgeLabel(t *testing.T) {
	const raw = `{
  "nodes": [
    {"id": "n1", "type": "Default", "data": {"name": "Decide", "isStart": true, "prompt": "pick"}},
    {"id": "path-a", "type": "End Call", "data": {"name": "A", "text": "took A"}},
    {"id": "path-b", "type": "End Call", "data": {"name": "B", "text": "took B"}}
  ],
  "edges": [
    {"id": "e1", "source": "n1", "target": "path-a", "data": {}},
    {"id": "e2", "source": "n1", "target": "path-b", "data": {}}
  ]
}`
	pp := mustParsePathway(t, raw)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("n1", "execute", pathwaytest.MockResponse{Content: "decided"})
	mock.OnNodePurpose("n1", "route", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "select_route", Args: map[string]any{"route": 1, "reason": "first is best"}},
		},
	})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := result.Steps[len(result.Steps)-1].NodeName; got != "A" {
		t.Errorf("expected terminal node name %q, got %q", "A", got)
	}
}

// TestLLMRouteNoReason verifies that a select_route tool call without a "reason"
// field causes llmRoute to use the fallback "selected route N" string.
func TestLLMRouteNoReason(t *testing.T) {
	const raw = `{
  "nodes": [
    {"id": "n1", "type": "Default", "data": {"name": "Decide", "isStart": true, "prompt": "pick"}},
    {"id": "path-a", "type": "End Call", "data": {"name": "A", "text": "took A"}},
    {"id": "path-b", "type": "End Call", "data": {"name": "B", "text": "took B"}}
  ],
  "edges": [
    {"id": "e1", "source": "n1", "target": "path-a", "data": {"label": "option-a"}},
    {"id": "e2", "source": "n1", "target": "path-b", "data": {"label": "option-b"}}
  ]
}`
	pp := mustParsePathway(t, raw)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNodePurpose("n1", "execute", pathwaytest.MockResponse{Content: "decided"})
	// No "reason" key → covers the `if r == ""` fallback in llmRoute.
	mock.OnNodePurpose("n1", "route", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "select_route", Args: map[string]any{"route": 2}},
		},
	})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := result.Steps[len(result.Steps)-1].NodeName; got != "B" {
		t.Errorf("expected terminal node name %q, got %q", "B", got)
	}
}

// TestCheckGlobalNodeSelectZero verifies that when select_global_node returns
// node:0 the engine continues normal execution (covers return nil, nil inside
// the for loop in checkGlobalNode).
func TestCheckGlobalNodeSelectZero(t *testing.T) {
	const withGlobalJSON = `{
  "nodes": [
    { "id": "start",   "type": "Default", "data": { "name": "Start",  "isStart": true, "prompt": "go" } },
    { "id": "global1", "type": "Default", "data": { "name": "Cancel", "isGlobal": true, "globalLabel": "Cancel", "prompt": "cancel" } },
    { "id": "end",     "type": "End Call","data": { "name": "Done",   "text": "done" } }
  ],
  "edges": [{ "id": "e1", "source": "start", "target": "end", "data": {} }]
}`
	pp := mustParsePathway(t, withGlobalJSON)
	mock := pathwaytest.NewMockLLMClient()
	// node:0 means "no global node" → covers the `return nil, nil` branch inside
	// the select_global_node for-loop when idx < 1.
	mock.OnNodePurpose(pathwalk.GlobalCheckNodeID, "check_global", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "select_global_node", Args: map[string]any{"node": float64(0)}},
		},
	})
	mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

	result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "test")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Reason != "terminal" {
		t.Errorf("expected terminal, got %q", result.Reason)
	}
	if result.Output != "ok" {
		t.Errorf("expected output=ok (last LLM step output), got %q", result.Output)
	}
}
