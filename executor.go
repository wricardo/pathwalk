package pathwalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// webhookClient is used for all webhook HTTP requests.
// A 30-second timeout prevents slow or hung endpoints from blocking indefinitely.
var webhookClient = &http.Client{Timeout: 30 * time.Second}

// nodeOutput is the internal result of executing a single node.
type nodeOutput struct {
	Text      string
	Vars      map[string]any
	ToolCalls []ToolCall
}

// executeNode dispatches to the appropriate executor based on node type.
func executeNode(ctx context.Context, node *Node, state *State, llm LLMClient, tools []Tool, log *slog.Logger) (*nodeOutput, error) {
	ctx = WithNodeID(ctx, node.ID)
	switch node.Type {
	case NodeTypeLLM:
		return executeLLM(ctx, node, state, llm, tools, log)
	case NodeTypeTerminal:
		return &nodeOutput{Text: node.TerminalText}, nil
	case NodeTypeWebhook:
		return executeWebhook(ctx, node, state)
	case NodeTypeRoute:
		return &nodeOutput{}, nil // routing is handled by the engine
	case NodeTypeCheckpoint:
		return executeCheckpoint(ctx, node, state, llm, log)
	default:
		return nil, fmt.Errorf("unsupported node type: %s", node.Type)
	}
}

// executeLLM runs an LLM conversation at a Default node, with tools, and
// optionally extracts variables from the output.
func executeLLM(ctx context.Context, node *Node, state *State, llm LLMClient, tools []Tool, log *slog.Logger) (*nodeOutput, error) {
	ctx = WithCallPurpose(ctx, "execute")

	systemPrompt := buildSystemPrompt(node, state)
	userMsg := buildUserMessage(state)

	// Merge global tools with node-level tools.
	allTools := tools
	if len(node.Tools) > 0 {
		allTools = make([]Tool, len(tools))
		copy(allTools, tools)
		for _, nt := range node.Tools {
			allTools = append(allTools, nodeToolToTool(nt, state, log))
		}
	}

	req := CompletionRequest{
		Model:       node.Model,
		Provider:    node.LLMProvider,
		Messages:    []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
		Tools:       allTools,
		Temperature: node.Temperature,
	}

	start := time.Now()
	resp, err := llm.Complete(ctx, req)
	if err != nil {
		// Return partial tool calls alongside the error so callers can record
		// what was executed before the failure (e.g. exceeded max tool rounds).
		var partial *nodeOutput
		if resp != nil && len(resp.ToolCalls) > 0 {
			partial = &nodeOutput{ToolCalls: resp.ToolCalls}
		}
		return partial, fmt.Errorf("node %q LLM call: %w", node.Name, err)
	}

	// If the model didn't use function calling but emitted a <|channel|> directive,
	// parse and execute the tool manually.
	if len(resp.ToolCalls) == 0 {
		if toolName, args, ok := parseChannelDirective(resp.Content); ok {
			for _, t := range tools {
				if t.Name == toolName {
					argsJSON, _ := json.Marshal(args)
					log.Debug("channel directive detected", "tool", toolName, "args", string(argsJSON))
					result, toolErr := t.Fn(ctx, args)
					if toolErr != nil {
						log.Warn("channel tool failed", "tool", toolName, "error", toolErr)
					} else {
						resultJSON, _ := json.Marshal(result)
						resp.Content = string(resultJSON)
						log.Debug("channel tool executed", "tool", toolName, "result", resp.Content)
					}
					break
				}
			}
		}
	}

	duration := time.Since(start).Round(time.Millisecond)
	log.Debug("LLM call completed", "node", node.Name, "duration", duration.String(), "temperature", node.Temperature)
	for _, tc := range resp.ToolCalls {
		argsJSON, _ := json.Marshal(tc.Args)
		resultJSON, _ := json.Marshal(tc.Result)
		log.Debug("tool call result", "tool", tc.Name, "args", string(argsJSON), "result", string(resultJSON))
	}

	out := &nodeOutput{Text: resp.Content, ToolCalls: resp.ToolCalls}

	// Extract variables if specified
	if len(node.ExtractVars) > 0 {
		vars, err := extractVars(ctx, node, resp.Content, llm, log)
		if err != nil {
			// Non-fatal: log and continue without extracted vars
			log.Warn("variable extraction failed", "node", node.Name, "error", err)
		} else {
			out.Vars = vars
		}
	}

	return out, nil
}

