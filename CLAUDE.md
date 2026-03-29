# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run all tests
go test ./...

# Run a single test
go test -run TestMinimalPathway ./...
go test -run TestPizzeriaRouting ./...

# Build the CLI
go build ./cmd/pathwalk/

# Run the CLI
./pathwalk run \
  --pathway examples/pizzeria_ops.json \
  --task "Create an order for John: 2x Margherita" \
  --graphql-endpoint http://localhost:4000/graphql \
  --model gpt-4o --api-key $OPENAI_API_KEY
```

## Architecture

This is a Go library (package `pathwalk`) that executes conversational pathway JSON files as agentic pipelines.

**Execution flow:**
1. `ParsePathway` / `ParsePathwayBytes` → `Pathway` (nodes + edges indexed)
2. `NewEngine(pathway, llmClient, opts...)` configures the runner
3. `engine.Run(ctx, task)` walks nodes step-by-step until terminal condition (high-level API)
4. Alternatively, `engine.Step(ctx, state, nodeID)` executes one node at a time for fine-grained control

**Per-node execution (`executor.go`):**
- `NodeTypeLLM` node: three LLM calls with distinct purposes set in context — `"execute"` (main action), `"extract_vars"` (structured variable extraction via `set_variables` tool call), `"route"` (when multiple edges, LLM picks via `select_route` tool call)
- `NodeTypeRoute` node: pure-Go condition evaluation against `state.Variables` (no LLM call)
- `NodeTypeTerminal` node: returns `TerminalText` as final output — terminates the run
- `NodeTypeWebhook` node: HTTP call with `{{variable}}` template substitution in body
- `NodeTypeCheckpoint` node: suspends or evaluates a gate. Four modes:
  - `human_input` / `human_approval`: suspends — `Step()` returns `WaitCondition`, caller collects input, calls `ResumeStep()`
  - `llm_eval`: LLM evaluates pass/fail against criteria (synchronous, no suspend)
  - `auto`: deterministic condition check, writes pass/fail to a variable (synchronous, no suspend)
- `NodeTypeAgent` node: spawns a single child agent run. Suspends with `WaitCondition{Mode: "agent", AgentTask: ...}`. Caller runs the child, calls `ResumeStep` with output in `Vars`.
- `NodeTypeTeam` node: spawns multiple child agents with a strategy. Suspends with `WaitCondition{Mode: "team", TeamTasks: [...], TeamStrategy: "parallel"|"race"|"sequence"}`. Caller runs children per strategy, calls `ResumeStep` with all outputs in `Vars`.

**Routing (`router.go`):**
- Single outgoing edge → follow it automatically
- Multiple edges on LLM/Webhook → LLM `select_route` function call
- Route node → evaluate `RouteRule` conditions (AND logic) in order; first match wins; `FallbackNodeID` if none match

**LLM interface (`llm.go`):**
- `LLMClient` interface with `Complete(ctx, CompletionRequest) (*CompletionResponse, error)`
- `OpenAIClient` handles the tool-call loop internally (up to 10 rounds)
- Compatible with any OpenAI-compatible API via `--base-url`

**Context keys for mock control:**
- `NodeIDContextKey` (`"nodeID"`) — which node is calling the LLM
- `CallPurposeContextKey` (`"callPurpose"`) — `"execute"`, `"extract_vars"`, or `"route"`

## Testing with MockLLMClient

All tests in `engine_test.go` use `MockLLMClient` from `pathwaytest/mock.go`. No real LLM calls are made.

```go
import "github.com/wricardo/pathwalk/pathwaytest"

mock := pathwaytest.NewMockLLMClient()

// Match by node ID only
mock.OnNode("n1", pathwaytest.MockResponse{Content: "Hello!"})

// Match by node ID + call purpose (more specific, wins over OnNode)
mock.OnNodePurpose("classify", "execute", pathwaytest.MockResponse{Content: "text"})
mock.OnNodePurpose("classify", "extract_vars", pathwaytest.MockResponse{
    ToolCalls: []pathwaytest.MockToolCall{
        {Name: "set_variables", Args: map[string]any{"key": "val"}},
    },
})

// Fallback for any unmatched call
mock.SetDefault(pathwaytest.MockResponse{Content: "fallback"})

// Assertions
mock.CallCount("n1")  // int — LLM calls for that node
mock.Calls            // []pathwaytest.RecordedCall — full call log
```

## Logging

The engine uses `log/slog` for structured logging. Pass a custom logger with:

```go
import "log/slog"

