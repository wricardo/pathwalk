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
}

// StartRun kicks off a PathwayWorkflow and returns the workflow ID immediately.
// The caller can later use GetResult or WaitForResult with the returned workflow ID.
// Pass opts.WorkflowID for idempotency; if empty, Temporal generates a UUID.
func StartRun(ctx context.Context, c client.Client, input PathwayInput, opts RunOptions) (string, error) {
	wopts := client.StartWorkflowOptions{
		ID:        opts.WorkflowID,
		TaskQueue: TaskQueue,
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

// WaitForResult blocks until the workflow finishes and returns the final RunResult.
// Pass runID="" to use the default empty run ID (appropriate for single-run workflows).
func WaitForResult(ctx context.Context, c client.Client, workflowID, runID string) (*pathwalk.RunResult, error) {
	var result pathwalk.RunResult
	if err := c.GetWorkflow(ctx, workflowID, runID).Get(ctx, &result); err != nil {
		return nil, fmt.Errorf("waiting for workflow: %w", err)
	}
	return &result, nil
}