// buildSystemPrompt constructs the system prompt for a Default node.
func buildSystemPrompt(node *Node, state *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are an AI agent executing a workflow step.\n")
	fmt.Fprintf(&b, "Step: %s\n\n", node.Name)

	prompt := node.Prompt
	if prompt == "" {
		prompt = node.Text
	}
	if prompt != "" {
		fmt.Fprintf(&b, "Instructions:\n%s\n\n", prompt)
	}

	fmt.Fprintf(&b, "Overall task: %s\n", state.Task)

	if node.Condition != "" {
		fmt.Fprintf(&b, "\nExit condition (when this step is done): %s\n", node.Condition)
	}

	return b.String()
}

// buildUserMessage constructs the user message carrying current state context.
func buildUserMessage(state *State) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n\n", state.Task)

	if len(state.Variables) > 0 {
		fmt.Fprintf(&b, "Current variables:\n%s\n", state.VarsSummary())
	}

	if len(state.Steps) > 0 {
		fmt.Fprintf(&b, "Previous steps:\n%s\n", state.StepsSummary())
	}

	fmt.Fprintf(&b, "\nPlease proceed with this step.")
	return b.String()
}

// extractVars pulls structured variables out of the node output.
// Variables with a JQ expression are extracted deterministically using gojq.
// Remaining variables are extracted by calling the LLM.
func extractVars(ctx context.Context, node *Node, text string, llm LLMClient, log *slog.Logger) (map[string]any, error) {
	ctx = WithCallPurpose(ctx, "extract_vars")

	result := map[string]any{}

	// First pass: extract variables that have JQ expressions (deterministic, no LLM call).
	var llmVars []VariableDef
	for _, v := range node.ExtractVars {
		if v.JQ != "" {
			extracted, err := RunJQ(v.JQ, text)
			if err != nil {
				log.Warn("jq extraction failed, will use LLM", "var", v.Name, "jq", v.JQ, "error", err)
				llmVars = append(llmVars, v)
				continue
			}
			if extracted == nil && v.Required {
				// JQ produced no value for a required variable — fall back to LLM.
				log.Warn("jq returned nil for required variable, will use LLM", "var", v.Name, "jq", v.JQ)
				llmVars = append(llmVars, v)
				continue
			}
			log.Debug("variable extracted via jq", "var", v.Name, "value", extracted)
			result[v.Name] = extracted
		} else {
			llmVars = append(llmVars, v)
		}
	}

	// Second pass: extract remaining variables via LLM.
	if len(llmVars) > 0 {
		llmResult, err := extractVarsLLM(ctx, llmVars, text, node.Model, node.LLMProvider, llm, log)
		if err != nil {
			return result, err
		}
		for k, v := range llmResult {
			result[k] = v
		}
	}

	if len(result) > 0 {
		varsJSON, _ := json.Marshal(result)
		log.Debug("variables extracted", "vars", string(varsJSON))
	}

	return result, nil
}

// extractVarsLLM calls the LLM to extract variables that don't have JQ expressions.
func extractVarsLLM(ctx context.Context, vars []VariableDef, text, model, provider string, llm LLMClient, log *slog.Logger) (map[string]any, error) {
	// Build the variable descriptions
	var varDesc strings.Builder
	for _, v := range vars {
		req := ""
		if v.Required {
			req = " (required)"
		}
		fmt.Fprintf(&varDesc, "- %s (%s)%s: %s\n", v.Name, v.Type, req, v.Description)
	}

	// Build the JSON schema for set_variables
	properties := map[string]any{}
	var requiredFields []string
	for _, v := range vars {
		jsonType := "string"
		switch v.Type {
		case "integer":
			jsonType = "integer"
		case "boolean":
			jsonType = "boolean"
		}
		properties[v.Name] = map[string]any{
			"type":        jsonType,
			"description": v.Description,
		}
		if v.Required {
			requiredFields = append(requiredFields, v.Name)
		}
	}

	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(requiredFields) > 0 {
		schema["required"] = requiredFields
	}

	setVarsTool := Tool{
		Name:        "set_variables",
		Description: "Set the extracted variables",
		Parameters:  schema,
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			return args, nil
		},
	}

	req := CompletionRequest{
		Model:    model,
		Provider: provider,
		Messages: []Message{
			{
				Role:    "system",
				Content: "Extract variables from the provided text. Call set_variables with the extracted values. Use null for variables that cannot be determined.",
			},
			{
				Role: "user",
				Content: fmt.Sprintf(
					"Text:\n%s\n\nVariables to extract:\n%s\nCall set_variables with the extracted values.",
					text, varDesc.String(),
				),
			},
		},
		Tools: []Tool{setVarsTool},
	}

	resp, err := llm.Complete(ctx, req)
	if err != nil {
		return nil, err
	}

	// Collect variables from tool calls made
	result := map[string]any{}
	for _, tc := range resp.ToolCalls {
		if tc.Name == "set_variables" {
			for k, v := range tc.Args {
				result[k] = v
			}
		}
	}

	return result, nil
}

