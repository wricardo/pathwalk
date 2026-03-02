# pathwalk — Full Documentation

`pathwalk` is a Go library (and CLI) that executes conversational pathway JSON files as agentic pipelines. A pathway is a directed graph of nodes connected by edges. The engine walks the graph step-by-step, calling an LLM, evaluating conditions, or making HTTP requests at each node, until it reaches a terminal node or an exit condition.

**Module path:** `github.com/wricardo/pathwalk`

---

## Table of Contents

1. [Quick Start](#quick-start)
2. [Architecture](#architecture)
3. [Pathway JSON Format](#pathway-json-format)
4. [Node Types](#node-types)
5. [API Reference](#api-reference)
6. [Tools](#tools)
7. [Testing with MockLLMClient](#testing-with-mockllmclient)
8. [CLI](#cli)
9. [File Layout](#file-layout)

---

## Quick Start

```go
import pathwalk "github.com/wricardo/pathwalk"

// 1. Parse the pathway JSON
pw, err := pathwalk.ParsePathway("examples/pizzeria_ops.json")

// 2. Create an LLM client
llm := pathwalk.NewOpenAIClient(apiKey, "", "gpt-4o")

// 3. Create and run the engine
engine := pathwalk.NewEngine(pw, llm)
result, err := engine.Run(ctx, "Create an order for John: 2x Margherita")

fmt.Println(result.Output)     // terminal node text
fmt.Println(result.Reason)     // "terminal"
fmt.Println(result.Variables)  // extracted variables
```

---

## Architecture

### Execution Flow

```
ParsePathway(file) → *Pathway
NewEngine(pathway, llm, opts...) → *Engine
engine.Run(ctx, task) → *RunResult
```

Inside `Run`, the engine walks nodes in a loop (up to `maxSteps`, default 50):

1. Dispatch to the node executor based on `node.Type`.
2. Apply extracted variables to shared state.
3. Route to the next node (single edge → follow it; multiple edges → LLM picks; Route node → condition eval).
4. Record the step.
5. Stop on terminal node, dead end, or max steps.

### Per-Node Execution (`executor.go`)

| Node Type | What happens |
|-----------|-------------|
| `NodeTypeLLM` ("llm") | Three LLM calls: `"execute"` (main action), `"extract_vars"` (structured variable extraction via `set_variables` tool call if `extractVars` is set), `"route"` (when multiple outgoing edges, LLM picks via `select_route` tool call) |
| `NodeTypeTerminal` ("terminal") | Returns `node.TerminalText` as the final output — terminates the run |
| `NodeTypeWebhook` ("webhook") | HTTP call with `{{variable}}` template substitution in body; extracts variables from JSON response |
| `NodeTypeRoute` ("route") | Pure-Go condition evaluation against `state.Variables` — no LLM call |

### Routing (`router.go`)

- **Single outgoing edge** → follow it automatically.
- **Multiple edges on LLM/Webhook nodes** → LLM `select_route` function call picks one.
- **Route node** → evaluate `RouteRule` conditions (AND logic) in order; first match wins; `FallbackNodeID` if none match.

### State

Each run maintains a `State` with:
- `Task string` — the initial task string.
- `Variables map[string]any` — accumulated extracted variables.
- `Steps []Step` — history of visited nodes.

### Context Keys

Two context keys control mock behavior in tests:

| Key | Constant | Values |
|-----|----------|--------|
| `"nodeID"` | `NodeIDContextKey` | node ID string |
| `"callPurpose"` | `CallPurposeContextKey` | `"execute"`, `"extract_vars"`, `"route"` |

Helper functions: `WithNodeID`, `NodeIDFromContext`, `WithCallPurpose`, `CallPurposeFromContext`.

---

## Pathway JSON Format

Pathways are JSON files (Bland AI export format) with `nodes` and `edges` arrays plus an optional `graphqlEndpoint`.

```json
{
  "graphqlEndpoint": "http://localhost:4000/graphql",
  "nodes": [...],
  "edges": [...]
}
```

### Node Object

```json
{
  "id": "unique-node-id",
  "type": "Default",
  "data": {
    "name": "Human-readable name",
    "isStart": true,
    "prompt": "Instructions for the LLM.",
    "condition": "Exit condition hint for routing decisions.",
    "extractVars": [
      ["variable_name", "string", "description", true]
    ],
    "modelOptions": { "newTemperature": 0.2 }
  }
}
```

**`type` values in JSON** (normalized to internal constants by the parser):

| JSON type | Internal constant |
|-----------|------------------|
| `"Default"` | `NodeTypeLLM` |
| `"End Call"` | `NodeTypeTerminal` |
| `"Webhook"` | `NodeTypeWebhook` |
| `"Route"` | `NodeTypeRoute` |

### `extractVars` Tuple Format

Each element is a 4-element JSON array: `[name, type, description, required]`

- `name` — variable name.
- `type` — `"string"`, `"integer"`, or `"boolean"`.
- `description` — used in the extraction prompt.
- `required` — `true`/`false`; adds field to JSON schema `required` list.

### Route Node

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
    "fallbackNodeId": "fallback-node"
  }
}
```

**Condition operators:** `"is"`, `"is not"`, `"contains"`, `"not contains"`, `">"`, `"<"`, `">="`, `"<="`

All conditions in a rule are evaluated with AND logic. Rules are checked in order; first match wins.

### Webhook Node

```json
{
  "id": "webhook-node",
  "type": "Webhook",
  "data": {
    "name": "Call API",
    "url": "https://api.example.com/orders",
    "method": "POST",
    "headers": { "Authorization": "Bearer {{token}}" },
    "body": { "customer": "{{customer_name}}", "amount": "{{total}}" },
    "extractVars": [
      ["order_id", "string", "Created order ID", true]
    ]
  }
}
```

`{{variable}}` placeholders in `body` and `headers` values are replaced with current state variables before the request is made. Variables are extracted from the JSON response by matching top-level keys.

### Edge Object

```json
{
  "id": "edge-id",
  "source": "source-node-id",
  "target": "target-node-id",
  "data": {
    "label": "Route label",
    "description": "Description shown to the LLM when choosing this route"
  }
}
```

---

## Node Types

### `NodeTypeLLM` — LLM Node

The main workhorse. Three LLM calls happen per visit:

1. **`"execute"`** — builds a system prompt from `node.Prompt` (falling back to `node.Text`) and `node.Condition`, then calls the LLM with any registered tools. The `<|channel|>...<|message|>` directive format is also parsed for models that don't support function calling natively.

2. **`"extract_vars"`** — if `node.ExtractVars` is non-empty, calls the LLM again with a `set_variables` tool to extract structured variables from the execute output.

3. **`"route"`** — if there are multiple outgoing edges, calls the LLM with a `select_route` tool to pick the next node.

### `NodeTypeTerminal` — Terminal Node

Returns `node.TerminalText` immediately. Sets `RunResult.Reason = "terminal"`.

### `NodeTypeRoute` — Route Node

Evaluates `node.Routes` conditions against `state.Variables` in order. No LLM call is made. Falls back to `node.FallbackNodeID` if no rule matches.

### `NodeTypeWebhook` — Webhook Node

Makes an HTTP request. Method defaults to `POST` if not specified. Template substitution is applied to the request body. Variables are extracted from the top-level keys of the JSON response.

---

## API Reference

### Parsing

```go
// Parse from file path
pw, err := pathwalk.ParsePathway("pathway.json")

// Parse from bytes
pw, err := pathwalk.ParsePathwayBytes(jsonBytes)
```

Returns `*Pathway`:

```go
type Pathway struct {
    Nodes           []*Node
    Edges           []*Edge
    NodeByID        map[string]*Node
    EdgesFrom       map[string][]*Edge // source nodeID → outgoing edges
    StartNode       *Node
    GlobalNodes     []*Node            // nodes with IsGlobal == true
    GraphQLEndpoint string             // optional default GraphQL endpoint
}
```

### Engine

```go
engine := pathwalk.NewEngine(pw, llm,
    pathwalk.WithTools(myTool),
    pathwalk.WithMaxSteps(100),
    pathwalk.WithVerbose(true),
)
result, err := engine.Run(ctx, "task description")
```

**Options:**

| Option | Default | Description |
|--------|---------|-------------|
| `WithTools(tools ...Tool)` | none | Register tools the LLM can call |
| `WithMaxSteps(n int)` | 50 | Maximum nodes to visit before stopping |
| `WithVerbose(v bool)` | false | Log each step to stderr |

### RunResult

```go
type RunResult struct {
    Output     string         // final output text (terminal node text or last LLM output)
    Variables  map[string]any // accumulated extracted variables
    Steps      []Step         // history of visited nodes
    Reason     string         // why the run ended
    FailedNode string         // node name when Reason is "error"
}
```

**`Reason` values:**

| Value | Meaning |
|-------|---------|
| `"terminal"` | Reached a terminal (End Call) node |
| `"max_steps"` | Hit the step limit |
| `"error"` | Node execution or routing returned an error |
| `"dead_end"` | No outgoing edges and no terminal |
| `"missing_node"` | `currentNode` was nil (internal error) |

### Step

```go
type Step struct {
    NodeID      string
    NodeName    string
    Output      string         // LLM or webhook output text
    Vars        map[string]any // variables extracted at this step
    ToolCalls   []ToolCall     // tools invoked during execution
    RouteReason string         // why this route was taken
    NextNode    string         // node ID of the next node
}
```

### Tool

```go
type Tool struct {
    Name        string
    Description string
    Parameters  map[string]any                                       // JSON schema
    Fn          func(ctx context.Context, args map[string]any) (any, error)
}
```

### LLMClient Interface

```go
type LLMClient interface {
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

type CompletionRequest struct {
    Model       string
    Messages    []Message
    Tools       []Tool
    Temperature float64
    MaxTokens   int
}

type CompletionResponse struct {
    Content   string
    ToolCalls []ToolCall
}
```

### OpenAIClient

```go
llm := pathwalk.NewOpenAIClient(
    apiKey,   // or "" to use OPENAI_API_KEY env var
    baseURL,  // or "" for default; set to use Groq, Ollama, OpenRouter, etc.
    model,    // e.g. "gpt-4o", "gpt-4o-mini"
)
```

Handles the tool-call loop internally (up to 10 rounds).

---

## Tools

The `tools` sub-package provides `GraphQLTool`, which exposes six tools to the LLM for exploring and calling a GraphQL API.

```go
import "github.com/wricardo/pathwalk/tools"

gt := &tools.GraphQLTool{
    Endpoint: "http://localhost:4000/graphql",
    Headers:  map[string]string{"Authorization": "Bearer ..."},
}

engine := pathwalk.NewEngine(pw, llm, pathwalk.WithTools(gt.AsTools()...))
```

**Exposed tools:**

| Tool | Description |
|------|-------------|
| `graphql_query` | Execute a GraphQL query |
| `graphql_mutation` | Execute a GraphQL mutation |
| `graphql_queries` | List available queries with signatures (supports `filter` arg) |
| `graphql_mutations` | List available mutations with signatures (supports `filter` arg) |
| `graphql_types` | List all named non-scalar types (supports `filter` arg) |
| `graphql_type` | Describe a type with fields expanded 2 levels deep |

The schema introspection tools help the LLM discover what operations are available before constructing queries.

---

## Testing with MockLLMClient

No real LLM calls are needed in tests. The root package exports `MockLLMClient`.

```go
mock := pathwalk.NewMockLLMClient()

// Match by node ID only (any call purpose)
mock.OnNode("n1", pathwalk.MockResponse{Content: "Hello!"})

// Match by node ID + call purpose (more specific, wins over OnNode)
mock.OnNodePurpose("classify", "execute", pathwalk.MockResponse{
    Content: "This is a reporting request.",
})
mock.OnNodePurpose("classify", "extract_vars", pathwalk.MockResponse{
    ToolCalls: []pathwalk.MockToolCall{
        {Name: "set_variables", Args: map[string]any{"operation_type": "reporting"}},
    },
})

// Fallback for any unmatched call
mock.SetDefault(pathwalk.MockResponse{Content: "ok"})

engine := pathwalk.NewEngine(pw, mock)
result, _ := engine.Run(ctx, "task")

// Assertions
mock.CallCount("classify")  // number of LLM calls for that node
mock.Calls                  // []RecordedCall — full call log
```

**`RecordedCall` fields:**
- `NodeID string` — from context
- `Purpose string` — `"execute"`, `"extract_vars"`, or `"route"`
- `Request CompletionRequest` — full request including messages and tools

**`MockResponse` fields:**
- `Content string` — text returned as `CompletionResponse.Content`
- `ToolCalls []MockToolCall` — tool calls the mock will execute (tool `Fn`s are called)
- `Error error` — if non-nil, `Complete` returns this error

Multiple `OnNode`/`OnNodePurpose` calls for the same key are consumed in order (FIFO queue).

### External pathwaytest package

For external test packages, `pathwaytest` provides the same mock under a separate import:

```go
import "github.com/wricardo/pathwalk/pathwaytest"

mock := pathwaytest.NewMockLLMClient()
mock.OnNode("n1", pathwaytest.MockResponse{Content: "Hello!"})
```

---

## CLI

Build:

```bash
go build ./cmd/pathwalk/
```

Usage:

```bash
pathwalk run \
  --pathway examples/pizzeria_ops.json \
  --task "Create an order for John: 2x Margherita" \
  --model gpt-4o \
  --api-key $OPENAI_API_KEY \
  --graphql-endpoint http://localhost:4000/graphql \
  --verbose
```

**Flags:**

| Flag | Env var | Default | Description |
|------|---------|---------|-------------|
| `--pathway` / `-p` | — | required | Path to pathway JSON file |
| `--task` / `-t` | — | required | Initial task description |
| `--model` | — | `gpt-4o` | LLM model name |
| `--api-key` | `OPENAI_API_KEY` | — | API key |
| `--base-url` | `OPENAI_BASE_URL` | — | Custom base URL (e.g. Groq, Ollama) |
| `--max-steps` | — | `50` | Maximum nodes to traverse |
| `--verbose` / `-v` | — | false | Print each step |
| `--graphql-endpoint` | `GRAPHQL_ENDPOINT` | — | Enables all six GraphQL tools |
| `--graphql-header` | — | — | `Key=Value` headers (repeatable) |

When `--graphql-endpoint` is not set, the engine falls back to `graphqlEndpoint` from the pathway JSON file.

---

## File Layout

```
github.com/wricardo/pathwalk/         (package pathwalk)
├── types.go          — exported types, NodeType constants, context key helpers
├── pathway.go        — ParsePathway(), ParsePathwayBytes(), *Pathway, type normalization
├── engine.go         — Engine, NewEngine(), Run(), EngineOption
├── executor.go       — per-node execution: executeLLM, executeWebhook, extractVars
├── router.go         — edge routing: llmRoute (select_route), evaluateRouteNode
├── state.go          — State, VarsSummary(), StepsSummary()
├── llm.go            — LLMClient interface, CompletionRequest/Response, OpenAIClient
├── testing.go        — MockLLMClient (in-package test double)
│
├── pathwaytest/
│   └── mock.go       — MockLLMClient for external test packages
│
├── tools/
│   ├── graphql.go    — GraphQLTool, graphql_query, graphql_mutation
│   └── schema.go     — AsTools(), graphql_queries, graphql_mutations, graphql_types, graphql_type
│
├── cmd/
│   ├── pathwalk/main.go    — primary CLI binary
│   └── aipathway/main.go   — legacy CLI binary
│
└── examples/
    └── pizzeria_ops.json   — full example: classify → route → handler → terminal
```
