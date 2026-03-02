// Package pathwalk executes conversational pathway JSON files as
// agentic pipelines. A pathway is a directed graph of nodes connected by
// edges. The [Engine] walks the graph step-by-step: [NodeTypeLLM] nodes
// invoke an LLM, [NodeTypeRoute] nodes evaluate conditions, [NodeTypeWebhook]
// nodes make HTTP calls, and [NodeTypeTerminal] nodes terminate the run.
//
// Quick start:
//
//	pathway, err := pathwalk.ParsePathway("my_pathway.json")
//	llm := pathwalk.NewOpenAIClient(apiKey, "", "gpt-4o")
//	engine := pathwalk.NewEngine(pathway, llm)
//	result, err := engine.Run(ctx, "your task description")
package pathwalk

import "context"

// NodeType identifies the kind of node in a pathway.
type NodeType string

const (
	// NodeTypeLLM invokes the LLM to execute the node's prompt and optionally
	// extracts variables from the response.
	NodeTypeLLM NodeType = "llm"
	// NodeTypeTerminal terminates the pathway run and returns the node's TerminalText.
	NodeTypeTerminal NodeType = "terminal"
	// NodeTypeWebhook performs an HTTP request and optionally extracts variables
	// from the JSON response.
	NodeTypeWebhook NodeType = "webhook"
	// NodeTypeRoute evaluates conditions against the current state variables to
	// pick the next node without calling the LLM.
	NodeTypeRoute NodeType = "route"
)

// VariableDef describes a variable to extract from LLM output.
type VariableDef struct {
	Name        string
	Type        string // "string", "integer", "boolean"
	Description string
	Required    bool
}

// Edge represents a directed connection between two nodes.
type Edge struct {
	ID     string
	Source string
	Target string
	Label  string
	Desc   string
}

// RouteCondition is a single field/operator/value check.
type RouteCondition struct {
	Field    string
	Operator string // "is", "is not", "contains", "not contains", ">", "<", ">=", "<="
	Value    string
}

// RouteRule maps a set of conditions (AND-logic) to a target node.
type RouteRule struct {
	Conditions []RouteCondition
	TargetID   string
}

// Node is a parsed node from the pathway.
type Node struct {
	ID          string
	Type        NodeType
	Name        string
	IsStart     bool
	IsGlobal    bool
	GlobalLabel string

	// LLM node
	Prompt      string
	Text        string
	Condition   string
	ExtractVars []VariableDef
	Temperature float64

	// Terminal node
	TerminalText string

	// Webhook node
	WebhookURL     string
	WebhookMethod  string
	WebhookHeaders map[string]string
	WebhookBody    any

	// Route node
	Routes         []RouteRule
	FallbackNodeID string

	// MaxVisits caps how many times this node may be visited in a single run.
	// 0 means use the pathway-level MaxVisitsPerNode default (or no limit).
	MaxVisits int
}

// Message is a single turn in an LLM conversation.
type Message struct {
	Role    string // "system", "user", "assistant"
	Content string
}

// Tool is a callable function exposed to the LLM.
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON schema
	Fn          func(ctx context.Context, args map[string]any) (any, error)
}

// ToolCall records a single tool invocation and its outcome.
type ToolCall struct {
	ID     string
	Name   string
	Args   map[string]any
	Result any
	Error  string
}

// Step records what happened at a single node.
type Step struct {
	NodeID   string
	NodeName string
	Output   string
	Vars     map[string]any
	// ToolCalls holds every tool invocation made by the LLM during this node's
	// execution. Empty for Route and Terminal nodes.
	ToolCalls []ToolCall
	// RouteReason is the human-readable explanation for why the engine
	// chose the next node (e.g. "single edge", "selected route 2").
	RouteReason string
	NextNode    string
}

// RunResult is the final result of running a pathway.
type RunResult struct {
	// Output is the last meaningful content produced by the run — the output
	// of the last LLM or webhook step that executed before the terminal node.
	// Terminal nodes do not contribute to Output.
	Output    string
	Variables map[string]any
	Steps     []Step
	// Reason explains why the run ended. Values: "terminal", "max_steps",
	// "error", "dead_end", "missing_node", "max_node_visits".
	Reason string
	// FailedNode is the name of the node that caused the run to stop when
	// Reason is "error" or "max_node_visits". Empty otherwise.
	FailedNode string
}

// contextKey is used for type-safe context values.
type contextKey string

const (
	// NodeIDContextKey is set in the context before each LLM call so mocks
	// can inspect which node triggered the call.
	NodeIDContextKey contextKey = "nodeID"

	// CallPurposeContextKey distinguishes the purpose of an LLM call.
	// Values: "execute", "extract_vars", "route", "check_global"
	CallPurposeContextKey contextKey = "callPurpose"

	verboseCtxKey contextKey = "verbose"
)

// GlobalCheckNodeID is the node ID placed in context during the global-node-check
// LLM call each step. Use it with MockLLMClient.OnNodePurpose in tests:
//
//	mock.OnNodePurpose(pathwalk.GlobalCheckNodeID, "check_global", ...)
const GlobalCheckNodeID = "$global_check"

// WithNodeID returns a context carrying the current node ID.
func WithNodeID(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, NodeIDContextKey, nodeID)
}

// NodeIDFromContext retrieves the node ID from the context.
func NodeIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(NodeIDContextKey).(string)
	return v
}

// WithCallPurpose returns a context carrying the call purpose.
func WithCallPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, CallPurposeContextKey, purpose)
}

// CallPurposeFromContext retrieves the call purpose from the context.
func CallPurposeFromContext(ctx context.Context) string {
	v, _ := ctx.Value(CallPurposeContextKey).(string)
	return v
}

func withVerboseCtx(ctx context.Context) context.Context {
	return context.WithValue(ctx, verboseCtxKey, true)
}

func isVerbose(ctx context.Context) bool {
	v, _ := ctx.Value(verboseCtxKey).(bool)
	return v
}