// executeWebhook performs the HTTP call described by a Webhook node.
func executeWebhook(ctx context.Context, node *Node, state *State) (*nodeOutput, error) {
	method := node.WebhookMethod
	if method == "" {
		method = "POST"
	}

	// Resolve variables in the body
	bodyData := resolveBody(node.WebhookBody, state.Variables)
	bodyBytes, err := json.Marshal(bodyData)
	if err != nil {
		return nil, fmt.Errorf("webhook body marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, node.WebhookURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range node.WebhookHeaders {
		req.Header.Set(k, v)
	}

	resp, err := webhookClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhook HTTP: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("webhook read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("webhook returned status %d: %s", resp.StatusCode, respBytes)
	}

	var respData any
	if err := json.Unmarshal(respBytes, &respData); err != nil {
		respData = string(respBytes)
	}

	// Extract variables from webhook response if extractVars is set.
	// Variables with JQ expressions use gojq; others do simple key lookup.
	vars := map[string]any{}
	if len(node.ExtractVars) > 0 {
		for _, vd := range node.ExtractVars {
			if vd.JQ != "" {
				extracted, err := RunJQ(vd.JQ, respData)
				if err == nil {
					vars[vd.Name] = extracted
					continue
				}
				// Fall through to key-based lookup on jq error
			}
			if m, ok := respData.(map[string]any); ok {
				if v, exists := m[vd.Name]; exists {
					vars[vd.Name] = v
				}
			}
		}
	}

	resultJSON, _ := json.Marshal(respData)
	return &nodeOutput{Text: string(resultJSON), Vars: vars}, nil
}

// resolveBody replaces {{variable}} placeholders in the body with state values.
func resolveBody(body any, vars map[string]any) any {
	switch v := body.(type) {
	case string:
		return resolveTemplate(v, vars)
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = resolveBody(val, vars)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, val := range v {
			out[i] = resolveBody(val, vars)
		}
		return out
	default:
		return v
	}
}

// parseChannelDirective parses the model's <|channel|> ... <|message|> format.
// Example: <|channel|>commentary to=graphql <|constrain|>json<|message|>{"query":"..."}
// Returns the tool name, parsed JSON args, and whether the format was detected.
func parseChannelDirective(text string) (toolName string, args map[string]any, ok bool) {
	const channelTag = "<|channel|>"
	const messageTag = "<|message|>"

	ci := strings.Index(text, channelTag)
	mi := strings.Index(text, messageTag)
	if ci == -1 || mi == -1 || mi <= ci {
		return "", nil, false
	}

	// Extract tool name from "to=<name>" in the channel section
	channelSection := text[ci+len(channelTag) : mi]
	toIdx := strings.Index(channelSection, "to=")
	if toIdx == -1 {
		return "", nil, false
	}
	namePart := strings.TrimSpace(channelSection[toIdx+3:])
	// Name ends at first space or <|
	for i, ch := range namePart {
		if ch == ' ' || (i+2 <= len(namePart) && namePart[i:i+2] == "<|") {
			namePart = namePart[:i]
			break
		}
	}
	toolName = strings.TrimSpace(namePart)
	if toolName == "" {
		return "", nil, false
	}

	// Extract JSON payload after <|message|>
	payload := strings.TrimSpace(text[mi+len(messageTag):])
	if err := json.Unmarshal([]byte(payload), &args); err != nil {
		return "", nil, false
	}

	return toolName, args, true
}

// resolveTemplate replaces {{varName}} in s with the corresponding state variable.
func resolveTemplate(s string, vars map[string]any) string {
	for k, v := range vars {
		placeholder := "{{" + k + "}}"
		s = strings.ReplaceAll(s, placeholder, fmt.Sprintf("%v", v))
	}
	return s
}

// nodeToolToTool converts a declarative NodeTool (from pathway JSON) into an
// executable Tool. Currently only "webhook" type tools are supported.
func nodeToolToTool(nt NodeTool, state *State, log *slog.Logger) Tool {
	return Tool{
		Name:        nt.Name,
		Description: nt.Description,
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			if nt.Type != "webhook" {
				return nil, fmt.Errorf("unsupported node tool type: %s", nt.Type)
			}

			result, statusCode, err := executeNodeToolWebhook(ctx, nt, state, args, log)
			if err != nil {
				return nil, err
			}

			// Extract variables from the response if configured.
			// Variables with JQ expressions use gojq; others do simple key lookup.
			if len(nt.ExtractVars) > 0 {
				extracted := map[string]any{}
				for _, vd := range nt.ExtractVars {
					if vd.JQ != "" {
						val, err := RunJQ(vd.JQ, result)
						if err == nil {
							extracted[vd.Name] = val
							continue
						}
						// Fall through to key-based lookup on jq error
					}
					if m, ok := result.(map[string]any); ok {
						if v, exists := m[vd.Name]; exists {
							extracted[vd.Name] = v
						}
					}
				}
				if len(extracted) > 0 {
					state.SetVars(extracted)
				}
			}

			// Evaluate response pathways for conditional routing.
			if routeNodeID := evaluateResponsePathways(nt.ResponsePathways, statusCode, result); routeNodeID != "" {
				state.SetVars(map[string]any{"$tool_route": routeNodeID})
				log.Debug("response pathway matched", "tool", nt.Name, "routeTo", routeNodeID)
			}

			return result, nil
		},
	}
}

