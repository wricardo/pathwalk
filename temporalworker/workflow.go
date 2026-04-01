package temporalworker

import (
	"fmt"
	"time"

	"github.com/wricardo/pathwalk"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const TaskQueue = "pathwalk"

// ResumeSignalName is the Temporal signal name used to resume a workflow blocked at a checkpoint.
const ResumeSignalName = "resume"

// ResumeSignal carries the human response to a checkpoint.
type ResumeSignal struct {
	Value string         `json:"value"`
	Vars  map[string]any `json:"vars,omitempty"`
}

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

	// Optional progress callback. When set, PathwayWorkflow calls this activity
	// after each step so callers can see incremental progress (live step streaming).
	ProgressTaskQueue    string
	ProgressActivityName string
}

// CompletionCallbackInput is the input to the completion callback activity.
// Called by PathwayWorkflow after the pathway run finishes.
type CompletionCallbackInput struct {
	Data   string            // echoes PathwayInput.CompletionData
	Result *pathwalk.RunResult // nil on workflow error
	Err    string            // set if workflow-level error occurred
}

// ProgressCallbackInput is the input to the progress callback activity.
// Called by PathwayWorkflow after each step completes.
type ProgressCallbackInput struct {
	Data     string         // echoes PathwayInput.CompletionData
	Snapshot *RunSnapshot   // current run state
}

// RunSnapshot captures the current state of a running or completed workflow.
type RunSnapshot struct {
	Status        string                  // "running", "waiting", or terminal reason
	CurrentNodeID string
	Output        string
	Variables     map[string]any
	Steps         []pathwalk.Step
	Error         string
	WaitCondition *pathwalk.WaitCondition `json:"waitCondition,omitempty"` // non-nil when blocked at a checkpoint
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

	stepCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
	})

	// callCompletion invokes the optional completion callback activity.
	callCompletion := func(result *pathwalk.RunResult, errStr string) {
		if input.CompletionTaskQueue == "" || input.CompletionActivityName == "" {
			return
		}
		cbInput := CompletionCallbackInput{
			Data:   input.CompletionData,
			Result: result,
			Err:    errStr,
		}
		cbCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           input.CompletionTaskQueue,
			StartToCloseTimeout: 60 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
		})
		if err := workflow.ExecuteActivity(cbCtx, input.CompletionActivityName, cbInput).Get(cbCtx, nil); err != nil {
			workflow.GetLogger(ctx).Warn("completion callback failed", "error", err)
		}
	}

	// callProgress invokes the optional progress callback activity after each step.
	callProgress := func() {
		if input.ProgressTaskQueue == "" || input.ProgressActivityName == "" {
			return
		}
		pCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			TaskQueue:           input.ProgressTaskQueue,
			StartToCloseTimeout: 10 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		cbInput := ProgressCallbackInput{
			Data:     input.CompletionData,
			Snapshot: &snapshot,
		}
		if err := workflow.ExecuteActivity(pCtx, input.ProgressActivityName, cbInput).Get(pCtx, nil); err != nil {
			workflow.GetLogger(ctx).Warn("progress callback failed", "error", err)
		}
	}

	// Register the resume signal channel before the loop so Temporal buffers any
	// early signals sent before the workflow reaches a checkpoint.
	resumeCh := workflow.GetSignalChannel(ctx, ResumeSignalName)

	// Execute steps in a loop.
	for step := 0; step < maxSteps; step++ {
		if snapshot.CurrentNodeID == "" {
			// This shouldn't happen, but handle gracefully.
			result := &pathwalk.RunResult{
				Output:    snapshot.Output,
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "missing_node",
			}
			callCompletion(result, "")
			return result, nil
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

		var result *StepActivityResult
		if err := workflow.ExecuteActivity(stepCtx, (*PathwayActivities).ExecuteStep, stepInput).Get(stepCtx, &result); err != nil {
			runResult := &pathwalk.RunResult{
				Output:     snapshot.Output,
				Variables:  state.Variables,
				Steps:      state.Steps,
				Reason:     "error",
				FailedNode: snapshot.CurrentNodeID,
			}
			callCompletion(runResult, err.Error())
			return runResult, fmt.Errorf("executing step at node %q: %w", snapshot.CurrentNodeID, err)
		}

		// Update state and snapshot from activity result.
		state = result.State
		stepResult := result.StepResult

		snapshot.Variables = state.Variables
		snapshot.Steps = state.Steps
		snapshot.Output = stepResult.Output
		callProgress()

		// Check if the run is done.
		if stepResult.Done {
			runResult := &pathwalk.RunResult{
				Output:     stepResult.Output,
				Variables:  state.Variables,
				Steps:      state.Steps,
				Reason:     stepResult.Reason,
				FailedNode: stepResult.FailedNode,
			}
			callCompletion(runResult, "")
			return runResult, nil
		}

		// Checkpoint: block on the resume signal until the caller sends a response.
		if stepResult.WaitCondition != nil {
			snapshot.Status = "waiting"
			snapshot.WaitCondition = stepResult.WaitCondition
			snapshot.CurrentNodeID = stepResult.WaitCondition.NodeID

			var sig ResumeSignal
			resumeCh.Receive(ctx, &sig)

			snapshot.Status = "running"
			snapshot.WaitCondition = nil

			resumeInput := ResumeStepActivityInput{
				PathwayJSON:  input.PathwayJSON,
				State:        state,
				ResumeNodeID: stepResult.WaitCondition.NodeID,
				Signal:       sig,
				LLMModel:     input.LLMModel,
				LLMBaseURL:   input.LLMBaseURL,
				LLMAPIKey:    input.LLMAPIKey,
				Verbose:      input.Verbose,
			}
			var resumeResult *ResumeStepActivityResult
			if err := workflow.ExecuteActivity(stepCtx, (*PathwayActivities).ExecuteResumeStep, resumeInput).Get(stepCtx, &resumeResult); err != nil {
				runResult := &pathwalk.RunResult{
					Output:     snapshot.Output,
					Variables:  state.Variables,
					Steps:      state.Steps,
					Reason:     "error",
					FailedNode: stepResult.WaitCondition.NodeID,
				}
				callCompletion(runResult, err.Error())
				return runResult, fmt.Errorf("resuming checkpoint at node %q: %w", stepResult.WaitCondition.NodeID, err)
			}

			state = resumeResult.State
			snapshot.Variables = state.Variables
			snapshot.Steps = state.Steps
			callProgress()

			if resumeResult.StepResult.Done {
				runResult := &pathwalk.RunResult{
					Output:     resumeResult.StepResult.Output,
					Variables:  state.Variables,
					Steps:      state.Steps,
					Reason:     resumeResult.StepResult.Reason,
					FailedNode: resumeResult.StepResult.FailedNode,
				}
				callCompletion(runResult, "")
				return runResult, nil
			}

			snapshot.CurrentNodeID = resumeResult.StepResult.NextNodeID
			continue
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
	callCompletion(result, "")
	return result, nil
}
