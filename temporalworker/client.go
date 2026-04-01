package temporalworker

import (
	"context"
	"fmt"

	"github.com/wricardo/pathwalk"
	"go.temporal.io/sdk/client"
)

// RunOptions configures a pathway workflow run.
type RunOptions struct {
	WorkflowID string // Optional; if empty, Temporal generates a UUID
	TaskQueue  string // Optional; if empty, defaults to TaskQueue ("pathwalk")
}

// StartRun kicks off a PathwayWorkflow and returns the workflow ID immediately.
// The caller can later use GetResult or WaitForResult with the returned workflow ID.
// Pass opts.WorkflowID for idempotency; if empty, Temporal generates a UUID.
// Pass opts.TaskQueue to override the default task queue (useful when PathwayWorkflow
// is registered on a non-pathwalk worker, e.g. jenny's "jenny" task queue).
func StartRun(ctx context.Context, c client.Client, input PathwayInput, opts RunOptions) (string, error) {
	tq := opts.TaskQueue
	if tq == "" {
		tq = TaskQueue
	}
	wopts := client.StartWorkflowOptions{
		ID:        opts.WorkflowID,
		TaskQueue: tq,
	}

	exec, err := c.ExecuteWorkflow(ctx, wopts, PathwayWorkflow, input)
	if err != nil {
		return "", fmt.Errorf("starting workflow: %w", err)
	}

	return exec.GetID(), nil
}

// GetResult queries the current state of a running or completed workflow.
// It returns a RunSnapshot showing the current node, variables, steps, and status.
// This is non-blocking and safe to call at any time.
func GetResult(ctx context.Context, c client.Client, workflowID string) (*RunSnapshot, error) {
	resp, err := c.QueryWorkflow(ctx, workflowID, "", "get-result")
	if err != nil {
		return nil, fmt.Errorf("querying workflow: %w", err)
	}

	var snapshot RunSnapshot
	if err := resp.Get(&snapshot); err != nil {
		return nil, fmt.Errorf("decoding query result: %w", err)
	}

	return &snapshot, nil
}

// SendResumeSignal sends a ResumeSignal to a PathwayWorkflow blocked at a checkpoint.
// workflowID must match the ID returned by StartRun.
func SendResumeSignal(ctx context.Context, c client.Client, workflowID string, sig ResumeSignal) error {
	if err := c.SignalWorkflow(ctx, workflowID, "", ResumeSignalName, sig); err != nil {
		return fmt.Errorf("sending resume signal to workflow %q: %w", workflowID, err)
	}
	return nil
}

// WaitForResult blocks until the workflow finishes and returns the final RunResult.
// Pass runID="" to use the default empty run ID (appropriate for single-run workflows).
func WaitForResult(ctx context.Context, c client.Client, workflowID, runID string) (*pathwalk.RunResult, error) {
	var result pathwalk.RunResult
	if err := c.GetWorkflow(ctx, workflowID, runID).Get(ctx, &result); err != nil {
		return nil, fmt.Errorf("waiting for workflow: %w", err)
	}
	return &result, nil
}
