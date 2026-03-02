package pathwalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// nodeOutput is the internal result of executing a single node.
type nodeOutput struct {
	Text      string
	Vars      map[string]any
	ToolCalls []ToolCall
}

// executeNode dispatches to the appropriate executor based on node type.
func executeNode(ctx context.Context, node *Node, state *State, llm LLMClient, tools []Tool) (*nodeOutput, error) {
	ctx = WithNodeID(ctx, node.ID)
	switch node.Type {
	case NodeTypeLLM:
		return executeLLM(ctx, node, state, llm, tools)
	case NodeTypeTerminal:
		return &nodeOutput{Text: node.TerminalText}, nil
	case NodeTypeWebhook:
		return executeWebhook(ctx, node, state)
	case NodeTypeRoute:
		return &nodeOutput{}, nil // routing is handled by the engine
	default:
		return nil, fmt.Errorf("unsupported node type: %s", node.Type)
	}
}

// executeLLM runs an LLM conversation at a Default node, with tools, and
// optionally extracts variables from the output.
func executeLLM(ctx context.Context, node *Node, state *State, llm LLMClient, tools []Tool) (*nodeOutput, error) {
	ctx = WithCallPurpose(ctx, "execute")

	systemPrompt := buildSystemPrompt(node, state)
	userMsg := buildUserMessage(state)

	req := CompletionRequest{
		Model:    "",
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
		Tools:       tools,
		Temperature: node.Temperature,
	}

	start := time.Now()
	resp, err := llm.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("node %q LLM call: %w", node.Name, err)
	}

	// If the model didn't use function calling but emitted a <|channel|> directive,
	// parse and execute the tool manually.
	if len(resp.ToolCalls) == 0 {
		if toolName, args, ok := parseChannelDirective(resp.Content); ok {
			for _, t := range tools {
				if t.Name == toolName {
					if isVerbose(ctx) {
						argsJSON, _ := json.Marshal(args)
						log.Printf("[channel] detected %s(%s)", toolName, argsJSON)
					}
					result, toolErr := t.Fn(ctx, args)
					if toolErr != nil {
						log.Printf("[warn] channel tool %q failed: %v", toolName, toolErr)
					} else {
						resultJSON, _ := json.Marshal(result)
						resp.Content = string(resultJSON)
						if isVerbose(ctx) {
							log.Printf("[tool] %s → %s", toolName, resp.Content)
						}
					}
					break
				}
			}
		}
	}

	if isVerbose(ctx) {
		extra := ""
		if node.Temperature > 0 {
			extra = fmt.Sprintf(" temperature=%.1f", node.Temperature)
		}
		log.Printf("[llm] node=%q duration=%s%s", node.Name, time.Since(start).Round(time.Millisecond), extra)
		for _, tc := range resp.ToolCalls {
			argsJSON, _ := json.Marshal(tc.Args)
			resultJSON, _ := json.Marshal(tc.Result)
			log.Printf("[tool] %s(%s) → %s", tc.Name, argsJSON, resultJSON)
		}
	}

	out := &nodeOutput{Text: resp.Content, ToolCalls: resp.ToolCalls}

	// Extract variables if specified
	if len(node.ExtractVars) > 0 {
		vars, err := extractVars(ctx, node, resp.Content, llm)
		if err != nil {
			// Non-fatal: log and continue without extracted vars
			log.Printf("[warn] variable extraction failed at node %q: %v", node.Name, err)
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

// extractVars calls the LLM to pull structured variables out of the node output.
func extractVars(ctx context.Context, node *Node, text string, llm LLMClient) (map[string]any, error) {
	ctx = WithCallPurpose(ctx, "extract_vars")

	// Build the variable descriptions
	var varDesc strings.Builder
	for _, v := range node.ExtractVars {
		req := ""
		if v.Required {
			req = " (required)"
		}
		fmt.Fprintf(&varDesc, "- %s (%s)%s: %s\n", v.Name, v.Type, req, v.Description)
	}

	// Build the JSON schema for set_variables
	properties := map[string]any{}
	var requiredFields []string
	for _, v := range node.ExtractVars {
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

	if isVerbose(ctx) && len(result) > 0 {
		varsJSON, _ := json.Marshal(result)
		log.Printf("[vars] extracted: %s", varsJSON)
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

	resp, err := http.DefaultClient.Do(req)
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

	// Extract variables from webhook response if extractVars is set
	vars := map[string]any{}
	if len(node.ExtractVars) > 0 {
		if m, ok := respData.(map[string]any); ok {
			for _, vd := range node.ExtractVars {
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