// executeNodeToolWebhook performs the HTTP call for a webhook NodeTool with
// timeout and retry support. Returns the parsed response body and HTTP status code.
func executeNodeToolWebhook(ctx context.Context, nt NodeTool, state *State, args map[string]any, log *slog.Logger) (any, int, error) {
	// Merge state vars with tool-call args for template resolution.
	mergedVars := make(map[string]any, len(state.Variables)+len(args))
	for k, v := range state.Variables {
		mergedVars[k] = v
	}
	for k, v := range args {
		mergedVars[k] = v
	}

	body := resolveTemplate(nt.Body, mergedVars)

	method := nt.Method
	if method == "" {
		method = "POST"
	}

	// Build an HTTP client with per-tool timeout if configured.
	client := webhookClient
	if nt.Timeout > 0 {
		client = &http.Client{Timeout: time.Duration(nt.Timeout) * time.Second}
	}

	maxAttempts := 1 + nt.Retries
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			log.Debug("retrying node tool webhook", "tool", nt.Name, "attempt", attempt+1)
		}

		req, err := http.NewRequestWithContext(ctx, method, nt.URL, strings.NewReader(body))
		if err != nil {
			return nil, 0, fmt.Errorf("node tool %q request: %w", nt.Name, err)
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range nt.Headers {
			req.Header.Set(k, v)
		}

		log.Debug("executing node tool webhook", "tool", nt.Name, "url", nt.URL)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("node tool %q HTTP: %w", nt.Name, err)
			continue
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("node tool %q read body: %w", nt.Name, err)
			continue
		}

		var respData any
		if err := json.Unmarshal(respBytes, &respData); err != nil {
			respData = string(respBytes)
		}

		if resp.StatusCode >= 400 {
			lastErr = fmt.Errorf("node tool %q returned status %d: %s", nt.Name, resp.StatusCode, respBytes)
			// Still return the response data and status for pathway evaluation,
			// but only on the last attempt.
			if attempt == maxAttempts-1 {
				return respData, resp.StatusCode, nil
			}
			continue
		}

		return respData, resp.StatusCode, nil
	}

	return nil, 0, lastErr
}

// evaluateResponsePathways checks response pathways for a matching condition.
// Returns the target node ID if a pathway matches, or "" if none match.
func evaluateResponsePathways(pathways []ToolResponsePathway, statusCode int, _ any) string {
	for _, rp := range pathways {
		if rp.NodeID == "" {
			continue // no routing target
		}

		switch rp.Type {
		case "default", "":
			// Default pathway always matches (used as a fallback).
			return rp.NodeID

		case "BlandStatusCode":
			if rp.Operator == "" {
				continue
			}
			actual := fmt.Sprintf("%d", statusCode)
			if matchCondition(actual, rp.Operator, rp.Value) {
				return rp.NodeID
			}
		}
	}
	return ""
}

