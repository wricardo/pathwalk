# Testing with MockLLMClient

All pathwalk tests use `MockLLMClient` from `github.com/wricardo/pathwalk/pathwaytest`. No real LLM calls are made.

## Import

```go
import "github.com/wricardo/pathwalk/pathwaytest"
```

## Basic Setup

```go
mock := pathwaytest.NewMockLLMClient()
engine := pathwalk.NewEngine(pathway, mock)
```

## Scripting Responses

### By node ID (matches any call purpose)

```go
mock.OnNode("greet", pathwaytest.MockResponse{Content: "Hello! What can I help you with?"})
```

### By node ID + call purpose (more specific — wins over OnNode)

```go
// Script the "execute" call
mock.OnNodePurpose("classify", "execute", pathwaytest.MockResponse{
    Content: "I'll classify that for you.",
})

// Script the "extract_vars" call — return a set_variables tool call
mock.OnNodePurpose("classify", "extract_vars", pathwaytest.MockResponse{
    ToolCalls: []pathwaytest.MockToolCall{
        {Name: "set_variables", Args: map[string]any{
            "intent": "order",
            "count":  3,
        }},
    },
})

// Script the "route" call — return a select_route tool call
mock.OnNodePurpose("router-node", "route", pathwaytest.MockResponse{
    ToolCalls: []pathwaytest.MockToolCall{
        {Name: "select_route", Args: map[string]any{"route": float64(2)}},
    },
})
```

### Fallback for unmatched calls

```go
mock.SetDefault(pathwaytest.MockResponse{Content: "default response"})
```

### Scripting errors

```go
mock.OnNode("flaky", pathwaytest.MockResponse{
    Error: errors.New("LLM unavailable"),
})
```

### Multiple responses (consumed in order)

```go
mock.OnNode("n1", pathwaytest.MockResponse{Content: "First visit"})
mock.OnNode("n1", pathwaytest.MockResponse{Content: "Second visit"})
// After both are consumed, falls through to SetDefault
```

## Call Purposes

| Purpose | When it fires |
|---------|---------------|
| `"execute"` | Main LLM call for every LLM node |
| `"extract_vars"` | Second call when node has `extractVars` |
| `"route"` | Third call when node has >1 outgoing edge |
| `"check_global"` | Before every step when global nodes exist; node ID is `pathwalk.GlobalCheckNodeID` (`"$global_check"`) |

## Assertions

```go
// Count calls for a node
mock.CallCount("greet") // int

// Inspect full call log
for _, call := range mock.Calls {
    fmt.Println(call.NodeID, call.Purpose)
    // call.Request holds the full CompletionRequest
}
```

## Complete Test Example

```go
func TestOrderFlow(t *testing.T) {
    const pathwayJSON = `{
      "nodes": [
        {"id": "greet",  "type": "Default",  "data": {"name": "Greet",  "isStart": true, "prompt": "...",
          "extractVars": [["intent","string","what user wants",true]]}},
        {"id": "orders", "type": "Default",  "data": {"name": "Orders", "prompt": "..."}},
        {"id": "done",   "type": "End Call", "data": {"name": "Done",   "text": "Goodbye!"}}
      ],
      "edges": [
        {"id": "e1", "source": "greet",  "target": "orders", "data": {}},
        {"id": "e2", "source": "orders", "target": "done",   "data": {}}
      ]
    }`

    pp, err := pathwalk.ParsePathwayBytes([]byte(pathwayJSON))
    if err != nil {
        t.Fatal(err)
    }

    mock := pathwaytest.NewMockLLMClient()
    mock.OnNodePurpose("greet", "execute", pathwaytest.MockResponse{
        Content: "Hi! What can I help with?",
    })
    mock.OnNodePurpose("greet", "extract_vars", pathwaytest.MockResponse{
        ToolCalls: []pathwaytest.MockToolCall{
            {Name: "set_variables", Args: map[string]any{"intent": "order"}},
        },
    })
    mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

    result, err := pathwalk.NewEngine(pp, mock).Run(context.Background(), "I want to order a pizza")
    if err != nil {
        t.Fatalf("Run: %v", err)
    }
    if result.Reason != "terminal" {
        t.Errorf("expected terminal, got %q", result.Reason)
    }
    if result.Variables["intent"] != "order" {
        t.Errorf("expected intent=order, got %v", result.Variables["intent"])
    }
}
```

## Parsing Helpers

Tests commonly use a `mustParsePathway` helper:

```go
func mustParsePathway(t *testing.T, raw string) *pathwalk.Pathway {
    t.Helper()
    pp, err := pathwalk.ParsePathwayBytes([]byte(raw))
    if err != nil {
        t.Fatalf("ParsePathwayBytes: %v", err)
    }
    return pp
}
```

## What to Test

| Scenario | How |
|----------|-----|
| Normal happy path | `OnNode` / `SetDefault` for all nodes, assert `Reason == "terminal"` |
| Variable extraction | `OnNodePurpose(..., "extract_vars", ...)` with `set_variables` tool call, assert `result.Variables` |
| LLM routing | `OnNodePurpose(..., "route", ...)` with `select_route` tool call, assert `result.Steps` path |
| Route node conditions | Set `state.Variables` before `Step()`, assert next node |
| LLM failure | `MockResponse{Error: ...}`, assert `result.Reason == "error"` |
| Max visits | Visit a node repeatedly, assert `"max_node_visits"` |
| Global node fires | `OnNodePurpose(pathwalk.GlobalCheckNodeID, "check_global", ...)` |
