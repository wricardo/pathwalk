package pathwalk

import (
	"context"
	"fmt"
	"log/slog"
)

const defaultMaxSteps = 50

// StepResult captures what happened during a single node execution.
type StepResult struct {
	Step       Step       // The step record for this execution
	NextNodeID string     // Empty when Done=true (terminal, dead_end, error, or max_node_visits)
	Done       bool       // True when the run should terminate
	Reason     string     // "terminal", "dead_end", "error", "missing_node", "max_node_visits"
	Output     string     // Text output from the node
	Error      string     // Error message if Reason=="error" or "max_node_visits"
	FailedNode string     // Name of the node that caused the stop when Reason is "error" or "max_node_visits"
	Logs       []LogEntry // Log records emitted during this step
}

// Engine executes a parsed pathway using an LLM and optional tools.
type Engine struct {
	pathway         *Pathway
	llm             LLMClient
	tools           []Tool
	maxSteps        int
	globalNodeCheck bool
	log             *slog.Logger
}

// EngineOption is a functional option for Engine.
type EngineOption func(*Engine)

// WithTools adds tools to the engine's tool registry.
func WithTools(tools ...Tool) EngineOption {
	return func(e *Engine) {
		e.tools = append(e.tools, tools...)
	}
}

// WithMaxSteps sets the maximum number of nodes to visit.
func WithMaxSteps(n int) EngineOption {
	return func(e *Engine) {
		e.maxSteps = n
	}
}

// WithGlobalNodeCheck enables or disables the per-step global node interception.
// By default it is enabled whenever the pathway has at least one global node.
func WithGlobalNodeCheck(enabled bool) EngineOption {
	return func(e *Engine) {
		e.globalNodeCheck = enabled
	}
}

// WithLogger sets the logger for the engine.
// If not set, slog.Default() is used.
func WithLogger(log *slog.Logger) EngineOption {
	return func(e *Engine) {
		e.log = log
	}
}

