# Engine API

## Parsing

```go
// From file
pathway, err := pathwalk.ParsePathway("pathway.json")

// From bytes (e.g. embedded, fetched from DB)
pathway, err := pathwalk.ParsePathwayBytes(jsonBytes)

// Validate without running (returns schema errors + parse errors)
result := pathwalk.ValidatePathwayBytes(jsonBytes)
if !result.Valid() {
    for _, e := range result.Errors() {
        log.Println(e)
    }
}
```

`ParsePathwayBytes` enforces full referential integrity — edge targets, route targets, fallback nodes, and tool response pathway nodes must all exist. Malformed `extractVars` tuples are errors, not silent skips.

## Creating an Engine

```go
engine := pathwalk.NewEngine(pathway, llm,
    pathwalk.WithTools(tool1, tool2),
    pathwalk.WithMaxSteps(100),
    pathwalk.WithLogger(customSlogLogger),
    pathwalk.WithVerbose(true),
)
```

**Panics** if `pathway` or `llm` is nil — both are required.

### Engine Options

| Option | Effect |
|--------|--------|
| `WithTools(tools ...Tool)` | Global tools available to every LLM node |
| `WithMaxSteps(n int)` | Override default 50-step cap |
| `WithLogger(*slog.Logger)` | Custom structured logger |
| `WithVerbose(bool)` | Extra debug logging |

## Run — High-Level API

```go
result, err := engine.Run(ctx, "task description")
```

Returns `*RunResult` and `error`. Both can be non-nil simultaneously when `Reason` is `"error"` or `"missing_node"` — the result contains partial state and the error describes what failed.

```go
type RunResult struct {
    Output     string         // Last LLM/webhook output before terminal
    Variables  map[string]any // Final state variables
    Steps      []Step         // Execution history
    Reason     string         // Why the run ended
    FailedNode string         // Set when Reason is "error" or "max_node_visits"
    Logs       []LogEntry     // All log records from this run
}
```

### Checking the result

```go
result, err := engine.Run(ctx, task)
if err != nil {
    // Reason is "error" or "missing_node"
    // result is still non-nil — check result.Steps for partial progress
    log.Printf("run failed at node %q: %v", result.FailedNode, err)
    return
}
switch result.Reason {
case "terminal":
    // Normal completion
case "max_steps":
    // Hit the step cap — partial run
case "dead_end":
    // Pathway design issue: node has no outgoing edges
}
```

## Step — Fine-Grained API

Executes one node at a time. Useful for external orchestration (Temporal, custom loops).

```go
state := pathwalk.NewState("task description")
nodeID := pathway.StartNode.ID

for {
    result, _ := engine.Step(ctx, state, nodeID)
    // result.Step contains what happened
    // state is mutated in place (variables, visit counts, steps appended)

    if result.Done {
        fmt.Println("Reason:", result.Reason)
        break
    }
    nodeID = result.NextNodeID
}
```

`Step` never returns a non-nil error — all failures surface via `StepResult.Done == true` with an appropriate `Reason`.

```go
type StepResult struct {
    Step          Step           // The step record (node, output, vars extracted)
    NextNodeID    string         // Empty when Done == true
    Done          bool
    Reason        string         // "terminal", "dead_end", "error", "missing_node", "max_node_visits", "checkpoint"
    Output        string
    Error         string         // Human-readable error message when Reason is "error"
    FailedNode    string         // Node name when Reason is "error" or "max_node_visits"
    Logs          []LogEntry     // Log records from this step only
    WaitCondition *WaitCondition // Non-nil when a checkpoint suspends (human_input/human_approval)
}
```

### Checkpoint Suspend/Resume

When `Step()` hits a suspending checkpoint node, it returns `WaitCondition != nil` with `Done == false`.
The caller collects external input and calls `ResumeStep`:

```go
for {
    result, _ := engine.Step(ctx, state, nodeID)
    if result.WaitCondition != nil {
        // Suspended — collect input from human/external system
        response := pathwalk.CheckpointResponse{Value: "approve"}
        result, _ = engine.ResumeStep(ctx, state, result.WaitCondition.NodeID, response)
    }
    if result.Done {
        break
    }
    nodeID = result.NextNodeID
}
```

`ResumeStep` stores the response value in `state.Variables[node.CheckpointVariable]`, merges any extra `response.Vars`, then routes to the next node via outgoing edges. Works for `NodeTypeCheckpoint`, `NodeTypeAgent`, and `NodeTypeTeam` nodes.

## LLM Client

```go
// Built-in OpenAI-compatible client
llm := pathwalk.NewOpenAIClient(apiKey, baseURL, model)
// baseURL="" uses the default OpenAI endpoint
// Compatible with Groq, Ollama, OpenRouter, venu, etc.
```

### Custom LLM Client

Implement the `LLMClient` interface:

```go
type LLMClient interface {
    Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}
```

The implementation is responsible for the full tool-call loop. Context carries `NodeIDContextKey` and `CallPurposeContextKey` for routing/mocking purposes:

```go
nodeID  := pathwalk.NodeIDFromContext(ctx)
purpose := pathwalk.CallPurposeFromContext(ctx)
// purpose: "execute", "extract_vars", "route", "check_global"
```

## State

```go
state := pathwalk.NewState("task")
// state.Variables  map[string]any  — current extracted variables
// state.Steps      []Step          — execution history
// state.Task       string          — original task description

state.SetVars(map[string]any{"key": "value"}) // merge (skips nil values)
state.VarsSummary()   // human-readable variable dump
state.StepsSummary()  // human-readable step history
```

Variables set externally before `Step()` are visible to the LLM in its system prompt.

## Pathway Structure

```go
type Pathway struct {
    Nodes           []*Node
    NodeByID        map[string]*Node
    Edges           []*Edge
    EdgesFrom       map[string][]*Edge  // source node ID → edges
    StartNode       *Node
    GlobalNodes     []*Node
    MaxVisitsPerNode int
    MaxTurns        int
}
```

After parsing, use `pathway.NodeByID["id"]` for direct node lookups.
