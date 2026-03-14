package pathwalk

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
)

// chooseNextNode picks the next node to visit after executing `node`.
// For Default nodes: if single edge, follows it; otherwise uses LLM function call.
// For Route nodes: evaluates conditions against state variables.
// For Webhook nodes: follows the single outgoing edge.
func chooseNextNode(
	ctx context.Context,
	node *Node,
	out *nodeOutput,
	state *State,
	edges []*Edge,
	llm LLMClient,
	log *slog.Logger,
) (string, string, error) { // returns (targetNodeID, reason, error)

	if len(edges) == 0 {
		return "", "no outgoing edges", nil
	}

	switch node.Type {
	case NodeTypeRoute:
		return evaluateRouteNode(node, state)

	case NodeTypeLLM, NodeTypeWebhook:
		if len(edges) == 1 {
			return edges[0].Target, "single edge", nil
		}
		return llmRoute(ctx, node, out, state, edges, llm, log)

	default:
		return edges[0].Target, "default", nil
	}
}

// llmRoute uses the LLM to pick among multiple outgoing edges.
func llmRoute(
	ctx context.Context,
	node *Node,
	out *nodeOutput,
	state *State,
	edges []*Edge,
	llm LLMClient,
	log *slog.Logger,
) (string, string, error) {
	ctx = WithNodeID(ctx, node.ID)
	ctx = WithCallPurpose(ctx, "route")

	// Build the list of available routes
	var routeList strings.Builder
	for i, e := range edges {
		label := e.Label
		if label == "" {
			label = fmt.Sprintf("Route %d", i+1)
		}
		desc := e.Desc
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&routeList, "%d. %s: %s\n", i+1, label, desc)
	}

	selectRouteTool := Tool{
		Name:        "select_route",
		Description: "Select the next route to take in the workflow",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"route": map[string]any{
					"type":        "integer",
					"description": "The route number to select (1-based)",
				},
				"reason": map[string]any{
					"type":        "string",
					"description": "Brief explanation for this routing decision",
				},
			},
			"required": []string{"route"},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			return args, nil
		},
	}

	condition := node.Condition
	if condition == "" {
		condition = "(no explicit condition; choose the most appropriate route)"
	}

	userContent := fmt.Sprintf(
		"Step output:\n%s\n\nCurrent variables:\n%s\n\nCondition: %s\n\nAvailable routes:\n%s\nCall select_route with the route number.",
		out.Text,
		state.VarsSummary(),
		condition,
		routeList.String(),
	)

	resp, err := llm.Complete(ctx, CompletionRequest{
		Messages: []Message{
			{
				Role:    "system",
				Content: "You are routing an agentic workflow. Pick the next step based on the step output, current variables, and the exit condition. Call select_route with the route number.",
			},
			{Role: "user", Content: userContent},
		},
		Tools: []Tool{selectRouteTool},
	})
	if err != nil {
		return "", "", fmt.Errorf("routing LLM call: %w", err)
	}

	// Find the select_route tool call
	for _, tc := range resp.ToolCalls {
		if tc.Name == "select_route" {
			routeNum, reason := parseSelectRoute(tc.Args)
			if routeNum < 1 || routeNum > len(edges) {
				log.Warn("LLM returned invalid route number, falling back to route 1",
					"returned", routeNum, "max", len(edges), "node", node.Name)
				routeNum = 1
			}
			edge := edges[routeNum-1]
			r := reason
			if r == "" {
				r = fmt.Sprintf("selected route %d", routeNum)
			}
			return edge.Target, r, nil
		}
	}

	// Fallback: first edge (LLM did not call select_route)
	log.Warn("LLM did not call select_route, falling back to first edge", "node", node.Name)
	return edges[0].Target, "fallback to first edge", nil
}

// parseSelectRoute extracts route number and reason from select_route tool args.
// The route number is returned as-is (no clamping); callers are responsible for
// validating and logging when the value is out of range.
func parseSelectRoute(args map[string]any) (int, string) {
	route := 0
	reason := ""

	if r, ok := args["route"]; ok {
		switch v := r.(type) {
		case float64:
			route = int(v)
		case int:
			route = v
		case json.Number:
			n, _ := strconv.Atoi(v.String())
			route = n
		case string:
			n, _ := strconv.Atoi(v)
			route = n
		}
	}

	if rs, ok := args["reason"].(string); ok {
		reason = rs
	}

	return route, reason
}

// evaluateRouteNode evaluates Route node conditions against current state variables.
func evaluateRouteNode(node *Node, state *State) (string, string, error) {
	for _, rule := range node.Routes {
		if allConditionsMet(rule.Conditions, state.Variables) {
			reason := conditionSummary(rule.Conditions)
			return rule.TargetID, reason, nil
		}
	}
	if node.FallbackNodeID != "" {
		return node.FallbackNodeID, "fallback", nil
	}
	return "", "no matching route and no fallback", nil
}

