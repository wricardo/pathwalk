# pathwalk

A Go library and CLI that executes [Bland AI](https://www.bland.ai/)-style conversational pathway JSON files as agentic pipelines. Define your workflow as a graph of nodes and edges; the engine walks the graph, calls your LLM at each step, extracts variables, and routes to the next node automatically.

## Installation

```bash
go get github.com/wricardo/pathwalk
```

## CLI

Build and run a pathway from the command line:

```bash
go build ./cmd/pathwalk/

./pathwalk run \
  --pathway examples/pizzeria_ops.json \
  --task "Create an order for John: 2x Margherita" \
  --model gpt-4o \
  --api-key $OPENAI_API_KEY
```

### `run` flags

| Flag | Default | Description |
|------|---------|-------------|
| `--pathway`, `-p` | required | Path to the pathway JSON file |
| `--task`, `-t` | required | Initial task description |
| `--model` | `gpt-4o` | LLM model name |
| `--api-key` | `$OPENAI_API_KEY` | API key |
| `--base-url` | `$OPENAI_BASE_URL` | Base URL (for OpenAI-compatible APIs) |
| `--max-steps` | `50` | Maximum nodes to traverse |
| `--verbose`, `-v` | `false` | Print each step's output and routing decision |
| `--graphql-endpoint` | `$GRAPHQL_ENDPOINT` | Enables the built-in GraphQL tools |
| `--graphql-header` | | Extra HTTP headers (`Key=Value`, repeatable) |

### `validate` command

Validates a pathway JSON file against the bundled JSON schema and structural rules:

```bash
./pathwalk validate examples/pizzeria_ops.json
```

Outputs schema errors and parse errors separately, exits with code 1 on failure.

## Library usage

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/wricardo/pathwalk"
)

func main() {
    pathway, err := pathwalk.ParsePathway("my_pathway.json")
    if err != nil {
        log.Fatal(err)
    }

    llm := pathwalk.NewOpenAIClient(apiKey, "", "gpt-4o")

    engine := pathwalk.NewEngine(pathway, llm,
        pathwalk.WithMaxSteps(30),
    )

    result, err := engine.Run(context.Background(), "My task description")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(result.Output)
    fmt.Println(result.Variables)
}
```

### Step-by-step execution

For fine-grained control, use `Step()` to process one node at a time:

```go
state := pathwalk.NewState("My task description")
nodeID := pathway.StartNodeID

