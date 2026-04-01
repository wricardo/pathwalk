package pathwalk

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

const defaultMaxSteps = 50

// StepResult captures what happened during a single node execution.
type StepResult struct {
	Step          Step           // The step record for this execution
	NextNodeID    string         // Empty when Done=true (terminal, dead_end, error, or max_node_visits)
	Done          bool           // True when the run should terminate
	Reason        string         // "terminal", "dead_end", "error", "missing_node", "max_node_visits", "checkpoint"
	Output        string         // Text output from the node
	Error         string         // Error message if Reason=="error" or "max_node_visits"
	FailedNode    string         // Name of the node that caused the stop when Reason is "error" or "max_node_visits"
	Logs          []LogEntry     // Log records emitted during this step
	WaitCondition *WaitCondition // Non-nil when the step is waiting for external input (checkpoint node)
}

// Engine executes a parsed pathway using an LLM and optional tools.
type Engine struct {
	pathway         *Pathway
	llm             LLMClient
	tools           []Tool
	maxSteps        int
	globalNodeCheck bool
	log             *slog.Logger
	stepCallbacks   []func(*StepResult)
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

// WithStepCallback registers a callback invoked after each step completes in Run.
// The callback receives the StepResult (including WaitCondition when a checkpoint
// suspends). Multiple callbacks may be registered; they are called in order.
// Useful for live progress updates, incremental persistence, or monitoring.
func WithStepCallback(fn func(*StepResult)) EngineOption {
	return func(e *Engine) {
		e.stepCallbacks = append(e.stepCallbacks, fn)
	}
}

// NewEngine creates an Engine for the given pathway and LLM client.
// Panics if pathway is nil. llm may be nil when the pathway declares providers;
// in that case the engine automatically builds a RoutingClient from pathway.Providers.
func NewEngine(pathway *Pathway, llm LLMClient, opts ...EngineOption) *Engine {
	if pathway == nil {
		panic("pathwalk: NewEngine called with nil pathway")
	}
	if llm == nil {
		if len(pathway.Providers) == 0 {
			panic("pathwalk: NewEngine called with nil llm and pathway has no providers configured")
		}
		llm = NewRoutingClient(pathway.Providers)
	}
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
	stepStart := time.Now()
	stepsBefore := len(state.Steps)

	// enrichLastStep stamps timing and a state snapshot onto the step that was
	// appended during this call. Called via defer so every return path is covered.
	enrichLastStep := func() {
		if len(state.Steps) <= stepsBefore {
			return // no step was appended (e.g. missing_node)
		}
		last := &state.Steps[len(state.Steps)-1]
		last.StartedAt = stepStart
		last.DurationMs = int(time.Since(stepStart).Milliseconds())
		last.StateSnapshot = make(map[string]any, len(state.Variables))
		for k, v := range state.Variables {
			last.StateSnapshot[k] = v
		}
	}
	defer enrichLastStep()

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

	// Handle agent nodes — always suspend.
	if currentNode.Type == NodeTypeAgent {
		task := resolveTemplate(currentNode.AgentTask, state.Variables)
		wc := &WaitCondition{
			Mode:   CheckpointModeAgent,
			NodeID: currentNode.ID,
			NodeName: currentNode.Name,
			Prompt: currentNode.Name,
			AgentTask: &AgentTask{
				Name:      currentNode.Name,
				AgentID:   currentNode.AgentID,
				Task:      task,
				OutputVar: currentNode.AgentOutputVar,
			},
		}
		sl := Step{
			NodeID: currentNode.ID,
			NodeName: currentNode.Name,
			Output: fmt.Sprintf("[agent] Spawning child agent %q (task: %s)", currentNode.Name, truncate(task, 100)),
		}
		state.Steps = append(state.Steps, sl)
		stepLog.Debug("agent node suspended", "node", currentNode.Name, "agentId", currentNode.AgentID)
		return &StepResult{
			Step:          sl,
			Reason:        "checkpoint",
			WaitCondition: wc,
			Logs:          lc.flush(),
		}, nil
	}

	// Handle team nodes — always suspend.
	if currentNode.Type == NodeTypeTeam {
		var tasks []AgentTask
		for _, a := range currentNode.TeamAgents {
			tasks = append(tasks, AgentTask{
				Name:      a.Name,
				AgentID:   a.AgentID,
				Task:      resolveTemplate(a.Task, state.Variables),
				OutputVar: a.OutputVar,
			})
		}
		wc := &WaitCondition{
			Mode:         CheckpointModeTeam,
			NodeID:       currentNode.ID,
			NodeName:     currentNode.Name,
			Prompt:       currentNode.Name,
			TeamTasks:    tasks,
			TeamStrategy: currentNode.TeamStrategy,
		}
		agentNames := make([]string, len(tasks))
		for i, t := range tasks {
			agentNames[i] = t.Name
		}
		sl := Step{
			NodeID:   currentNode.ID,
			NodeName: currentNode.Name,
			Output:   fmt.Sprintf("[team:%s] Spawning %d agents: %s", currentNode.TeamStrategy, len(tasks), fmt.Sprintf("%v", agentNames)),
		}
		state.Steps = append(state.Steps, sl)
		stepLog.Debug("team node suspended", "node", currentNode.Name, "strategy", currentNode.TeamStrategy, "agents", len(tasks))
		return &StepResult{
			Step:          sl,
			Reason:        "checkpoint",
			WaitCondition: wc,
			Logs:          lc.flush(),
		}, nil
	}

	// Handle checkpoint nodes that require external input (human modes suspend).
	if currentNode.Type == NodeTypeCheckpoint {
		switch currentNode.CheckpointMode {
		case CheckpointModeHumanInput, CheckpointModeHumanApproval, CheckpointModeWait:
			wc := &WaitCondition{
				Mode:         currentNode.CheckpointMode,
				NodeID:       currentNode.ID,
				NodeName:     currentNode.Name,
				Prompt:       currentNode.CheckpointPrompt,
				VariableName: currentNode.CheckpointVariable,
				Variables:    currentNode.ExtractVars,
				WaitDuration: currentNode.WaitDuration,
			}
			if currentNode.CheckpointMode == CheckpointModeHumanApproval {
				wc.Options = currentNode.CheckpointOptions
				if len(wc.Options) == 0 {
					wc.Options = []string{"approve", "reject"}
				}
			}
			sl := Step{
				NodeID:   currentNode.ID,
				NodeName: currentNode.Name,
				Output:   fmt.Sprintf("[%s] %s", currentNode.CheckpointMode, currentNode.CheckpointPrompt),
			}
			state.Steps = append(state.Steps, sl)
			stepLog.Debug("checkpoint suspended", "node", currentNode.Name, "mode", currentNode.CheckpointMode)
			return &StepResult{
				Step:          sl,
				Reason:        "checkpoint",
				WaitCondition: wc,
				Logs:          lc.flush(),
			}, nil
		case CheckpointModeAuto, CheckpointModeLLMEval:
			// synchronous — fall through to executeNode
		}
	}

	// Skip unsupported node types
	switch currentNode.Type {
	case NodeTypeLLM, NodeTypeTerminal, NodeTypeWebhook, NodeTypeRoute, NodeTypeCheckpoint, NodeTypeAgent, NodeTypeTeam:
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
		// If executeNode returned partial output (e.g. tool calls accumulated
		// before hitting a limit), record them in a failed step so the full
		// execution trace is visible to callers.
		var partialToolCalls []ToolCall
		if out != nil {
			partialToolCalls = out.ToolCalls
		}
		failedStep := Step{
			NodeID:    currentNode.ID,
			NodeName:  currentNode.Name,
			ToolCalls: partialToolCalls,
		}
		state.Steps = append(state.Steps, failedStep)
		return &StepResult{
			Step:       failedStep,
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

	// Check if a response pathway set a routing override via $tool_route.
	if routeNodeID, ok := state.Variables["$tool_route"].(string); ok && routeNodeID != "" {
		delete(state.Variables, "$tool_route")
		if _, exists := e.pathway.NodeByID[routeNodeID]; exists {
			stepLog.Debug("tool response pathway override", "from", currentNode.Name, "to", routeNodeID)

			sl := Step{
				NodeID:      currentNode.ID,
				NodeName:    currentNode.Name,
				Output:      out.Text,
				Vars:        copyVars(out.Vars),
				ToolCalls:   out.ToolCalls,
				RouteReason: "tool_response_pathway",
				NextNode:    routeNodeID,
			}
			state.Steps = append(state.Steps, sl)
			return &StepResult{
				Step:       sl,
				NextNodeID: routeNodeID,
				Output:     out.Text,
				Logs:       lc.flush(),
			}, nil
		}
		stepLog.Warn("tool response pathway target not found", "nodeId", routeNodeID)
	}

	// Route to next node
	nextNodeID, routeReason, err := chooseNextNode(ctx, currentNode, out, state, edges, e.llm, stepLog)
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
//
// Unlike Step, Run can return both a non-nil *RunResult and a non-nil error
// simultaneously when Reason is "error" or "missing_node". Callers should
// always inspect both: the result contains the partial execution state (steps
// taken, variables extracted so far) and the error describes what went wrong.
func (e *Engine) Run(ctx context.Context, task string) (*RunResult, error) {
	if e.pathway.StartNode == nil {
		return &RunResult{Reason: "missing_node"}, fmt.Errorf("pathway has no start node")
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

		for _, cb := range e.stepCallbacks {
			cb(result)
		}

		// Checkpoint suspension: Run() cannot continue without external input.
		if result.WaitCondition != nil {
			return &RunResult{
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "checkpoint",
				Logs:      allLogs,
			}, fmt.Errorf("run suspended at checkpoint %q: use Step/ResumeStep for checkpoint support", result.WaitCondition.NodeName)
		}

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
				return rr, errors.New(result.Error)
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

// ResumeStep resumes execution after a checkpoint suspension.
// nodeID must be the checkpoint node that suspended (WaitCondition.NodeID).
// The response is applied to state, and the engine routes to the next node.
func (e *Engine) ResumeStep(ctx context.Context, state *State, nodeID string, response CheckpointResponse) (*StepResult, error) {
	resumeStart := time.Now()
	stepsBefore := len(state.Steps)
	defer func() {
		if len(state.Steps) > stepsBefore {
			last := &state.Steps[len(state.Steps)-1]
			last.StartedAt = resumeStart
			last.DurationMs = int(time.Since(resumeStart).Milliseconds())
			last.StateSnapshot = make(map[string]any, len(state.Variables))
			for k, v := range state.Variables {
				last.StateSnapshot[k] = v
			}
		}
	}()

	node, ok := e.pathway.NodeByID[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %q not found in pathway", nodeID)
	}
	switch node.Type {
	case NodeTypeCheckpoint, NodeTypeAgent, NodeTypeTeam:
		// valid — these node types support resume
	default:
		return nil, fmt.Errorf("node %q is type %q, not a resumable node (checkpoint, agent, or team)", nodeID, node.Type)
	}

	// Store the response value in the designated variable.
	if node.CheckpointVariable != "" {
		state.Variables[node.CheckpointVariable] = response.Value
	}
	// Merge any extra variables from the response.
	if response.Vars != nil {
		state.SetVars(response.Vars)
	}

	// Route to the next node using outgoing edges.
	edges := e.pathway.EdgesFrom[nodeID]
	nextNodeID := ""
	routeReason := "no outgoing edges"
	if len(edges) == 1 {
		nextNodeID = edges[0].Target
		routeReason = "single edge"
	} else if len(edges) > 1 {
		// For multiple edges, use the checkpoint variable value to pick.
		// This delegates to the standard edge-following; the caller
		// typically chains a Route node after the checkpoint.
		nextNodeID = edges[0].Target
		routeReason = "first edge (multiple)"
	}

	sl := Step{
		NodeID:      node.ID,
		NodeName:    node.Name,
		Vars:        copyVars(response.Vars),
		RouteReason: routeReason,
		NextNode:    nextNodeID,
		ResumeValue: response.Value,
		ChildRuns:   response.ChildRuns,
	}
	state.Steps = append(state.Steps, sl)

	if nextNodeID == "" {
		return &StepResult{
			Step:   sl,
			Done:   true,
			Reason: "dead_end",
		}, nil
	}

	return &StepResult{
		Step:       sl,
		NextNodeID: nextNodeID,
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
