# Tools

Tools are functions exposed to the LLM via function calling. Pathwalk supports two kinds:

1. **Global tools** — registered on the engine, available to every LLM node
2. **Node-level tools** — declared in pathway JSON, scoped to a single node

## Global Tools

```go
engine := pathwalk.NewEngine(pathway, llm,
    pathwalk.WithTools(tool1, tool2),
)
```

### Tool Interface

```go
type Tool struct {
    Name        string
    Description string
    Parameters  map[string]any        // JSON Schema for the arguments
    Fn          func(ctx context.Context, args map[string]any) (any, error)
}
```

### Custom Tool Example

```go
saveTool := pathwalk.Tool{
    Name:        "save_order",
    Description: "Persist the order to the database. Call once all details are confirmed.",
    Parameters: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "customer_name": map[string]any{"type": "string"},
            "items":         map[string]any{"type": "string"},
        },
        "required": []string{"customer_name", "items"},
    },
    Fn: func(ctx context.Context, args map[string]any) (any, error) {
        name := args["customer_name"].(string)
        items := args["items"].(string)
        id, err := db.SaveOrder(ctx, name, items)
        return map[string]any{"order_id": id}, err
    },
}
```

## GraphQL Tool

`github.com/wricardo/pathwalk/tools` provides a ready-made GraphQL tool set.

```go
import "github.com/wricardo/pathwalk/tools"

gql := &tools.GraphQLTool{
    Endpoint: "http://localhost:4000/graphql",
    Headers:  map[string]string{"Authorization": "Bearer " + token},
    Name:     "",  // optional suffix for multi-endpoint pathways
}

engine := pathwalk.NewEngine(pathway, llm,
    pathwalk.WithTools(gql.AsTools()...),
)
```

This registers eight tools with the LLM:

| Tool name | Purpose |
|-----------|---------|
| `graphql_query` | Execute a GraphQL query (optional `jq` param for response filtering) |
| `graphql_mutation` | Execute a GraphQL mutation (optional `jq` param) |
| `graphql_batch` | Execute multiple operations in one request (NDJSON) |
| `graphql_explore` | Batch schema exploration (queries + mutations + types in one call) |
| `graphql_queries` | List available queries in the schema |
| `graphql_mutations` | List available mutations in the schema |
| `graphql_types` | List all types |
| `graphql_type` | Describe a specific type |

When `Name` is set (e.g. `Name: "orders"`), all tool names get a suffix: `graphql_query_orders`, etc. Use this when a pathway needs to talk to multiple GraphQL endpoints.

### Server-Side jq Filtering

`graphql_query`, `graphql_mutation`, and `graphql_batch` accept an optional `jq` parameter. When the server supports NDJSON with jq filtering (like graphql-api's `internal/transport/NDJSON`), the filter is applied server-side before the response is returned. Otherwise, pathwalk applies it client-side via gojq.

```
LLM calls: graphql_query(query: "{ users { id name status } }", jq: ".data.users[].name")
Result:    ["Alice", "Bob"]   ← instead of full {"data":{"users":[...]}} envelope
```

This reduces tokens in the LLM conversation by returning only the relevant data.

### graphql_batch

Sends multiple operations as NDJSON to the server in a single HTTP request:

```
LLM calls: graphql_batch(operations: [
  {query: "{ users { id } }", jq: ".data.users | length"},
  {query: "{ orders { id } }", jq: ".data.orders | length"}
])
Result: [42, 15]
```

Falls back to sequential requests if the server doesn't support NDJSON.

### graphql_explore

Batches schema introspection into a single request:

```
LLM calls: graphql_explore(
  include_queries: true,
  include_mutations: true,
  types: ["User", "CreateUserInput"]
)
Result: {queries: [...], mutations: [...], types: [...]}
```

## jq Tool

Pure Go jq implementation using `github.com/itchyny/gojq`. No external binary required.

```go
engine := pathwalk.NewEngine(pathway, llm,
    pathwalk.WithTools(tools.JqTool{}.AsTools()...),
)
```

The LLM can call `jq(data: {...}, filter: ".users[].name")` to transform JSON data.

Also available as a Go function:

```go
result, err := pathwalk.RunJQ(".users[].name", data)
```

## Built-in Tools Bundle

```go
// All general-purpose tools: jq, grep, http_request
engine := pathwalk.NewEngine(pathway, llm,
    pathwalk.WithTools(tools.BuiltinTools()...),
)
```

## extractVars with jq (Deterministic Extraction)

`VariableDef` supports an optional `JQ` field. When set, the engine uses gojq instead of calling the LLM to extract the variable — saving a full LLM round trip.

```go
// In Go:
VariableDef{Name: "orderId", Type: "string", Description: "The order ID", JQ: ".data.createOrder.id"}
```

```json
// In pathway JSON (5th tuple element):
["orderId", "string", "The order ID", true, ".data.createOrder.id"]
```

When the LLM or webhook returns a JSON response, the jq expression extracts the value deterministically. Variables without a JQ expression fall back to LLM-based extraction as before.

## Node-Level Tools (Webhook)

Declared in pathway JSON, scoped to one node. See [pathway-json.md](pathway-json.md#node-level-tools) for the JSON format.

### How They Work

1. The LLM calls the tool by name during the `"execute"` call
2. The engine makes the HTTP request with `{{variable}}` substitution in the body
3. The JSON response is fed back into the conversation (`behavior: "feed_context"`)
4. Variables in `extractVars` are extracted from the response JSON and merged into state
5. If a `responsePathway` condition matches, `$tool_route` is set in state and the next Step routes there instead of following normal edges

### `$tool_route` Mechanism

When a tool's `responsePathway` fires with a non-empty `nodeId`, the engine sets `state.Variables["$tool_route"] = nodeId`. The next `Step()` reads this, removes it, and jumps to that node if it exists. If the node doesn't exist (or `$tool_route` is absent), normal edge routing applies.

### Body Template Substitution

The `body` string in the tool config uses `{{variableName}}` placeholders:

```json
"body": "{\"name\": \"{{customer_name}}\", \"email\": \"{{email}}\"}"
```

The substitution merges tool call `args` (LLM-supplied) with `state.Variables`. Tool args take precedence over state vars when keys conflict.

## Channel Directives (Fallback)

If a model doesn't support function calling but emits a `<|channel|>` directive in its response text, the engine parses and executes it automatically:

```
<|channel|>{"tool": "save_order", "args": {"customer_name": "John"}}
```

This is a fallback for models that format tool calls as text rather than using the API's native function calling.
