# Temporal Integration

The `temporalworker` package runs pathwalk pathways as durable Temporal workflows. Each node execution is a separate activity — crashes resume from the last completed step.

## Package

```go
import "github.com/wricardo/pathwalk/temporalworker"
```

## Starting a Worker

```go
c, err := client.Dial(client.Options{HostPort: "localhost:7233"})
if err != nil {
    log.Fatal(err)
}
defer c.Close()

// StartWorker registers PathwayWorkflow + ExecuteStep activity and starts the worker.
if err := temporalworker.StartWorker(c); err != nil {
    log.Fatal(err)
}
```

Or run the prebuilt binary:
```bash
TEMPORAL_HOST=localhost:7233 ./pathwalk-worker
```

## Running a Pathway

```go
input := temporalworker.PathwayInput{
    PathwayJSON: pathwayBytes,
    Task:        "Create an order for John: 2x Margherita",
    LLMModel:    "gpt-4o",
    LLMAPIKey:   os.Getenv("OPENAI_API_KEY"),
    MaxSteps:    50,  // 0 = use default
}

// Fire and forget — returns immediately with a run ID
runID, err := temporalworker.StartRun(ctx, temporalClient, input,
    temporalworker.StartOptions{WorkflowID: "optional-custom-id"},
)

// Poll for current state (non-blocking)
snapshot, err := temporalworker.GetResult(ctx, temporalClient, runID)

// Block until complete
result, err := temporalworker.WaitForResult(ctx, temporalClient, runID, "")
```

## PathwayInput

```go
type PathwayInput struct {
    PathwayJSON []byte  // JSON-encoded pathway
    Task        string  // Initial task description
    MaxSteps    int     // 0 = 50 (default)
    Verbose     bool
    LLMModel    string
    LLMBaseURL  string  // empty = OpenAI default
    LLMAPIKey   string

    // Optional completion callback
    CompletionTaskQueue    string
    CompletionActivityName string
    CompletionData         string // opaque caller data echoed back
}
```

## Completion Callbacks

When `CompletionTaskQueue` and `CompletionActivityName` are set, the workflow calls that activity after finishing (success, error, or max_steps):

```go
type CompletionCallbackInput struct {
    Data   string             // echoes PathwayInput.CompletionData
    Result *pathwalk.RunResult
    Err    string             // set when workflow-level error occurred
}
```

The callback is called with `MaximumAttempts: 3`. Failures are logged as warnings — they do not fail the main workflow.

## Mid-Run Status

Query a running workflow for its current state:

```go
snapshot, err := temporalworker.GetResult(ctx, client, runID)
// snapshot.Status        "running" or terminal reason
// snapshot.CurrentNodeID current node being executed
// snapshot.Variables     variables extracted so far
// snapshot.Steps         steps completed so far
// snapshot.Output        last node output
```

## Architecture

```
PathwayWorkflow (workflow.go)
  └─ for each step:
       ExecuteStep activity (activities.go)
         └─ engine.Step(ctx, state, nodeID)
              └─ executeNode → LLM / Webhook / Route / Terminal
```

- **Stateless activities**: the pathway JSON is re-parsed on every activity execution (safe across workers)
- **Activity timeout**: 10 minutes per step
- **Task queue**: `"pathwalk"` (constant `temporalworker.TaskQueue`)
- **Query handler**: `"get-result"` → returns `*RunSnapshot`

## Key Differences from engine.Run()

| Aspect | `engine.Run()` | `PathwayWorkflow` |
|--------|----------------|-------------------|
| Durability | None — in-process | Full Temporal durability |
| Crash recovery | Restarts from scratch | Resumes from last completed step |
| Logs | In `RunResult.Logs` | Activity logs visible in Temporal UI |
| `FailedNode` | Set on error | Set on activity failure |
| Cancellation | Context cancellation | Temporal workflow cancellation |