// conditionSummary builds a human-readable description of the matched conditions.
func conditionSummary(conds []RouteCondition) string {
	if len(conds) == 0 {
		return "conditions matched"
	}
	c := conds[0]
	s := fmt.Sprintf("%s %s %q", c.Field, c.Operator, c.Value)
	if len(conds) > 1 {
		s += fmt.Sprintf(" (+%d)", len(conds)-1)
	}
	return s
}

// allConditionsMet returns true when every condition in the slice is satisfied.
func allConditionsMet(conds []RouteCondition, vars map[string]any) bool {
	for _, c := range conds {
		if !conditionMet(c, vars) {
			return false
		}
	}
	return true
}

// conditionMet reports whether a single RouteCondition is satisfied by vars.
// A missing field evaluates to false for positive operators ("is", "contains",
// comparisons) and to true for negative operators ("is not", "not contains"),
// treating absence as not-equal.
func conditionMet(c RouteCondition, vars map[string]any) bool {
	raw, exists := vars[c.Field]
	if !exists {
		// For "is not" or "not contains", missing field still counts as not-equal
		switch c.Operator {
		case "is not", "not contains":
			return true
		}
		return false
	}

	val := fmt.Sprintf("%v", raw)
	target := c.Value

	switch strings.ToLower(c.Operator) {
	case "is", "equals", "==":
		return strings.EqualFold(val, target)
	case "is not", "not equals", "!=":
		return !strings.EqualFold(val, target)
	case "contains":
		return strings.Contains(strings.ToLower(val), strings.ToLower(target))
	case "not contains":
		return !strings.Contains(strings.ToLower(val), strings.ToLower(target))
	case ">":
		a, b, ok := parseFloatPair(val, target)
		return ok && a > b
	case ">=":
		a, b, ok := parseFloatPair(val, target)
		return ok && a >= b
	case "<":
		a, b, ok := parseFloatPair(val, target)
		return ok && a < b
	case "<=":
		a, b, ok := parseFloatPair(val, target)
		return ok && a <= b
	}
	return false
}

// parseFloatPair parses two strings as float64. Returns false if either fails to parse.
func parseFloatPair(a, b string) (float64, float64, bool) {
	fa, err := strconv.ParseFloat(a, 64)
	if err != nil {
		return 0, 0, false
	}
	fb, err := strconv.ParseFloat(b, 64)
	if err != nil {
		return 0, 0, false
	}
	return fa, fb, true
}

// checkGlobalNode asks the LLM whether any global node should fire given the
// current state. Returns the global node to jump to, or nil if none should fire.
// Returns nil immediately without an LLM call if globals is empty.
func checkGlobalNode(ctx context.Context, globals []*Node, state *State, llm LLMClient) (*Node, error) {
	if len(globals) == 0 {
		return nil, nil
	}

	ctx = WithNodeID(ctx, GlobalCheckNodeID)
	ctx = WithCallPurpose(ctx, "check_global")

	var labelList strings.Builder
	fmt.Fprintf(&labelList, "0. None — continue normal execution\n")
	for i, n := range globals {
		fmt.Fprintf(&labelList, "%d. %s\n", i+1, n.GlobalLabel)
	}

	selectGlobalTool := Tool{
		Name:        "select_global_node",
		Description: "Select a global node to activate, or 0 for none.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"node": map[string]any{
					"type":        "integer",
					"description": "The global node number to activate (1-based), or 0 for none.",
				},
			},
			"required": []string{"node"},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			return args, nil
		},
	}

	userContent := fmt.Sprintf(
		"Task: %s\n\nCurrent variables:\n%s\n\nPrevious steps:\n%s\n\nGlobal triggers (activate if the situation clearly matches):\n%s\nCall select_global_node with the number of the matching trigger, or 0 if none apply.",
		state.Task,
		state.VarsSummary(),
		state.StepsSummary(),
		labelList.String(),
	)

	resp, err := llm.Complete(ctx, CompletionRequest{
		Messages: []Message{
			{
				Role:    "system",
				Content: "You are checking whether the current conversation state matches any global interrupt condition. Pick the matching global node number, or 0 if none apply. Be conservative — only activate a global node when the match is clear.",
			},
			{Role: "user", Content: userContent},
		},
		Tools: []Tool{selectGlobalTool},
	})
	if err != nil {
		return nil, fmt.Errorf("global node check LLM call: %w", err)
	}

	for _, tc := range resp.ToolCalls {
		if tc.Name == "select_global_node" {
			idx := parseIntArg(tc.Args, "node")
			if idx >= 1 && idx <= len(globals) {
				return globals[idx-1], nil
			}
			return nil, nil
		}
	}

	return nil, nil
}

// parseIntArg extracts an integer from a tool args map, handling float64/int/string/json.Number.
func parseIntArg(args map[string]any, key string) int {
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case json.Number:
		n, _ := strconv.Atoi(val.String())
		return n
	case string:
		n, _ := strconv.Atoi(val)
		return n
	}
	return 0
}
