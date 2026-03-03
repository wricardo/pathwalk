package temporalworker

import (
	"fmt"
	"time"

	"github.com/wricardo/pathwalk"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "pathwalk"

// PathwayInput contains the parameters for a PathwayWorkflow.
type PathwayInput struct {
	PathwayJSON []byte // JSON-encoded pathway
	Task        string // Initial task description
	MaxSteps    int    // 0 = use engine default (50)
	Verbose     bool
	LLMModel    string
	LLMBaseURL  string
	LLMAPIKey   string

	// Optional completion callback. When set, PathwayWorkflow calls this activity
	// on CompletionTaskQueue after finishing (success or error), passing a
	// CompletionCallbackInput.
	CompletionTaskQueue    string
	CompletionActivityName string
	CompletionData         string // opaque caller data (e.g., execution ID)
}

// CompletionCallbackInput is the input to the completion callback activity.
// Called by PathwayWorkflow after the pathway run finishes.
type CompletionCallbackInput struct {
	Data   string            // echoes PathwayInput.CompletionData
	Result *pathwalk.RunResult // nil on workflow error
	Err    string            // set if workflow-level error occurred
}

// RunSnapshot captures the current state of a running or completed workflow.
type RunSnapshot struct {
	Status        string         // "running" or terminal reason
	CurrentNodeID string
	Output        string
	Variables     map[string]any
	Steps         []pathwalk.Step
	Error         string
}

// PathwayWorkflow executes a pathwalk pathway as a Temporal workflow.
// It returns the final RunResult or an error.
func PathwayWorkflow(ctx workflow.Context, input PathwayInput) (*pathwalk.RunResult, error) {
	// Parse the pathway deterministically (safe in workflow).
	pathway, err := pathwalk.ParsePathwayBytes(input.PathwayJSON)
	if err != nil {
		return nil, fmt.Errorf("parsing pathway: %w", err)
	}

	// Initialize state and snapshot.
	state := pathwalk.NewState(input.Task)
	snapshot := RunSnapshot{
		Status:        "running",
		CurrentNodeID: pathway.StartNode.ID,
		Variables:     state.Variables,
		Steps:         state.Steps,
	}

	// Register the query handler for mid-run status checks.
	err = workflow.SetQueryHandler(ctx, "get-result", func() (*RunSnapshot, error) {
		return &snapshot, nil
	})
	if err != nil {
		return nil, fmt.Errorf("registering query handler: %w", err)
	}

	// Determine max steps.
	maxSteps := input.MaxSteps
	if maxSteps == 0 {
		maxSteps = 50
	}
	if pathway.MaxTurns > 0 && pathway.MaxTurns < maxSteps {
		maxSteps = pathway.MaxTurns
	}

	// Execute steps in a loop.
	for step := 0; step < maxSteps; step++ {
		if snapshot.CurrentNodeID == "" {
			// This shouldn't happen, but handle gracefully.
			return &pathwalk.RunResult{
				Output:    snapshot.Output,
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "missing_node",
			}, nil
		}

		// Call the ExecuteStep activity.
		stepInput := StepActivityInput{
			PathwayJSON:   input.PathwayJSON,
			State:         state,
			CurrentNodeID: snapshot.CurrentNodeID,
			LLMModel:      input.LLMModel,
			LLMBaseURL:    input.LLMBaseURL,
			LLMAPIKey:     input.LLMAPIKey,
			Verbose:       input.Verbose,
		}

		actOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, actOpts)

		var result *StepActivityResult
		if err := workflow.ExecuteActivity(ctx, (*PathwayActivities).ExecuteStep, stepInput).Get(ctx, &result); err != nil {
			return &pathwalk.RunResult{
				Output:    snapshot.Output,
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "error",
			}, fmt.Errorf("executing step at node %q: %w", snapshot.CurrentNodeID, err)
		}

		// Update state and snapshot from activity result.
		state = result.State
		stepResult := result.StepResult

		snapshot.Variables = state.Variables
		snapshot.Steps = state.Steps
		snapshot.Output = stepResult.Output

		// Check if the run is done.
		if stepResult.Done {
			result := &pathwalk.RunResult{
				Output:    stepResult.Output,
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    stepResult.Reason,
			}

			// Call completion callback if configured.
			if input.CompletionTaskQueue != "" && input.CompletionActivityName != "" {
				cbInput := CompletionCallbackInput{
					Data:   input.CompletionData,
					Result: result,
				}
				cbOpts := workflow.ActivityOptions{
					TaskQueue:           input.CompletionTaskQueue,
					StartToCloseTimeout: 60 * time.Second,
					RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
				}
				ctx = workflow.WithActivityOptions(ctx, cbOpts)
				_ = workflow.ExecuteActivity(ctx, input.CompletionActivityName, cbInput).Get(ctx, nil)
			}

			return result, nil
		}

		// Update current node for next iteration.
		snapshot.CurrentNodeID = stepResult.NextNodeID
	}

	// Max steps exceeded.
	result := &pathwalk.RunResult{
		Output:    snapshot.Output,
		Variables: state.Variables,
		Steps:     state.Steps,
		Reason:    "max_steps",
	}

	// Call completion callback if configured.
	if input.CompletionTaskQueue != "" && input.CompletionActivityName != "" {
		cbInput := CompletionCallbackInput{
			Data:   input.CompletionData,
			Result: result,
		}
		cbOpts := workflow.ActivityOptions{
			TaskQueue:           input.CompletionTaskQueue,
			StartToCloseTimeout: 60 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
		}
		ctx = workflow.WithActivityOptions(ctx, cbOpts)
		_ = workflow.ExecuteActivity(ctx, input.CompletionActivityName, cbInput).Get(ctx, nil)
	}

	return result, nil
}
