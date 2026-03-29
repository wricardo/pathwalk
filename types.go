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
	// NodeTypeCheckpoint suspends or evaluates a gate condition.
	// See CheckpointMode for the four supported modes.
	NodeTypeCheckpoint NodeType = "checkpoint"
	// NodeTypeAgent spawns a single child agent run and suspends until it completes.
	NodeTypeAgent NodeType = "agent"
	// NodeTypeTeam spawns multiple child agent runs with a coordination strategy
	// (parallel, race, sequence) and suspends until they complete.
	NodeTypeTeam NodeType = "team"
)

// CheckpointMode determines how a Checkpoint node behaves.
type CheckpointMode string

const (
	// CheckpointModeHumanInput suspends execution and waits for freeform human input.
	CheckpointModeHumanInput CheckpointMode = "human_input"
	// CheckpointModeHumanApproval suspends execution and waits for human approve/reject.
	CheckpointModeHumanApproval CheckpointMode = "human_approval"
	// CheckpointModeLLMEval calls the LLM to evaluate pass/fail against criteria (synchronous).
	CheckpointModeLLMEval CheckpointMode = "llm_eval"
	// CheckpointModeAuto evaluates deterministic conditions against state variables (synchronous).
	CheckpointModeAuto CheckpointMode = "auto"
	// CheckpointModeWait suspends execution for a duration or until an external event.
	// The caller handles the actual sleeping/waiting and calls ResumeStep when ready.
	CheckpointModeWait CheckpointMode = "wait"
	// CheckpointModeAgent indicates suspension for a child agent run.
	CheckpointModeAgent CheckpointMode = "agent"
	// CheckpointModeTeam indicates suspension for parallel/race/sequence child agent runs.
	CheckpointModeTeam CheckpointMode = "team"
)

// WaitCondition describes what a checkpoint node is waiting for.
// Returned on StepResult when a checkpoint suspends execution.
type WaitCondition struct {
	Mode         CheckpointMode   `json:"mode"`
	NodeID       string           `json:"node_id"`
	NodeName     string           `json:"node_name"`
	Prompt       string           `json:"prompt"`
	Criteria     string           `json:"criteria,omitempty"`
	Conditions   []RouteCondition `json:"conditions,omitempty"`
	Options      []string         `json:"options,omitempty"`
	VariableName string           `json:"variable_name"`
	// Variables defines structured fields to collect from the human.
	// When present, the UI should render a form with typed inputs instead of
	// a single freeform text field. Each variable becomes a form field.
	// Supported types: "string", "integer", "boolean", "datetime".
	Variables []VariableDef `json:"variables,omitempty"`
	// WaitDuration is a Go duration string (e.g. "24h", "5m", "168h") for wait mode.
	// When set, the caller should sleep for this duration before resuming.
	// When empty in wait mode, the caller waits for an external event/signal.
	WaitDuration string `json:"wait_duration,omitempty"`
	// AgentTask describes a single child agent to spawn (mode=agent).
	AgentTask *AgentTask `json:"agent_task,omitempty"`
	// TeamTasks describes multiple child agents to spawn (mode=team).
	TeamTasks []AgentTask `json:"team_tasks,omitempty"`
	// TeamStrategy is the coordination strategy for team mode: "parallel", "race", "sequence".
	TeamStrategy string `json:"team_strategy,omitempty"`
}

// AgentTaskDef is a declarative agent definition in the pathway JSON (before template resolution).
type AgentTaskDef struct {
	Name      string `json:"name"`
	AgentID   string `json:"agentId"`
	Task      string `json:"task"`
	OutputVar string `json:"outputVar"`
}

// AgentTask describes a child agent run to spawn (with resolved templates).
type AgentTask struct {
	Name      string `json:"name"`
	AgentID   string `json:"agent_id"`
	Task      string `json:"task"`       // resolved task template
	OutputVar string `json:"output_var"` // variable name in parent state for this agent's output
}

// CheckpointResponse carries the external input that resumes a suspended checkpoint.
type CheckpointResponse struct {
	Value     string         `json:"value"`
	Vars      map[string]any `json:"vars,omitempty"`
	// ChildRuns captures execution traces from child agents (Agent/Team nodes).
	// The caller populates this so the parent's step log includes full child traces.
	ChildRuns []ChildRun `json:"child_runs,omitempty"`
}