// NewEngine creates an Engine for the given pathway and LLM client.
func NewEngine(pathway *Pathway, llm LLMClient, opts ...EngineOption) *Engine {
	e := &Engine{
		pathway:         pathway,
		llm:             llm,
		maxSteps:        defaultMaxSteps,
		globalNodeCheck: len(pathway.GlobalNodes) > 0,
		log:             slog.Default(),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// NewState creates a new execution state for the given task.
// Use this to initialize state for calls to Engine.Step.
func NewState(task string) *State {
	return newState(task)
}

// Step executes a single node in the pathway and returns the result.
// State is mutated in place (variables merged, step appended).
// Call this repeatedly with the returned NextNodeID until StepResult.Done is true.
//
// Example:
//
//	state := NewState("my task")
//	for {
//	    result, err := engine.Step(ctx, state, startNodeID)
//	    if err != nil || result.Done {
//	        break
//	    }
//	    nodeID = result.NextNodeID
//	}
func (e *Engine) Step(ctx context.Context, state *State, nodeID string) (*StepResult, error) {
	lc := newLogCapture(e.log.Handler())
	stepLog := slog.New(lc)

	currentNode, ok := e.pathway.NodeByID[nodeID]
	if !ok {
		return &StepResult{
			Done:   true,
			Reason: "missing_node",
			Error:  fmt.Sprintf("node %q not found in pathway", nodeID),
			Logs:   lc.flush(),
		}, nil
	}

	// Check global nodes before executing the current node.
	if e.globalNodeCheck {
		globalNode, err := checkGlobalNode(ctx, e.pathway.GlobalNodes, state, e.llm)
		if err != nil {
			stepLog.Warn("global node check failed", "error", err)
			// Non-fatal: fall through and execute currentNode as normal.
		} else if globalNode != nil {
			stepLog.Debug("global node intercepted", "node", globalNode.Name)
			currentNode = globalNode
		}
	}

	stepLog.Debug("executing step", "node", currentNode.Name, "type", currentNode.Type)

	// Enforce per-node visit cap.
	state.VisitCounts[currentNode.ID]++
	nodeVisitLimit := e.pathway.MaxVisitsPerNode
	if currentNode.MaxVisits > 0 {
		nodeVisitLimit = currentNode.MaxVisits
	}
	if nodeVisitLimit > 0 && state.VisitCounts[currentNode.ID] > nodeVisitLimit {
		return &StepResult{
			Done:       true,
			Reason:     "max_node_visits",
			Error:      fmt.Sprintf("node %q exceeded max visits (%d)", currentNode.Name, nodeVisitLimit),
			FailedNode: currentNode.Name,
			Logs:       lc.flush(),
		}, nil
	}

	// Skip unsupported node types
	switch currentNode.Type {
	case NodeTypeLLM, NodeTypeTerminal, NodeTypeWebhook, NodeTypeRoute:
		// handled below
	default:
		stepLog.Warn("skipping unsupported node type", "type", currentNode.Type, "node", currentNode.Name)
		edges := e.pathway.EdgesFrom[currentNode.ID]
		if len(edges) == 0 {
			return &StepResult{
				Done:   true,
				Reason: "dead_end",
				Logs:   lc.flush(),
			}, nil
		}
		return &StepResult{
			NextNodeID: edges[0].Target,
			Output:     "",
			Logs:       lc.flush(),
		}, nil
	}

	// Execute the node
	out, err := executeNode(ctx, currentNode, state, e.llm, e.tools, stepLog)
	if err != nil {
		return &StepResult{
			Done:       true,
			Reason:     "error",
			Error:      fmt.Sprintf("executing node %q: %v", currentNode.Name, err),
			FailedNode: currentNode.Name,
			Logs:       lc.flush(),
		}, nil
	}

	// Apply extracted variables to state
	if out.Vars != nil {
		state.SetVars(out.Vars)
	}

	// Find outgoing edges
	edges := e.pathway.EdgesFrom[currentNode.ID]

	// Handle terminal nodes
	if currentNode.Type == NodeTypeTerminal {
		sl := Step{
			NodeID:      currentNode.ID,
			NodeName:    currentNode.Name,
			Output:      out.Text,
			Vars:        copyVars(out.Vars),
			ToolCalls:   out.ToolCalls,
			RouteReason: "terminal",
			NextNode:    "(end)",
		}
		state.Steps = append(state.Steps, sl)
		stepLog.Debug("terminal node reached", "node", currentNode.Name)

		// Terminal nodes hold static text; the meaningful answer is in the
		// last LLM/webhook step that ran before the terminal. Walk back
		// through prior steps (skip the terminal we just appended) to find it.
		output := out.Text
		for i := len(state.Steps) - 2; i >= 0; i-- {
			if state.Steps[i].Output != "" {
				output = state.Steps[i].Output
				break
			}
		}
		return &StepResult{
			Step:       sl,
			Done:       true,
			Reason:     "terminal",
			Output:     output,
			Logs:       lc.flush(),
		}, nil
	}

	// Route to next node
	nextNodeID, routeReason, err := chooseNextNode(ctx, currentNode, out, state, edges, e.llm)
	if err != nil {
		return &StepResult{
			Done:       true,
			Reason:     "error",
			Error:      fmt.Sprintf("routing from node %q: %v", currentNode.Name, err),
			Output:     out.Text,
			FailedNode: currentNode.Name,
			Logs:       lc.flush(),
		}, nil
	}

	nextName := nextNodeID
	if n, ok := e.pathway.NodeByID[nextNodeID]; ok {
		nextName = n.Name
	}
	stepLog.Debug("routing to next node", "from", currentNode.Name, "to", nextName, "reason", routeReason)

	// Record step
	sl := Step{
		NodeID:      currentNode.ID,
		NodeName:    currentNode.Name,
		Output:      out.Text,
		Vars:        copyVars(out.Vars),
		ToolCalls:   out.ToolCalls,
		RouteReason: routeReason,
		NextNode:    nextNodeID,
	}
	state.Steps = append(state.Steps, sl)

	if nextNodeID == "" {
		return &StepResult{
			Step:   sl,
			Done:   true,
			Reason: "dead_end",
			Output: out.Text,
			Logs:   lc.flush(),
		}, nil
	}

	return &StepResult{
		Step:       sl,
		NextNodeID: nextNodeID,
		Output:     out.Text,
		Logs:       lc.flush(),
	}, nil
}

// Run executes the pathway with `task` as the initial context.
func (e *Engine) Run(ctx context.Context, task string) (*RunResult, error) {
	if e.pathway.StartNode == nil {
		return &RunResult{Reason: "missing_node"}, nil
	}

	state := newState(task)
	nodeID := e.pathway.StartNode.ID

	// Pathway-level maxTurns overrides the engine default when set.
	stepCap := e.maxSteps
	if e.pathway.MaxTurns > 0 && e.pathway.MaxTurns < stepCap {
		stepCap = e.pathway.MaxTurns
	}

	var allLogs []LogEntry

	for i := 0; i < stepCap; i++ {
		result, _ := e.Step(ctx, state, nodeID) // Step never returns a non-nil error
		allLogs = append(allLogs, result.Logs...)

		if result.Done {
			rr := &RunResult{
				Output:     result.Output,
				Variables:  state.Variables,
				Steps:      state.Steps,
				Reason:     result.Reason,
				FailedNode: result.FailedNode,
				Logs:       allLogs,
			}
			// "error" and "missing_node" are unexpected mid-run failures;
			// surface them as Go errors to preserve the Run() contract.
			if result.Reason == "error" || result.Reason == "missing_node" {
				return rr, fmt.Errorf("%s", result.Error)
			}
			return rr, nil
		}

		nodeID = result.NextNodeID
	}

	return &RunResult{
		Variables: state.Variables,
		Steps:     state.Steps,
		Reason:    "max_steps",
		Logs:      allLogs,
	}, nil
}

func copyVars(vars map[string]any) map[string]any {
	if vars == nil {
		return nil
	}
	out := make(map[string]any, len(vars))
	for k, v := range vars {
		out[k] = v
	}
	return out
}
