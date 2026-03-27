# Pathway JSON Format

A pathway is a JSON object with `nodes` and `edges` arrays.

## Top-Level Structure

```json
{
  "nodes": [...],
  "edges": [...],
  "maxTurns": 30
}
```

`maxTurns` is optional (overrides the engine's default 50-step cap).

## Node Object

```json
{
  "id": "unique-node-id",
  "type": "Default",
  "data": {
    "name": "Human-readable name",
    "isStart": true,
    "prompt": "System prompt for the LLM",
    "text": "Also used as system prompt (alias for prompt)",
    "condition": "Hint for the LLM about when to route away",
    "extractVars": [...],
    "routes": [...],
    "fallbackNodeId": "node-id",
    "isGlobal": false,
    "globalLabel": "",
    "modelOptions": { "newTemperature": 0.7 },
    "maxVisits": 3,
    "tools": [...]
  }
}
```

### Type Values

| `"type"` in JSON | Parsed as |
|------------------|-----------|
| `"Default"` | `NodeTypeLLM` |
| `"End Call"` | `NodeTypeTerminal` |
| `"Webhook"` | `NodeTypeWebhook` |
| `"Route"` | `NodeTypeRoute` |

For `"End Call"` nodes, `data.text` is the terminal text returned as the final output.

## Edge Object

```json
{
  "id": "edge-id",
  "source": "source-node-id",
  "target": "target-node-id",
  "data": {
    "label": "Optional label shown to LLM when routing",
    "description": "Optional longer description"
  }
}
```

Both `source` and `target` must reference existing node IDs — the parser validates this.

## extractVars

Tells the engine to make a second LLM call after `execute` to extract structured data into state variables.

```json
"extractVars": [
  ["variable_name", "string",  "Description for the LLM", true],
  ["count",         "integer", "How many items",           false],
  ["confirmed",     "boolean", "User confirmed?",          false]
]
```

Tuple format: `[name, type, description, required]`
- `type` must be `"string"`, `"integer"`, or `"boolean"`
- `required` is optional (defaults to false)
- Tuples with fewer than 3 elements cause a parse error

Extracted variables are merged into `state.Variables` and available in all subsequent nodes as `{{variable_name}}` in templates.

## Route Node

```json
{
  "id": "router",
  "type": "Route",
  "data": {
    "name": "Router",
    "routes": [
      {
        "conditions": [
          {"field": "order_type", "operator": "is", "value": "pizza"},
          {"field": "item_count", "operator": ">",  "value": "0"}
        ],
        "targetNodeId": "pizza-node"
      },
      {
        "conditions": [
          {"field": "order_type", "operator": "is", "value": "drinks"}
        ],
        "targetNodeId": "drinks-node"
      }
    ],
    "fallbackNodeId": "unknown-node"
  }
}
```

- Conditions within a rule are **AND** logic
- Rules are evaluated in order — first match wins
- `fallbackNodeId` fires when no rule matches
- All node IDs referenced must exist in the pathway

### Valid Operators

`is`, `equals`, `==`, `is not`, `not equals`, `!=`, `contains`, `not contains`, `>`, `>=`, `<`, `<=`

Operators are case-insensitive. Numeric operators (`>`, `<`, `>=`, `<=`) parse values as integers; fall back to lexicographic if either side is not a valid integer.

## Webhook Node

```json
{
  "id": "save-order",
  "type": "Webhook",
  "data": {
    "name": "Save Order",
    "url": "https://api.example.com/orders",
    "method": "POST",
    "headers": { "Authorization": "Bearer {{api_token}}" },
    "body": {
      "customer": "{{customer_name}}",
      "items": "{{order_items}}"
    }
  }
}
```

- `method` defaults to `"POST"` if omitted
- `body` can be a JSON object or a string — `{{variable}}` placeholders are replaced with current state variables
- If the response is valid JSON, it is available to the next LLM node via conversation history

## Node-Level Tools

Declarative tools scoped to a single node. Only visible to the LLM when executing that node.

```json
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
      "body": "{\"name\": \"{{customer_name}}\", \"email\": \"{{email}}\"}",
      "timeout": 10,
      "retries": 1
    },
    "extractVars": [
      ["customer_id", "string", "Assigned customer ID", true]
    ],
    "responsePathways": [
      {
        "type": "BlandStatusCode",
        "operator": "==",
        "value": "409",
        "nodeId": "already-exists-node",
        "name": "Conflict"
      },
      {
        "type": "default",
        "nodeId": ""
      }
    ]
  }
]
```

- `type`: only `"webhook"` is supported
- `behavior`: `"feed_context"` — response is injected back into the LLM conversation
- `config.timeout`: seconds (0 = 30s default)
- `config.retries`: retry count on HTTP failure (0 = no retries)
- `extractVars`: same format as node-level extractVars; variables extracted from the JSON response
- `responsePathways`: conditional routing based on the tool response
  - `"default"` — always matches (use as fallback)
  - `"BlandStatusCode"` — matches on HTTP status code using operator/value
  - When a pathway with a non-empty `nodeId` matches, it overrides normal edge routing via `$tool_route` state variable

## Global Nodes

A node with `"isGlobal": true` and a non-empty `"globalLabel"` is a **global node**. Before every step, the engine asks the LLM whether any global node should fire (e.g. "Cancel", "Human Transfer"). If one fires, execution jumps to that node regardless of normal routing.

```json
{
  "id": "cancel",
  "type": "Default",
  "data": {
    "name": "Cancel",
    "isGlobal": true,
    "globalLabel": "Cancel order",
    "prompt": "The user wants to cancel. Confirm cancellation and end the call."
  }
}
```

## Minimal Working Example

```json
{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Greet",
        "isStart": true,
        "prompt": "Greet the user and ask what they need.",
        "extractVars": [
          ["intent", "string", "What the user wants to do", true]
        ]
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Goodbye", "text": "Thanks, goodbye!" }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "end", "data": {} }
  ]
}
```
