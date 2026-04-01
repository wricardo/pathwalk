package temporalworker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/tools"
	"go.temporal.io/sdk/activity"
)

// pathwayCache caches parsed Pathway values keyed by SHA-256 of the JSON bytes.
// A single parse result is safe to reuse across activity calls — Pathway is read-only after parsing.
var pathwayCache sync.Map // map[sha256-string]*pathwalk.Pathway

func cachedParsePathway(data []byte) (*pathwalk.Pathway, error) {
	key := sha256.Sum256(data)
	if cached, ok := pathwayCache.Load(key); ok {
		return cached.(*pathwalk.Pathway), nil
	}
	p, err := pathwalk.ParsePathwayBytes(data)
	if err != nil {
		return nil, err
	}
	pathwayCache.Store(key, p)
	return p, nil
}

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

// ResumeStepActivityInput is the input to the ExecuteResumeStep activity.
type ResumeStepActivityInput struct {
	PathwayJSON  []byte
	State        *pathwalk.State
	ResumeNodeID string        // WaitCondition.NodeID from the suspended step
	Signal       ResumeSignal  // the human response
	LLMModel     string
	LLMBaseURL   string
	LLMAPIKey    string
	Verbose      bool
}

// ResumeStepActivityResult is the output of the ExecuteResumeStep activity.
type ResumeStepActivityResult struct {
	State      *pathwalk.State
	StepResult *pathwalk.StepResult
}

// ExecuteResumeStep is a Temporal activity that calls engine.ResumeStep() to unblock
// a checkpoint and route to the next node.
func (a *PathwayActivities) ExecuteResumeStep(ctx context.Context, input ResumeStepActivityInput) (*ResumeStepActivityResult, error) {
	activity.RecordHeartbeat(ctx, "resume="+input.ResumeNodeID)

	pathway, err := cachedParsePathway(input.PathwayJSON)
	if err != nil {
		return nil, fmt.Errorf("ParsePathway: %w", err)
	}

	var llmClient pathwalk.LLMClient
	if a.LLMClientOverride != nil {
		llmClient = a.LLMClientOverride
	} else if len(pathway.Providers) == 0 {
		// No pathway-level providers — use credentials from the activity input.
		llmClient = pathwalk.NewOpenAIClient(input.LLMAPIKey, input.LLMBaseURL, input.LLMModel)
	}
	// else: pathway has providers; pass nil — NewEngine auto-builds a RoutingClient.

	opts := []pathwalk.EngineOption{}
	if pathway.GraphQLEndpoint != "" {
		gt := &tools.GraphQLTool{Endpoint: pathway.GraphQLEndpoint, Headers: pathway.GraphQLHeaders}
		opts = append(opts, pathwalk.WithTools(gt.AsTools()...))
	}
	for name, ep := range pathway.GraphQLEndpoints {
		gt := &tools.GraphQLTool{Endpoint: ep, Name: name, Headers: pathway.GraphQLEndpointHeaders[name]}
		opts = append(opts, pathwalk.WithTools(gt.AsTools()...))
	}

	engine := pathwalk.NewEngine(pathway, llmClient, opts...)

	response := pathwalk.CheckpointResponse{
		Value: input.Signal.Value,
		Vars:  input.Signal.Vars,
	}
	stepResult, err := engine.ResumeStep(ctx, input.State, input.ResumeNodeID, response)
	if err != nil {
		return nil, fmt.Errorf("engine.ResumeStep: %w", err)
	}

	return &ResumeStepActivityResult{
		State:      input.State,
		StepResult: stepResult,
	}, nil
}

// ExecuteStep is a Temporal activity that executes a single node step.
// It parses the pathway, creates an engine, and calls engine.Step().
func (a *PathwayActivities) ExecuteStep(ctx context.Context, input StepActivityInput) (*StepActivityResult, error) {
	// Record a heartbeat to show progress.
	activity.RecordHeartbeat(ctx, "node="+input.CurrentNodeID)

	// Parse the pathway (cached by SHA-256 of the JSON bytes).
	pathway, err := cachedParsePathway(input.PathwayJSON)
	if err != nil {
		// Pathway parse errors — return early.
		activity.RecordHeartbeat(ctx, fmt.Sprintf("parse_error=%v", err))
		return nil, fmt.Errorf("ParsePathway: %w", err)
	}

	// Create or use override LLM client.
	var llmClient pathwalk.LLMClient
	if a.LLMClientOverride != nil {
		llmClient = a.LLMClientOverride
	} else if len(pathway.Providers) == 0 {
		// No pathway-level providers — use credentials from the activity input.
		llmClient = pathwalk.NewOpenAIClient(input.LLMAPIKey, input.LLMBaseURL, input.LLMModel)
	}
	// else: pathway has providers; pass nil — NewEngine auto-builds a RoutingClient.

	// Create engine options.
	opts := []pathwalk.EngineOption{}

	// Inject GraphQL tools if endpoint is configured in pathway.
	if pathway.GraphQLEndpoint != "" {
		gt := &tools.GraphQLTool{Endpoint: pathway.GraphQLEndpoint, Headers: pathway.GraphQLHeaders}
		opts = append(opts, pathwalk.WithTools(gt.AsTools()...))
	}
	for name, ep := range pathway.GraphQLEndpoints {
		gt := &tools.GraphQLTool{Endpoint: ep, Name: name, Headers: pathway.GraphQLEndpointHeaders[name]}
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