// VariableDef describes a variable to extract from LLM output or collect from human input.
type VariableDef struct {
	Name        string `json:"name"`
	Type        string `json:"type"` // "string", "integer", "boolean", "datetime"
	Description string `json:"description"`
	Required    bool   `json:"required"`
	// JQ is an optional jq expression for deterministic extraction from
	// structured (JSON) responses. When set, the engine uses gojq instead
	// of calling the LLM, saving a round trip. The expression receives the
	// full response and should return a single value.
	// Example: ".data.createOrder.id"
	JQ string `json:"jq,omitempty"`
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

	// Node-level tools (parsed from JSON, scoped to this node only)
	Tools []NodeTool

	// Route node
	Routes         []RouteRule
	FallbackNodeID string

	// MaxVisits caps how many times this node may be visited in a single run.
	// 0 means use the pathway-level MaxVisitsPerNode default (or no limit).
	MaxVisits int

	// Checkpoint node
	CheckpointMode       CheckpointMode
	CheckpointPrompt     string
	CheckpointCriteria   string
	CheckpointVariable   string
	CheckpointConditions []RouteCondition
	CheckpointOptions    []string
	// WaitDuration is a Go duration string for wait mode (e.g. "24h", "5m").
	WaitDuration string

	// Agent node
	AgentID        string // ID of the child agent to spawn (references an Agent record)
	AgentTask      string // task template with {{variable}} placeholders
	AgentOutputVar string // variable name for the child agent's output

	// Team node
	TeamStrategy string           // "parallel", "race", "sequence"
	TeamAgents   []AgentTaskDef   // child agents to spawn
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

// NodeTool is a declarative tool definition attached to a specific node in the
// pathway JSON. Unlike Tool, it carries no Go function — the engine constructs
// an executable Tool from the config at runtime (e.g. performing a webhook call).
type NodeTool struct {
	Name        string
	Description string
	Type        string // "webhook" or "custom_tool"
	Behavior    string // "feed_context" — response fed back into conversation

	// Webhook config
	URL     string
	Method  string
	Headers map[string]string
	Body    string // raw body template with {{variable}} placeholders

	// Timeout in seconds for the HTTP request. 0 means use the default (30s).
	Timeout int
	// Retries is the number of retry attempts on failure. 0 means no retries.
	Retries int

	// Speech is optional text the agent speaks while the tool executes
	// (relevant for voice agents; ignored by the default engine).
	Speech string

	// Variables to extract from the tool's response
	ExtractVars []VariableDef

	// ResponsePathways defines conditional routing based on the tool's response.
	// When Behavior is "feed_context", the LLM sees the result and continues.
	// When pathways have conditions, a matching pathway can redirect the
	// conversation to a different node, overriding normal edge-based routing.
	ResponsePathways []ToolResponsePathway
}

// ToolResponsePathway describes how to handle a tool's response.
// It can act as a conditional offramp: if the response matches the condition,
// route to the specified node instead of continuing normal flow.
type ToolResponsePathway struct {
	// Type is the trigger type: "default" (always matches)
	// or "BlandStatusCode" (matches on HTTP status code).
	Type string `json:"type"`

	// Condition operator: "==", "!=", ">", "<", ">=", "<=", "contains", "!contains", "is".
	// Empty means no condition (always matches).
	Operator string `json:"operator,omitempty"`

	// Value to compare against (e.g. "200", "error").
	Value string `json:"value,omitempty"`

	// NodeID is the target node to route to when the condition matches.
	NodeID string `json:"nodeId"`

	// Name is a human-readable label for this pathway.
	Name string `json:"name,omitempty"`
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
	// ResumeValue records the value submitted to resume a checkpoint/agent/team.
	// Empty for non-resumable nodes.
	ResumeValue string
	// ChildRuns captures execution traces from child agent runs (Agent/Team nodes).
	ChildRuns []ChildRun
}

// ChildRun captures the execution trace of a child agent run.
type ChildRun struct {
	Name    string `json:"name"`     // child agent name
	AgentID string `json:"agent_id"` // child agent ID
	Output  string `json:"output"`   // final output
	Steps   []Step `json:"steps"`    // full execution trace
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
	// Logs contains all log records emitted during this run.
	Logs []LogEntry
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

