package pathwalk

import (
	"context"
	"fmt"
	"log"
)

const defaultMaxSteps = 50

// Engine executes a parsed pathway using an LLM and optional tools.
type Engine struct {
	pathway         *Pathway
	llm             LLMClient
	tools           []Tool
	maxSteps        int
	verbose         bool
	globalNodeCheck bool
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

// WithVerbose enables step-by-step logging.
func WithVerbose(v bool) EngineOption {
	return func(e *Engine) {
		e.verbose = v
	}
}

// WithGlobalNodeCheck enables or disables the per-step global node interception.
// By default it is enabled whenever the pathway has at least one global node.
func WithGlobalNodeCheck(enabled bool) EngineOption {
	return func(e *Engine) {
		e.globalNodeCheck = enabled
	}
}

// NewEngine creates an Engine for the given pathway and LLM client.
func NewEngine(pathway *Pathway, llm LLMClient, opts ...EngineOption) *Engine {
	e := &Engine{
		pathway:         pathway,
		llm:             llm,
		maxSteps:        defaultMaxSteps,
		globalNodeCheck: len(pathway.GlobalNodes) > 0,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Run executes the pathway with `task` as the initial context.
func (e *Engine) Run(ctx context.Context, task string) (*RunResult, error) {
	if e.verbose {
		ctx = withVerboseCtx(ctx)
	}
	state := newState(task)
	currentNode := e.pathway.StartNode

	for step := 0; step < e.maxSteps; step++ {
		if currentNode == nil {
			return &RunResult{
				Output:    "",
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "missing_node",
			}, nil
		}

		// Check global nodes before executing the current node.
		if e.globalNodeCheck {
			globalNode, err := checkGlobalNode(ctx, e.pathway.GlobalNodes, state, e.llm)
			if err != nil {
				if e.verbose {
					log.Printf("[warn] global node check failed: %v", err)
				}
				// Non-fatal: fall through and execute currentNode as normal.
			} else if globalNode != nil {
				if e.verbose {
					log.Printf("[global] intercepted → %q", globalNode.Name)
				}
				currentNode = globalNode
			}
		}

		if e.verbose {
			log.Printf("[step %d] node=%q type=%s", step+1, currentNode.Name, currentNode.Type)
		}

		// Skip unsupported node types
		switch currentNode.Type {
		case NodeTypeLLM, NodeTypeTerminal, NodeTypeWebhook, NodeTypeRoute:
			// handled below
		default:
			log.Printf("[warn] skipping unsupported node type %q at node %q", currentNode.Type, currentNode.Name)
			edges := e.pathway.EdgesFrom[currentNode.ID]
			if len(edges) == 0 {
				return &RunResult{
					Variables: state.Variables,
					Steps:     state.Steps,
					Reason:    "dead_end",
				}, nil
			}
			currentNode = e.pathway.NodeByID[edges[0].Target]
			continue
		}

		// Execute the node
		out, err := executeNode(ctx, currentNode, state, e.llm, e.tools)
		if err != nil {
			return &RunResult{
				Output:     "",
				Variables:  state.Variables,
				Steps:      state.Steps,
				Reason:     "error",
				FailedNode: currentNode.Name,
			}, fmt.Errorf("executing node %q: %w", currentNode.Name, err)
		}

		// Apply extracted variables to state
		if out.Vars != nil {
			state.SetVars(out.Vars)
		}

		// Find outgoing edges
		edges := e.pathway.EdgesFrom[currentNode.ID]

		// Determine next node
		var nextNodeID, routeReason string
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
			if e.verbose {
				log.Printf("[end] %s\n%s", currentNode.Name, out.Text)
			}
			return &RunResult{
				Output:    out.Text,
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "terminal",
			}, nil
		}

		nextNodeID, routeReason, err = chooseNextNode(ctx, currentNode, out, state, edges, e.llm)
		if err != nil {
			return &RunResult{
				Output:     out.Text,
				Variables:  state.Variables,
				Steps:      state.Steps,
				Reason:     "error",
				FailedNode: currentNode.Name,
			}, fmt.Errorf("routing from node %q: %w", currentNode.Name, err)
		}

		if e.verbose {
			nextName := nextNodeID
			if n, ok := e.pathway.NodeByID[nextNodeID]; ok {
				nextName = n.Name
			}
			log.Printf("[route] %s → %s (%s)\nOutput: %s",
				currentNode.Name, nextName, routeReason, truncate(out.Text, 300))
		}

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
			return &RunResult{
				Output:    out.Text,
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "dead_end",
			}, nil
		}

		next, ok := e.pathway.NodeByID[nextNodeID]
		if !ok {
			return &RunResult{
				Output:    out.Text,
				Variables: state.Variables,
				Steps:     state.Steps,
				Reason:    "error",
			}, fmt.Errorf("next node %q not found in pathway", nextNodeID)
		}
		currentNode = next
	}

	return &RunResult{
		Output:    "",
		Variables: state.Variables,
		Steps:     state.Steps,
		Reason:    "max_steps",
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
