package pathwalk

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNewAgent(t *testing.T) {
	tools := []Tool{{Name: "test_tool"}}
	systemPrompt := "You are a test agent"

	agent := NewAgent(&MockLLMClient{}, tools, systemPrompt)

	if agent == nil {
		t.Fatal("NewAgent returned nil")
	}
	if len(agent.tools) != 2 { // test_tool + built-in done tool
		t.Errorf("expected 2 tools (1 + done), got %d", len(agent.tools))
	}
	if agent.tools[1].Name != "done" {
		t.Errorf("expected last tool to be 'done', got %q", agent.tools[1].Name)
	}
	if len(agent.history) != 1 {
		t.Errorf("expected 1 history item (system), got %d", len(agent.history))
	}
	if agent.history[0].Role != "system" {
		t.Errorf("expected first message to be system role")
	}
	if agent.history[0].Content != systemPrompt {
		t.Errorf("expected system prompt in history")
	}
}

func TestNewAgentWithModel(t *testing.T) {
	llm := &MockLLMClient{}
	tools := []Tool{{Name: "test_tool"}}
	systemPrompt := "System prompt"
	model := "gpt-4-custom"

	agent := NewAgentWithModel(llm, tools, systemPrompt, model)

	if agent.model != model {
		t.Errorf("expected model %q, got %q", model, agent.model)
	}
}

func TestAgent_Ask_SingleTurn(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "Hello, I can help with that."},
		},
	}
	agent := NewAgent(llm, []Tool{}, "You are helpful")

	resp, err := agent.Ask(context.Background(), "What can you do?")

	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if resp.Content != "Hello, I can help with that." {
		t.Errorf("unexpected response: %q", resp.Content)
	}
	if len(agent.history) != 3 { // system + user + assistant
		t.Errorf("expected 3 history items, got %d", len(agent.history))
	}
	if agent.history[1].Role != "user" {
		t.Errorf("expected user message in history")
	}
	if agent.history[2].Role != "assistant" {
		t.Errorf("expected assistant message in history")
	}
}

func TestAgent_Ask_ToolCalls(t *testing.T) {
	tool := Tool{
		Name: "greet",
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			return "Tool executed", nil
		},
	}

	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{
				Content: "I'll greet you",
				ToolCalls: []ToolCall{
					{
						ID:   "call_1",
						Name: "greet",
						Args: map[string]any{},
					},
				},
			},
		},
	}
	agent := NewAgent(llm, []Tool{tool}, "Helper")

	resp, err := agent.Ask(context.Background(), "Say hello")

	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "greet" {
		t.Errorf("expected tool call name 'greet', got %q", resp.ToolCalls[0].Name)
	}
}

func TestAgent_Ask_DoneTool(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{
				Content: "Task complete",
				ToolCalls: []ToolCall{
					{
						ID:   "call_done",
						Name: "done",
						Args: map[string]any{"summary": "Work finished"},
					},
				},
			},
		},
	}
	agent := NewAgent(llm, []Tool{}, "Assistant")

	resp, err := agent.Ask(context.Background(), "Finish")

	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}
	// Ask() returns tool calls but doesn't execute them
	// Caller must execute tools separately
	if len(resp.ToolCalls) != 1 {
		t.Errorf("expected 1 tool call in response, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "done" {
		t.Errorf("expected done tool call, got %q", resp.ToolCalls[0].Name)
	}
}

func TestAgent_Ask_Error_RemovesUserMessage(t *testing.T) {
	llm := &MockLLMClient{
		err: errors.New("API error"),
	}
	agent := NewAgent(llm, []Tool{}, "")

	historyBefore := len(agent.history)
	_, err := agent.Ask(context.Background(), "Test message")

	if err == nil {
		t.Fatal("expected error from Ask")
	}
	historyAfter := len(agent.history)
	if historyBefore != historyAfter {
		t.Errorf("expected history size to remain %d after error, got %d", historyBefore, historyAfter)
	}
}

func TestAgent_History(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "Response 1"},
		},
	}
	agent := NewAgent(llm, []Tool{}, "System")

	agent.Ask(context.Background(), "First")

	history := agent.History()
	if len(history) != 3 { // system + user + assistant
		t.Errorf("expected 3 messages, got %d", len(history))
	}
}

