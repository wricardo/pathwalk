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
    pathwalk.WithTools(gql.Tools()...),
)
```

This registers six tools with the LLM:

| Tool name | Purpose |
|-----------|---------|
| `graphql_query` | Execute a GraphQL query |
| `graphql_mutation` | Execute a GraphQL mutation |
| `graphql_queries` | List available queries in the schema |
| `graphql_mutations` | List available mutations in the schema |
| `graphql_types` | List all types |
| `graphql_type` | Describe a specific type |

When `Name` is set (e.g. `Name: "orders"`), all tool names get a suffix: `graphql_query_orders`, etc. Use this when a pathway needs to talk to multiple GraphQL endpoints.

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