customLog := slog.New(slog.NewTextHandler(os.Stderr, nil))
engine := pathwalk.NewEngine(pathway, llm, pathwalk.WithLogger(customLog))
```

During `Step()` execution, logs are captured per-step in `StepResult.Logs`.

**`RunResult.Reason` values:** `"terminal"`, `"max_steps"`, `"error"`, `"dead_end"`, `"missing_node"`, `"max_node_visits"`, `"checkpoint"`

**`StepResult` (returned by `engine.Step()`):**
- `Step`: The step record (node executed, output, variables extracted)
- `NextNodeID`: Node to execute next (empty when `Done==true`)
- `Done`: True when run should terminate
- `Reason`: Why termination occurred — `"terminal"`, `"dead_end"`, `"error"`, `"missing_node"`, `"max_node_visits"`, `"checkpoint"`
- `Output`: Text output from the node
- `Error`: Error message if applicable
- `Logs`: Log records captured during this step
- `WaitCondition`: Non-nil when a checkpoint suspends execution (human_input/human_approval modes)

**`engine.ResumeStep(ctx, state, nodeID, CheckpointResponse)`** resumes after a checkpoint.
Pass the `WaitCondition.NodeID` and a `CheckpointResponse{Value, Vars, ChildRuns}`. Returns a `*StepResult`
with `NextNodeID` set for continued stepping. Works for Checkpoint, Agent, and Team nodes.

**Step logging:** Every step records `ResumeValue` (what was submitted) and `ChildRuns` (child agent
execution traces). Suspend steps include descriptive `Output` (e.g. `[human_approval] prompt text`,
`[agent] Spawning child "name"`, `[team:parallel] Spawning N agents`). `ChildRun{Name, AgentID, Output, Steps}`
captures the full trace of each child agent.

## Pathway JSON format

Pathways are JSON files with `nodes` and `edges` arrays. The parser maps raw JSON type strings
to normalized NodeType constants: `"Default"` → `NodeTypeLLM`, `"End Call"` → `NodeTypeTerminal`,
`"Webhook"` → `NodeTypeWebhook`, `"Route"` → `NodeTypeRoute`, `"Checkpoint"` → `NodeTypeCheckpoint`,
`"Agent"` → `NodeTypeAgent`, `"Team"` → `NodeTypeTeam`.

Key `node.data` fields:
- `isStart: true` — marks the entry node
- `extractVars: [["name", "type", "description", required], ...]` — variables to extract
- `routes: [{conditions: [{field, operator, value}], targetNodeId}]` — Route node rules
- `condition` — exit condition hint passed to the LLM for routing decisions
- `modelOptions.newTemperature` — per-node LLM temperature
- `checkpointMode` — Checkpoint mode: `"human_input"`, `"human_approval"`, `"llm_eval"`, `"auto"`
- `checkpointPrompt` — text shown to the human or used as LLM eval context
- `checkpointVariable` — variable name to store the checkpoint response
- `checkpointCriteria` — pass/fail criteria for `llm_eval` mode
- `checkpointConditions` — deterministic conditions for `auto` mode (same format as route conditions)
- `checkpointOptions` — custom options for `human_approval` (default: `["approve", "reject"]`)

`extractVars` tuple format: `[name(string), type("string"|"integer"|"boolean"), description(string), required(bool)]`

Route condition operators: `"is"`, `"is not"`, `"contains"`, `"not contains"`, `">"`, `"<"`, `">="`, `"<="`

## Node-Level Tools

Nodes can declare tools in `node.data.tools`. These are scoped to the node — the LLM only sees them
when executing that specific node. They are merged with any global tools registered via `WithTools()`.

Currently only `"webhook"` type tools are supported. The engine converts each `NodeTool` into an
executable `Tool` at runtime, performing the HTTP call with `{{variable}}` template substitution.

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
- `type`: `"webhook"` — makes an HTTP call with the configured method/URL/body
- `behavior`: `"feed_context"` — the response is fed back to the LLM conversation
- `config.timeout`: per-tool HTTP timeout in seconds (0 = default 30s)
- `config.retries`: number of retry attempts on failure (0 = no retries)
- `extractVars`: variables to extract from the webhook JSON response into state
- `responsePathways`: conditional routing based on the tool's response:
  - `"default"` — always matches (fallback)
  - `"BlandStatusCode"` — matches on HTTP status code with an operator/value condition
  - When a pathway with a `nodeId` matches, it overrides normal edge-based routing

See `examples/node_tools_example.json` for a complete working example.