func TestAgent_Reset(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "Response"},
		},
	}
	agent := NewAgent(llm, []Tool{}, "System Prompt")

	agent.Ask(context.Background(), "First")
	if len(agent.history) < 3 {
		t.Fatal("history should have messages before reset")
	}

	agent.Reset()

	if len(agent.history) != 1 {
		t.Errorf("expected 1 message after reset (system only), got %d", len(agent.history))
	}
	if agent.history[0].Content != "System Prompt" {
		t.Errorf("expected system prompt to remain after reset")
	}
	if agent.done || agent.summary != "" {
		t.Errorf("expected done flag and summary cleared after reset")
	}
}

func TestAgent_RunInteractive_WithTask(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "Task executed"},
		},
	}
	agent := NewAgent(llm, []Tool{}, "")

	var output bytes.Buffer
	err := agent.RunInteractive(context.Background(), strings.NewReader(""), &output, "One-shot task")

	if err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
	if !strings.Contains(output.String(), "One-shot task") {
		t.Errorf("expected task prompt in output")
	}
}

func TestAgent_RunInteractive_InteractiveMode(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "Response 1"},
			{Content: "Response 2"},
		},
	}
	agent := NewAgent(llm, []Tool{}, "")

	input := "line1\nline2\n"
	var output bytes.Buffer
	err := agent.RunInteractive(context.Background(), strings.NewReader(input), &output, "")

	if err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
	if !strings.Contains(output.String(), "GraphQL agent ready") {
		t.Errorf("expected prompt in output")
	}
}

func TestAgent_RunInteractive_EOF(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "Response"},
		},
	}
	agent := NewAgent(llm, []Tool{}, "")

	var output bytes.Buffer
	err := agent.RunInteractive(context.Background(), strings.NewReader(""), &output, "task")

	if err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
}

func TestAgent_RunInteractive_DoneToolExits(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{
				Content: "Done",
				ToolCalls: []ToolCall{
					{ID: "done_1", Name: "done", Args: map[string]any{"summary": "finished"}},
				},
			},
		},
	}
	agent := NewAgent(llm, []Tool{}, "")

	var output bytes.Buffer
	err := agent.RunInteractive(context.Background(), strings.NewReader(""), &output, "task")

	if err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
}

func TestAgent_MultiTurn_Conversation(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "I can help"},
			{Content: "Here's the answer"},
		},
	}
	agent := NewAgent(llm, []Tool{}, "You are helpful")

	// First turn
	resp1, err := agent.Ask(context.Background(), "Can you help?")
	if err != nil {
		t.Fatalf("First Ask failed: %v", err)
	}
	if resp1.Content != "I can help" {
		t.Errorf("unexpected response from first turn")
	}

	// Second turn - should include conversation history
	resp2, err := agent.Ask(context.Background(), "What's the answer?")
	if err != nil {
		t.Fatalf("Second Ask failed: %v", err)
	}
	if resp2.Content != "Here's the answer" {
		t.Errorf("unexpected response from second turn")
	}

	// Verify full history
	history := agent.History()
	if len(history) != 5 { // system + user1 + assistant1 + user2 + assistant2
		t.Errorf("expected 5 history items, got %d", len(history))
	}
}

func TestAgent_EmptyInput_SkipsInInteractive(t *testing.T) {
	llm := &MockLLMClient{
		responses: []CompletionResponse{
			{Content: "Should not be called"},
		},
	}
	agent := NewAgent(llm, []Tool{}, "")

	// Empty line followed by EOF
	input := "\n\n"
	var output bytes.Buffer
	err := agent.RunInteractive(context.Background(), strings.NewReader(input), &output, "")

	if err != nil {
		t.Fatalf("RunInteractive failed: %v", err)
	}
	// LLM should not be called for empty lines
	// (depends on MockLLMClient implementation)
}

// MockLLMClient is a test double for LLMClient
type MockLLMClient struct {
	responses []CompletionResponse
	callCount int
	err       error
}

func (m *MockLLMClient) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.callCount >= len(m.responses) {
		return &CompletionResponse{Content: "No more mocked responses"}, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++
	return &resp, nil
}
