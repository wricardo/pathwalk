package pathwalk

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

const agentSystemPromptTemplate = `You are a GraphQL API agent. You help users accomplish tasks by executing GraphQL queries and mutations against the configured endpoint.

%s

Instructions:
- Use graphql_queries, graphql_mutations, and graphql_type tools to explore the schema when needed before writing operations.
- Always mention key IDs and important values in your text responses so they are available in follow-up tasks.
- When you have completed all requested operations, call the done tool with a brief summary.
- If an operation fails, explain why and ask for clarification rather than retrying blindly.`

// AgentResponse holds the result of a single Agent.Ask call.
type AgentResponse struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
	Summary   string
}

// Agent is a free-form, multi-turn LLM agent. Unlike Engine (which follows a
// pathway graph), Agent maintains a conversation history across calls and lets
// the LLM decide which tools to use to accomplish each task.
type Agent struct {
	llm     LLMClient
	tools   []Tool
	history []Message
	model   string
	done    bool
	summary string
}

// NewAgent creates an Agent with the given LLM client, tool set, and system
// prompt. A built-in "done" tool is appended automatically; callers should not
// include one in tools.
func NewAgent(llm LLMClient, tools []Tool, systemPrompt string) *Agent {
	a := &Agent{
		llm:     llm,
		history: []Message{{Role: "system", Content: systemPrompt}},
	}
	a.tools = append(tools, a.doneTool())
	return a
}

// NewAgentWithModel is like NewAgent but overrides the model used by the LLM client.
func NewAgentWithModel(llm LLMClient, tools []Tool, systemPrompt, model string) *Agent {
	a := NewAgent(llm, tools, systemPrompt)
	a.model = model
	return a
}

func (a *Agent) doneTool() Tool {
	return Tool{
		Name:        "done",
		Description: "Signal that the task is complete. Call when all requested operations are finished.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "Brief summary of what was accomplished",
				},
			},
			"required": []string{"summary"},
		},
		Fn: func(_ context.Context, args map[string]any) (any, error) {
			a.done = true
			a.summary, _ = args["summary"].(string)
			return "Task marked as done.", nil
		},
	}
}

// Ask sends a user message to the LLM, executes any tool calls, appends the
// exchange to the conversation history, and returns the response.
func (a *Agent) Ask(ctx context.Context, userMessage string) (AgentResponse, error) {
	a.done = false
	a.summary = ""
	a.history = append(a.history, Message{Role: "user", Content: userMessage})

	resp, err := a.llm.Complete(ctx, CompletionRequest{
		Model:    a.model,
		Messages: a.history,
		Tools:    a.tools,
	})
	if err != nil {
		// Remove the user message we just appended so the caller can retry.
		a.history = a.history[:len(a.history)-1]
		return AgentResponse{}, err
	}

	a.history = append(a.history, Message{Role: "assistant", Content: resp.Content})

	return AgentResponse{
		Content:   resp.Content,
		ToolCalls: resp.ToolCalls,
		Done:      a.done,
		Summary:   a.summary,
	}, nil
}

// History returns the current conversation history (system prompt + exchanges).
func (a *Agent) History() []Message {
	return a.history
}

// Reset clears conversation history, keeping only the system prompt.
func (a *Agent) Reset() {
	a.history = a.history[:1]
	a.done = false
	a.summary = ""
}

// RunInteractive runs a multi-turn REPL, reading lines from r and writing
// responses to w. The loop exits on EOF, a context cancellation, or when the
// agent calls the done tool.
//
// If task is non-empty it is sent as the first message without prompting the
// user, enabling one-shot mode.
func (a *Agent) RunInteractive(ctx context.Context, r io.Reader, w io.Writer, task string) error {
	scanner := bufio.NewScanner(r)

	send := func(msg string) (bool, error) {
		fmt.Fprintln(w)
		resp, err := a.Ask(ctx, msg)
		if err != nil {
			return false, err
		}
		if resp.Content != "" {
			fmt.Fprintln(w, resp.Content)
		}
		if len(resp.ToolCalls) > 0 {
			var names []string
			for _, tc := range resp.ToolCalls {
				if tc.Name != "done" {
					names = append(names, tc.Name)
				}
			}
			if len(names) > 0 {
				fmt.Fprintf(w, "[tools: %s]\n", strings.Join(names, ", "))
			}
		}
		return resp.Done, nil
	}

	if task != "" {
		fmt.Fprintf(w, "> %s", task)
		done, err := send(task)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}

	fmt.Fprintln(w, "\nGraphQL agent ready. Describe what you want to do (Ctrl+D to exit).")
	for {
		fmt.Fprint(w, "\n> ")
		if !scanner.Scan() {
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		done, err := send(line)
		if err != nil {
			fmt.Fprintf(w, "error: %v\n", err)
			continue
		}
		if done {
			break
		}
	}
	return scanner.Err()
}

// BuildAgentSystemPrompt constructs the standard system prompt, optionally
// embedding a schema context block (e.g. from GraphQLTool.BuildSchemaContext).
func BuildAgentSystemPrompt(schemaContext string) string {
	if schemaContext == "" {
		return fmt.Sprintf(agentSystemPromptTemplate, "(schema not available — use graphql_queries / graphql_mutations to explore)")
	}
	return fmt.Sprintf(agentSystemPromptTemplate, schemaContext)
}
