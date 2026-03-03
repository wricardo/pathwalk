package temporalworker

import (
	"context"
	"fmt"

	"github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/tools"
	"go.temporal.io/sdk/activity"
)

// StepActivityInput is the input to the ExecuteStep activity.
type StepActivityInput struct {
	PathwayJSON   []byte          // JSON-encoded pathway
	State         *pathwalk.State // Current execution state
	CurrentNodeID string
	LLMModel      string
	LLMBaseURL    string
	LLMAPIKey     string
	Verbose       bool
}

// StepActivityResult is the output of the ExecuteStep activity.
type StepActivityResult struct {
	State      *pathwalk.State
	StepResult *pathwalk.StepResult
}

// PathwayActivities holds the activity implementations.
type PathwayActivities struct {
	// LLMClientOverride optionally overrides the LLM client (for testing).
	// If nil, one is created from the input credentials.
	LLMClientOverride pathwalk.LLMClient
}

// ExecuteStep is a Temporal activity that executes a single node step.
// It parses the pathway, creates an engine, and calls engine.Step().
func (a *PathwayActivities) ExecuteStep(ctx context.Context, input StepActivityInput) (*StepActivityResult, error) {
	// Record a heartbeat to show progress.
	activity.RecordHeartbeat(ctx, "node="+input.CurrentNodeID)

	// Parse the pathway.
	pathway, err := pathwalk.ParsePathwayBytes(input.PathwayJSON)
	if err != nil {
		// Pathway parse errors — return early.
		activity.RecordHeartbeat(ctx, fmt.Sprintf("parse_error=%v", err))
		return nil, fmt.Errorf("ParsePathway: %w", err)
	}

	// Create or use override LLM client.
	var llmClient pathwalk.LLMClient
	if a.LLMClientOverride != nil {
		llmClient = a.LLMClientOverride
	} else {
		llmClient = pathwalk.NewOpenAIClient(input.LLMAPIKey, input.LLMBaseURL, input.LLMModel)
	}

	// Create engine options.
	opts := []pathwalk.EngineOption{}

	// Inject GraphQL tools if endpoint is configured in pathway.
	if pathway.GraphQLEndpoint != "" {
		gt := &tools.GraphQLTool{Endpoint: pathway.GraphQLEndpoint}
		opts = append(opts, pathwalk.WithTools(gt.AsTools()...))
	}

	engine := pathwalk.NewEngine(pathway, llmClient, opts...)

	// Execute one step.
	stepResult, err := engine.Step(ctx, input.State, input.CurrentNodeID)
	if err != nil {
		// Execution errors are retryable.
		return nil, fmt.Errorf("engine.Step: %w", err)
	}

	return &StepActivityResult{
		State:      input.State,
		StepResult: stepResult,
	}, nil
}
