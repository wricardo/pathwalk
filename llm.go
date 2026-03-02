package pathwalk

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// LLMClient is the interface for making LLM completions.
// The implementation is responsible for handling the tool-call loop.
type LLMClient interface {
	// Complete sends messages to the LLM, executes any tool calls, and returns
	// the final text content plus a record of all tool calls made.
	Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error)
}

// CompletionRequest is the input to LLMClient.Complete.
type CompletionRequest struct {
	Model       string
	Messages    []Message
	Tools       []Tool
	Temperature float64
	MaxTokens   int
}

// CompletionResponse is the output from LLMClient.Complete.
type CompletionResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// OpenAIClient implements LLMClient using the openai-go SDK.
// It is compatible with any OpenAI-compatible API (Groq, Ollama, OpenRouter, etc.).
type OpenAIClient struct {
	client *openai.Client
	model  string
}

// NewOpenAIClient creates a new OpenAIClient.
// apiKey and baseURL can be empty to use environment defaults.
func NewOpenAIClient(apiKey, baseURL, model string) *OpenAIClient {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &OpenAIClient{
		client: openai.NewClient(opts...),
		model:  model,
	}
}

const maxToolRounds = 10

// Complete sends the request to the OpenAI API, handles tool call loops, and returns
// the final assistant content.
func (c *OpenAIClient) Complete(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	// Build openai messages
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			msgs = append(msgs, openai.SystemMessage(m.Content))
		case "user":
			msgs = append(msgs, openai.UserMessage(m.Content))
		case "assistant":
			msgs = append(msgs, openai.AssistantMessage(m.Content))
		}
	}

	// Build tool params and lookup map
	toolMap := make(map[string]Tool, len(req.Tools))
	toolParams := make([]openai.ChatCompletionToolParam, 0, len(req.Tools))
	for _, t := range req.Tools {
		toolMap[t.Name] = t
		toolParams = append(toolParams, openai.ChatCompletionToolParam{
			Type: openai.F(openai.ChatCompletionToolTypeFunction),
			Function: openai.F(openai.FunctionDefinitionParam{
				Name:        openai.F(t.Name),
				Description: openai.F(t.Description),
				Parameters:  openai.F(openai.FunctionParameters(t.Parameters)),
			}),
		})
	}

	model := req.Model
	if model == "" {
		model = c.model
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.F(openai.ChatModel(model)),
		Messages: openai.F(msgs),
	}
	if len(toolParams) > 0 {
		params.Tools = openai.F(toolParams)
	}
	if req.Temperature > 0 {
		params.Temperature = openai.F(req.Temperature)
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.F(int64(req.MaxTokens))
	}

	var allToolCalls []ToolCall

	for round := 0; round < maxToolRounds; round++ {
		resp, err := c.client.Chat.Completions.New(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("openai completion (round %d): %w", round, err)
		}
		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("openai returned no choices")
		}

		choice := resp.Choices[0]

		if choice.FinishReason != openai.ChatCompletionChoicesFinishReasonToolCalls {
			return &CompletionResponse{
				Content:   choice.Message.Content,
				ToolCalls: allToolCalls,
			}, nil
		}

		// Append assistant message (with tool_calls) to conversation.
		// ChatCompletionMessage satisfies ChatCompletionMessageParamUnion directly.
		params.Messages = openai.F(append(params.Messages.Value, choice.Message))

		// Execute each tool call and append tool result messages
		for _, tc := range choice.Message.ToolCalls {
			var args map[string]any
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
				args = map[string]any{}
			}

			tcResult := ToolCall{
				ID:   tc.ID,
				Name: tc.Function.Name,
				Args: args,
			}

			tool, ok := toolMap[tc.Function.Name]
			if ok {
				result, toolErr := tool.Fn(ctx, args)
				if toolErr != nil {
					tcResult.Error = toolErr.Error()
					tcResult.Result = "error: " + toolErr.Error()
				} else {
					tcResult.Result = result
				}
			} else {
				tcResult.Error = "unknown tool: " + tc.Function.Name
				tcResult.Result = tcResult.Error
			}
			allToolCalls = append(allToolCalls, tcResult)

			// Serialize result for the tool message
			resultBytes, _ := json.Marshal(tcResult.Result)
			params.Messages = openai.F(append(
				params.Messages.Value,
				openai.ToolMessage(tc.ID, string(resultBytes)),
			))
		}
	}

	return nil, fmt.Errorf("exceeded maximum tool call rounds (%d)", maxToolRounds)
}
