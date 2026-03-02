# aipathwayengine

A Go library and CLI that executes [Bland AI](https://www.bland.ai/)-style conversational pathway JSON files as agentic pipelines. Define your workflow as a graph of nodes and edges; the engine walks the graph, calls your LLM at each step, extracts variables, and routes to the next node automatically.

## Installation

```bash
go get github.com/wricardo/aipathwayengine
```

## CLI

Build and run a pathway from the command line:

```bash
go build ./cmd/aipathway/

./aipathway run \
  --pathway examples/pizzeria_ops.json \
  --task "Create an order for John: 2x Margherita" \
  --model gpt-4o \
  --api-key $OPENAI_API_KEY \
  --verbose
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--pathway`, `-p` | required | Path to the pathway JSON file |
| `--task`, `-t` | required | Initial task description |
| `--model` | `gpt-4o` | LLM model name |
| `--api-key` | `$OPENAI_API_KEY` | API key |
| `--base-url` | `$OPENAI_BASE_URL` | Base URL (for OpenAI-compatible APIs) |
| `--max-steps` | `50` | Maximum nodes to traverse |
| `--verbose`, `-v` | false | Print each step and routing decision |
| `--graphql-endpoint` | `$GRAPHQL_ENDPOINT` | Enables the built-in `graphql` tool |
| `--graphql-header` | | Extra HTTP headers (`Key=Value`, repeatable) |

## Library usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    aipe "github.com/wricardo/aipathwayengine"
)

func main() {
    pathway, err := aipe.ParsePathway("my_pathway.json")
    if err != nil {
        log.Fatal(err)
    }

    llm := aipe.NewOpenAIClient(apiKey, "", "gpt-4o")

    engine := aipe.NewEngine(pathway, llm,
        aipe.WithMaxSteps(30),
        aipe.WithVerbose(true),
    )

    result, err := engine.Run(context.Background(), "My task description")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(result.Output)
    fmt.Println(result.Variables)
}
```

### Adding tools

```go
myTool := aipe.Tool{
    Name:        "lookup_user",
    Description: "Look up a user by email",
    Parameters: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "email": map[string]any{"type": "string"},
        },
        "required": []string{"email"},
    },
    Fn: func(ctx context.Context, args map[string]any) (any, error) {
        // your implementation
        return map[string]any{"id": "123", "name": "Alice"}, nil
    },
}

engine := aipe.NewEngine(pathway, llm, aipe.WithTools(myTool))
```

The built-in GraphQL tool is available in `tools/graphql.go`:

```go
import "github.com/wricardo/aipathwayengine/tools"

gt := &tools.GraphQLTool{
    Endpoint: "http://localhost:4000/graphql",
    Headers:  map[string]string{"Authorization": "Bearer " + token},
}
engine := aipe.NewEngine(pathway, llm, aipe.WithTools(gt.AsTool()))
```

### RunResult

```go
type RunResult struct {
    Output    string         // final text output
    Variables map[string]any // accumulated extracted variables
    Steps     []StepLog      // one entry per visited node
    Reason    string         // "end_call" | "max_steps" | "error" | "dead_end" | "null_node"
}
```

## Pathway JSON format

Pathways are JSON files with `nodes` and `edges` arrays, compatible with the Bland AI export format.

### Node types

**`Default`** — runs an LLM prompt, optionally extracts variables, then routes to the next node.

```json
{
  "id": "classify",
  "type": "Default",
  "data": {
    "name": "Classify Request",
    "isStart": true,
    "prompt": "Classify the incoming request.",
    "condition": "Exit when classification is complete.",
    "extractVars": [
      ["operation_type", "string", "The operation category", true]
    ],
    "modelOptions": { "newTemperature": 0.1 }
  }
}
```

`extractVars` tuple: `[name, type, description, required]`
Supported types: `"string"`, `"integer"`, `"boolean"`

**`Route`** — branches based on extracted variables (no LLM call).

```json
{
  "id": "router",
  "type": "Route",
  "data": {
    "name": "Route to Handler",
    "routes": [
      {
        "conditions": [{ "field": "operation_type", "value": "orders", "operator": "is" }],
        "targetNodeId": "orders-node"
      }
    ],
    "fallbackNodeId": "end"
  }
}
```

Supported operators: `"is"`, `"is not"`, `"contains"`, `"not contains"`, `">"`, `"<"`, `">="`, `"<="`

Multiple conditions within a rule are AND-ed; rules are evaluated in order.

**`End Call`** — terminal node; returns `text` as the run output.

```json
{
  "id": "end",
  "type": "End Call",
  "data": { "name": "Done", "text": "Operation complete." }
}
```

**`Webhook`** — makes an HTTP request; supports `{{variable}}` placeholders in the body.

```json
{
  "id": "notify",
  "type": "Webhook",
  "data": {
    "name": "Notify",
    "url": "https://example.com/hook",
    "method": "POST",
    "headers": { "Authorization": "Bearer token" },
    "body": { "customer": "{{customer_name}}" },
    "extractVars": [["order_id", "string", "Created order ID", true]]
  }
}
```

### Edges

```json
{
  "id": "e1",
  "source": "classify",
  "target": "router",
  "data": { "label": "continue", "description": "When classification is done" }
}
```

When a Default node has multiple outgoing edges, the LLM picks the route using the edge labels and descriptions as options.

## Testing

`MockLLMClient` lets you script LLM responses without network calls:

```go
mock := aipe.NewMockLLMClient()

// Match by node ID
mock.OnNode("n1", aipe.MockResponse{Content: "Hello!"})

// Match by node ID + call purpose ("execute", "extract_vars", or "route")
mock.OnNodePurpose("classify", "extract_vars", aipe.MockResponse{
    ToolCalls: []aipe.MockToolCall{
        {Name: "set_variables", Args: map[string]any{"operation_type": "orders"}},
    },
})

// Fallback for any unmatched call
mock.SetDefault(aipe.MockResponse{Content: "ok"})

engine := aipe.NewEngine(pathway, mock)
result, err := engine.Run(ctx, "test task")

// Assertions
mock.CallCount("n1")  // number of LLM calls for that node
mock.Calls            // []RecordedCall — full call log
```

Run the tests:

```bash
go test ./...
```