// matchCondition evaluates a single operator/value condition against an actual value.
// For ordering operators (>, <, >=, <=) both values are parsed as integers; if
// either fails to parse the comparison falls back to lexicographic order.
func matchCondition(actual, operator, expected string) bool {
	switch operator {
	case "==", "is":
		return actual == expected
	case "!=":
		return actual != expected
	case "contains":
		return strings.Contains(actual, expected)
	case "!contains":
		return !strings.Contains(actual, expected)
	case ">":
		a, b, ok := parseIntPair(actual, expected)
		if ok {
			return a > b
		}
		return actual > expected
	case "<":
		a, b, ok := parseIntPair(actual, expected)
		if ok {
			return a < b
		}
		return actual < expected
	case ">=":
		a, b, ok := parseIntPair(actual, expected)
		if ok {
			return a >= b
		}
		return actual >= expected
	case "<=":
		a, b, ok := parseIntPair(actual, expected)
		if ok {
			return a <= b
		}
		return actual <= expected
	default:
		return false
	}
}

// executeCheckpoint handles synchronous checkpoint modes (auto and llm_eval).
// Human modes are handled by the engine before reaching executeNode.
func executeCheckpoint(ctx context.Context, node *Node, state *State, llm LLMClient, log *slog.Logger) (*nodeOutput, error) {
	switch node.CheckpointMode {
	case CheckpointModeAuto:
		result := "fail"
		if allConditionsMet(node.CheckpointConditions, state.Variables) {
			result = "pass"
		}
		vars := map[string]any{}
		if node.CheckpointVariable != "" {
			vars[node.CheckpointVariable] = result
		}
		log.Debug("auto checkpoint evaluated", "node", node.Name, "result", result)
		return &nodeOutput{Text: result, Vars: vars}, nil

	case CheckpointModeLLMEval:
		ctx = WithCallPurpose(ctx, "checkpoint_eval")

		// Build the evaluation prompt from criteria and current state.
		evalPrompt := fmt.Sprintf(
			"Evaluate whether the current state meets the following criteria.\n\nCriteria: %s\n\nCurrent variables:\n%s\n\nPrevious steps:\n%s\n\nCall checkpoint_eval with result \"pass\" or \"fail\" and a brief reason.",
			node.CheckpointCriteria,
			state.VarsSummary(),
			state.StepsSummary(),
		)

		checkpointEvalTool := Tool{
			Name:        "checkpoint_eval",
			Description: "Report whether the checkpoint criteria are met.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"result": map[string]any{
						"type":        "string",
						"description": "Either \"pass\" or \"fail\"",
						"enum":        []string{"pass", "fail"},
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "Brief explanation",
					},
				},
				"required": []string{"result"},
			},
			Fn: func(ctx context.Context, args map[string]any) (any, error) {
				return args, nil
			},
		}

		resp, err := llm.Complete(ctx, CompletionRequest{
			Model:    node.Model,
			Provider: node.LLMProvider,
			Messages: []Message{
				{Role: "system", Content: "You are evaluating a quality gate in an agentic workflow. Assess the current state against the criteria and call checkpoint_eval."},
				{Role: "user", Content: evalPrompt},
			},
			Tools: []Tool{checkpointEvalTool},
		})
		if err != nil {
			return nil, fmt.Errorf("checkpoint llm_eval: %w", err)
		}

		result := "fail" // default to fail if LLM doesn't call the tool
		for _, tc := range resp.ToolCalls {
			if tc.Name == "checkpoint_eval" {
				if r, ok := tc.Args["result"].(string); ok {
					result = r
				}
				break
			}
		}

		vars := map[string]any{}
		if node.CheckpointVariable != "" {
			vars[node.CheckpointVariable] = result
		}
		log.Debug("llm_eval checkpoint evaluated", "node", node.Name, "result", result)
		return &nodeOutput{Text: result, Vars: vars}, nil

	default:
		return nil, fmt.Errorf("unsupported checkpoint mode: %s", node.CheckpointMode)
	}
}

// parseIntPair parses two strings as int64. Returns false if either fails.
func parseIntPair(a, b string) (int64, int64, bool) {
	ia, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	ib, err := strconv.ParseInt(b, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ia, ib, true
}
