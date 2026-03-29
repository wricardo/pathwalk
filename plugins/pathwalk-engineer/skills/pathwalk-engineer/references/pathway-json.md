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
| `"Checkpoint"` | `NodeTypeCheckpoint` |
| `"Agent"` | `NodeTypeAgent` |
| `"Team"` | `NodeTypeTeam` |

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

Tells the engine to extract structured data into state variables. By default, a second LLM call is made after `execute`. When a `jq` expression is provided (5th element), extraction is deterministic — no LLM call needed.

```json
"extractVars": [
  ["variable_name", "string",  "Description for the LLM", true],
  ["count",         "integer", "How many items",           false],
  ["confirmed",     "boolean", "User confirmed?",          false],
  ["order_id",      "string",  "The created order ID",     true, ".data.createOrder.id"]
]
```

Tuple format: `[name, type, description, required, jq]`
- `type` must be `"string"`, `"integer"`, or `"boolean"`
- `required` is optional (defaults to false)
- `jq` is optional — a jq expression to extract the value from the response (no LLM call)
- Tuples with fewer than 3 elements cause a parse error

When `jq` is set, the engine applies it via gojq on the response text/JSON. If jq extraction fails, it falls back to LLM extraction. Variables without jq use LLM extraction as before.

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

## Checkpoint Node

Suspends execution for external input or evaluates a gate condition.

```json
{
  "id": "approval-gate",
  "type": "Checkpoint",
  "data": {
    "name": "Approval Gate",
    "checkpointMode": "human_approval",
    "checkpointPrompt": "Do you approve this action?",
    "checkpointVariable": "approval_status",
    "checkpointOptions": ["approve", "reject"]
  }
}
```

### Checkpoint Modes

| Mode | Suspends? | Behavior |
|------|-----------|----------|
| `human_input` | Yes | Waits for freeform text input |
| `human_approval` | Yes | Waits for one of the defined options (default: approve/reject) |
| `llm_eval` | No | LLM evaluates pass/fail against `checkpointCriteria` |
| `auto` | No | Deterministic condition check using `checkpointConditions` |

### Checkpoint Fields

- `checkpointMode` (required) — one of the four modes above
- `checkpointPrompt` — text shown to the human or used as LLM eval context
- `checkpointVariable` — variable name to store the response (`"pass"`/`"fail"` for auto/llm_eval, user input for human modes)
- `checkpointCriteria` — pass/fail criteria text (llm_eval only)
- `checkpointConditions` — array of condition objects, same format as route conditions (auto only)
- `checkpointOptions` — custom options array for human_approval (default: `["approve", "reject"]`)

After a checkpoint, the stored variable is available for downstream Route nodes to branch on.

## Agent Node

Spawns a single child agent run and suspends until it completes.

```json
{
  "id": "research",
  "type": "Agent",
  "data": {
    "name": "Research Agent",
    "agentId": "agent-researcher-123",
    "task": "Research {{topic}} and summarize.",
    "outputVar": "research_summary"
  }
}
```

- `agentId`: ID of the child agent (an Agent record in the DB with its own pathway + tools)
- `task`: task template — `{{variable}}` placeholders are resolved from parent state
- `outputVar`: variable name where the child's output is stored in parent state

The engine suspends with `WaitCondition{Mode: "agent", AgentTask: {...}}`. The caller spawns the child, collects the result, and calls `ResumeStep` with the output in `CheckpointResponse.Vars`.

## Team Node

Spawns multiple child agent runs with a coordination strategy.

```json
{
  "id": "review-team",
  "type": "Team",
  "data": {
    "name": "Review Team",
    "strategy": "parallel",
    "agents": [
      {"name": "Bugs", "agentId": "agent-bugs", "task": "Find bugs in {{code}}", "outputVar": "bug_report"},
      {"name": "Security", "agentId": "agent-sec", "task": "Security review: {{code}}", "outputVar": "sec_report"}
    ]
  }
}
```

### Team strategies

| Strategy | Behavior |
|----------|----------|
| `parallel` | Spawn all, wait for all, merge all outputs |
| `race` | Spawn all, use first to complete, cancel rest |
| `sequence` | Run in order, each gets prior agent outputs |

The engine suspends with `WaitCondition{Mode: "team", TeamTasks: [...], TeamStrategy: "..."}`. The caller handles coordination and calls `ResumeStep` with all outputs in `Vars`.

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