for {
    result, err := engine.Step(ctx, state, nodeID)
    if err != nil || result.Done {
        break
    }
    nodeID = result.NextNodeID
}
```

### Engine options

| Option | Description |
|--------|-------------|
| `WithMaxSteps(n)` | Maximum nodes to visit in a single `Run()` call (default 50) |
| `WithTools(tools...)` | Register global tools available to all LLM nodes |
| `WithLogger(log)` | Set a custom `*slog.Logger` (default `slog.Default()`) |
| `WithGlobalNodeCheck(bool)` | Enable/disable per-step global node interception (auto-enabled when pathway has global nodes) |

### Adding tools

```go
myTool := pathwalk.Tool{
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

engine := pathwalk.NewEngine(pathway, llm, pathwalk.WithTools(myTool))
```

### GraphQL tools

The `tools` package provides six GraphQL tools that are automatically wired when a `--graphql-endpoint` is set or when using the library directly:

| Tool | Description |
|------|-------------|
| `graphql_query` | Execute a GraphQL query |
| `graphql_mutation` | Execute a GraphQL mutation |
| `graphql_queries` | List available queries with argument types and return types |
| `graphql_mutations` | List available mutations with argument types and return types |
| `graphql_types` | List all named non-scalar types (objects, inputs, enums, interfaces) |
| `graphql_type` | Describe a specific type with fields expanded 2 levels deep |

The list/describe tools support optional `filter` and `withDescription` parameters.

```go
import "github.com/wricardo/pathwalk/tools"

gt := &tools.GraphQLTool{
    Endpoint: "http://localhost:4000/graphql",
    Headers:  map[string]string{"Authorization": "Bearer " + token},
}
engine := pathwalk.NewEngine(pathway, llm, pathwalk.WithTools(gt.AsTools()...))
```

When `Name` is set on `GraphQLTool`, all tool names get a `_<Name>` suffix (e.g. `graphql_query_sheets`) so multiple endpoints can coexist.

### RunResult

```go
type RunResult struct {
    Output     string         // final text output
    Variables  map[string]any // accumulated extracted variables
    Steps      []Step         // one entry per visited node
    Reason     string         // why the run ended (see below)
    FailedNode string         // node that caused the stop (on "error" or "max_node_visits")
    Logs       []LogEntry     // structured log records emitted during the run
}
```

**`Reason` values:**

| Value | Meaning |
|-------|---------|
| `"terminal"` | Reached a terminal (`End Call`) node |
| `"max_steps"` | Hit the step limit (`WithMaxSteps` or pathway `maxTurns`) |
| `"error"` | An error occurred during execution |
| `"dead_end"` | Node has no outgoing edges and isn't terminal |
| `"missing_node"` | Referenced node ID not found in the pathway |
| `"max_node_visits"` | A node exceeded its per-node visit limit |

`Run()` can return both a non-nil `*RunResult` and a non-nil error when `Reason` is `"error"` or `"missing_node"`. The result contains partial execution state (steps taken, variables extracted so far).

### StepResult

```go
type StepResult struct {
    Step       Step       // the step record for this execution
    NextNodeID string     // empty when Done=true
    Done       bool       // true when the run should terminate
    Reason     string     // same values as RunResult.Reason
    Output     string     // text output from the node
    Error      string     // error message if applicable
    FailedNode string     // node name that caused the stop
    Logs       []LogEntry // log records emitted during this step
}
```

### Validation

Validate pathway JSON programmatically:

```go
data, _ := os.ReadFile("pathway.json")
result := pathwalk.ValidatePathwayBytes(data)

if !result.Valid() {
    for _, err := range result.Errors() {
        fmt.Println(err)
    }
}
```

`ValidatePathwayBytes` runs both JSON schema validation (against an embedded schema) and structural parsing. Both checks run independently so all errors are returned in a single call.

## Pathway JSON format

Pathways are JSON files with `nodes` and `edges` arrays, compatible with the Bland AI export format.

### Top-level fields

```json
{
  "nodes": [...],
  "edges": [...],
  "graphqlEndpoint": "http://localhost:4000/graphql",
  "graphqlEndpoints": { "sheets": "http://localhost:4001/graphql" },
  "maxTurns": 30,
  "maxVisitsPerNode": 5
}
```

| Field | Description |
|-------|-------------|
| `graphqlEndpoint` | Default GraphQL endpoint; the CLI flag overrides this |
| `graphqlEndpoints` | Named endpoints; tools get `_<name>` suffix |
| `maxTurns` | Caps total node transitions (overrides engine default if lower) |
| `maxVisitsPerNode` | Default per-node visit cap for all nodes (0 = no limit) |

### Node types

**`Default`** (LLM node) -- runs an LLM prompt, optionally extracts variables, then routes to the next node.

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
    "modelOptions": { "newTemperature": 0.1 },
    "maxVisits": 3
  }
}
```

`extractVars` tuple: `[name, type, description, required]`
Supported types: `"string"`, `"integer"`, `"boolean"`

`maxVisits` overrides the pathway-level `maxVisitsPerNode` for this node.

**`Route`** -- branches based on extracted variables (no LLM call).

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

Multiple conditions within a rule are AND-ed; rules are evaluated in order; first match wins. String comparisons are case-insensitive. Numeric operators (`>`, `<`, `>=`, `<=`) parse values as float64.

**`End Call`** -- terminal node; returns `text` as the run output.

```json
{
  "id": "end",
  "type": "End Call",
  "data": { "name": "Done", "text": "Operation complete." }
}
```

**`Webhook`** -- makes an HTTP request; supports `{{variable}}` placeholders in the body.

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

### Global nodes

Nodes marked with `"isGlobal": true` act as interrupt handlers. Before each step, the engine asks the LLM whether any global node's condition matches the current state. If one matches, execution jumps to that node instead.

```json
{
  "id": "escalate",
  "type": "Default",
  "data": {
    "name": "Escalate to Manager",
    "isGlobal": true,
    "globalLabel": "Customer asks to speak with a manager",
    "prompt": "Transfer to manager..."
  }
}
```

Global node checking is auto-enabled when the pathway has at least one global node. Override with `WithGlobalNodeCheck(false)`.

### Node-level tools

Nodes can declare their own tools in `node.data.tools`. These are scoped to the node -- the LLM only sees them when executing that specific node. They are merged with any global tools registered via `WithTools()`.

Currently only `"webhook"` type tools are supported. The engine performs the HTTP call with `{{variable}}` template substitution.

```json
{
  "tools": [
    {
      "name": "save_customer",
      "description": "Save customer data. Call when name and email are confirmed.",
      "type": "webhook",
      "behavior": "feed_context",
      "config": {
        "url": "https://api.example.com/customers",
        "method": "POST",
        "headers": { "Content-Type": "application/json" },
        "body": "{\"name\": \"{{customer_name}}\", \"email\": \"{{customer_email}}\"}",
        "timeout": 10,
        "retries": 1
      },
      "extractVars": [["customer_id", "string", "Assigned customer ID", true]],
      "responsePathways": [
        { "type": "BlandStatusCode", "operator": "==", "value": "409", "nodeId": "already_exists" },
        { "type": "default", "nodeId": "" }
      ]
    }
  ]
}
```

**Key fields:**
- `type`: `"webhook"` -- makes an HTTP call with the configured method/URL/body
- `behavior`: `"feed_context"` -- the response is fed back to the LLM conversation
- `config.timeout`: per-tool HTTP timeout in seconds (0 = default 30s)
- `config.retries`: number of retry attempts on failure (0 = no retries)
- `extractVars`: variables to extract from the webhook JSON response into state
- `responsePathways`: conditional routing based on the tool's response:
  - `"default"` -- always matches (fallback)
  - `"BlandStatusCode"` -- matches on HTTP status code with an operator/value condition
  - When a pathway with a `nodeId` matches, it overrides normal edge-based routing

See `examples/node_tools_example.json` for a complete working example.

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

## LLM client

`LLMClient` is the interface for making LLM completions:

```go
type LLMClient interface {
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}
```

The built-in `OpenAIClient` works with any OpenAI-compatible API (OpenAI, Groq, Ollama, OpenRouter, venu, etc.) via the `baseURL` parameter. It handles the tool-call loop internally (up to 25 rounds).

### Context keys

Two context keys are set before each LLM call, useful for mocking:

- `NodeIDContextKey` (`"nodeID"`) -- which node triggered the call
- `CallPurposeContextKey` (`"callPurpose"`) -- `"execute"`, `"extract_vars"`, `"route"`, or `"check_global"`

## Temporal integration

The `temporalworker` package runs pathways as distributed Temporal workflows, executing each node as a separate activity.

### Worker

Build and run the Temporal worker:

```bash
go build ./cmd/pathwalk-worker/

TEMPORAL_HOST=localhost:7233 TEMPORAL_NAMESPACE=default ./pathwalk-worker
```

| Env var | Default | Description |
|---------|---------|-------------|
| `TEMPORAL_HOST` | `localhost:7233` | Temporal server address |
| `TEMPORAL_NAMESPACE` | `default` | Temporal namespace |

Or embed the worker in your own service:

```go
import "github.com/wricardo/pathwalk/temporalworker"

w, err := temporalworker.StartWorker(temporalClient, &temporalworker.PathwayActivities{})
```

### Starting a workflow

```go
import "github.com/wricardo/pathwalk/temporalworker"

pathwayJSON, _ := os.ReadFile("my_pathway.json")

input := temporalworker.PathwayInput{
    PathwayJSON: pathwayJSON,
    Task:        "Create an order for John",
    LLMModel:    "gpt-4o",
    LLMAPIKey:   os.Getenv("OPENAI_API_KEY"),
    MaxSteps:    30,
}

// Start async -- returns immediately with the workflow ID.
workflowID, err := temporalworker.StartRun(ctx, temporalClient, input, temporalworker.RunOptions{
    WorkflowID: "my-idempotent-id", // optional; Temporal generates a UUID if empty
})
```

### Querying status

```go
// Non-blocking: get current state of a running workflow.
snapshot, err := temporalworker.GetResult(ctx, temporalClient, workflowID)
// snapshot.Status is "running" or a terminal reason
// snapshot.CurrentNodeID, snapshot.Variables, snapshot.Steps, snapshot.Output

// Blocking: wait for the workflow to finish.
result, err := temporalworker.WaitForResult(ctx, temporalClient, workflowID, "")
// result is *pathwalk.RunResult
```

### Completion callbacks

Optionally invoke an activity on a different task queue when the workflow finishes:

```go
input := temporalworker.PathwayInput{
    // ... pathway config ...
    CompletionTaskQueue:    "my-app-queue",
    CompletionActivityName: "HandlePathwayComplete",
    CompletionData:         "execution-123", // opaque; echoed back in the callback
}
```

The callback receives a `CompletionCallbackInput` with the `RunResult` and the echoed `CompletionData`.

### Features

- Each node executes as a separate Temporal activity with heartbeats
- Pathway JSON is cached by SHA-256 hash across activity invocations
- Built-in `"get-result"` query handler for mid-run status checks
- Graceful shutdown via SIGINT/SIGTERM

## Web UI

A React SPA for visualizing pathway JSON files, served by a Go HTTP server.

### Build and run

```bash
# Build the React app (required once before running)
cd ui && npm install && npm run build && cd ..

# Build and start the server
go build ./cmd/pathwalk-ui/
./pathwalk-ui
```

Open `http://localhost:8080` in your browser.

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `:8080` | Listen address |
| `--ui` | `ui/dist` | Path to React build output |
| `--pathways` | `examples` | Directory containing pathway JSON files |

### Development mode

Run the Go server and the Vite dev server side by side. The Vite proxy forwards `/api` requests to the Go server:

```bash
# Terminal 1 -- Go API server
./pathwalk-ui -addr :8080

# Terminal 2 -- React dev server with hot reload
cd ui && npm run dev
# open http://localhost:5173
```

### Features

- **Sidebar** -- lists all `.json` files in the pathways directory; click one to load it.
- **Flow diagram** -- nodes are rendered at their `position` coordinates from the JSON. Nodes without positions are laid out automatically using BFS from the start node.
- **Node types** are color-coded:
  - Blue -- LLM (`Default`)
  - Orange -- Route
  - Purple -- Webhook
  - Red -- Terminal (`End Call`)
  - A green dot marks the start node.
- **Pan and zoom** -- drag to pan, scroll to zoom centered on the cursor.
- **Node details panel** -- click any node to open a panel showing its prompt, exit condition, extract variables, routes, tools, and other fields.

### API endpoints

The Go server exposes two JSON endpoints:

| Endpoint | Description |
|----------|-------------|
| `GET /api/pathways` | Returns a JSON array of `.json` filenames from the pathways directory |
| `GET /api/pathway?file=<name>` | Returns the raw JSON content of a single pathway file |

## Testing

`MockLLMClient` lets you script LLM responses without network calls:

```go
mock := pathwaytest.NewMockLLMClient()

// Match by node ID
mock.OnNode("n1", pathwaytest.MockResponse{Content: "Hello!"})

// Match by node ID + call purpose ("execute", "extract_vars", "route", or "check_global")
mock.OnNodePurpose("classify", "extract_vars", pathwaytest.MockResponse{
    ToolCalls: []pathwaytest.MockToolCall{
        {Name: "set_variables", Args: map[string]any{"operation_type": "orders"}},
    },
})

// Mock global node checks
mock.OnNodePurpose(pathwalk.GlobalCheckNodeID, "check_global", pathwaytest.MockResponse{
    ToolCalls: []pathwaytest.MockToolCall{
        {Name: "select_global_node", Args: map[string]any{"node": 0}},
    },
})

// Fallback for any unmatched call
mock.SetDefault(pathwaytest.MockResponse{Content: "ok"})

engine := pathwalk.NewEngine(pathway, mock)
result, err := engine.Run(ctx, "test task")

// Assertions
mock.CallCount("n1")  // number of LLM calls for that node
mock.Calls            // []RecordedCall -- full call log
```

Run the tests:

```bash
go test ./...
```

## Examples

| File | Description |
|------|-------------|
| `examples/pizzeria_ops.json` | Multi-node pizzeria operations pathway with classification, routing, and GraphQL tools |
| `examples/node_tools_example.json` | Demonstrates node-level webhook tools with response pathways and conditional routing |
| `examples/pizzeria-server/` | A gqlgen GraphQL server that backs the pizzeria pathway |
