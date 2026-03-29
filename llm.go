package pathwalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
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
//
// When Complete returns both a non-nil response and a non-nil error (e.g. the
// tool-call round limit was exceeded), ToolCalls contains the tool calls the
// LLM emitted before the error occurred. These calls were NOT executed — they
// are recorded so callers can surface what was attempted. Content will be
// empty in this partial-error case.
type CompletionResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// cleaningTransport is an http.RoundTripper that strips non-standard fields
// (e.g. "reasoning") from chat completion responses before the SDK parses them.
type cleaningTransport struct {
	wrapped http.RoundTripper
}

func (t *cleaningTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.wrapped.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return resp, err
	}

	// Strip "reasoning" from each message in choices[].message so the SDK parses cleanly.
	var raw map[string]any
	if jsonErr := json.Unmarshal(body, &raw); jsonErr == nil {
		if choices, ok := raw["choices"].([]any); ok {
			for _, c := range choices {
				if cm, ok := c.(map[string]any); ok {
					if msg, ok := cm["message"].(map[string]any); ok {
						delete(msg, "reasoning")
					}
				}
			}
		}
		if cleaned, jsonErr := json.Marshal(raw); jsonErr == nil {
			body = cleaned
		}
	}

	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	return resp, nil
}

// OpenAIClient implements LLMClient using the openai-go SDK.
// It is compatible with any OpenAI-compatible API (venu, Groq, Ollama, OpenRouter, etc.).
type OpenAIClient struct {
	client openai.Client
	model  string
}

// NewOpenAIClient creates a new OpenAIClient.
// apiKey and baseURL can be empty to use environment defaults.
func NewOpenAIClient(apiKey, baseURL, model string) *OpenAIClient {
	transport := &cleaningTransport{wrapped: http.DefaultTransport}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Transport: transport}),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &OpenAIClient{
		client: openai.NewClient(opts...),
		model:  model,
	}
}

const maxToolRounds = 25

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
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  shared.FunctionParameters(t.Parameters),
			},
		})
	}

	model := req.Model
	if model == "" {
		model = c.model
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(model),
		Messages: msgs,
		Tools:    toolParams,
	}
	if req.Temperature > 0 {
		params.Temperature = openai.Float(req.Temperature)
	}
	if req.MaxTokens > 0 {
		params.MaxTokens = openai.Int(int64(req.MaxTokens))
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

		if choice.FinishReason != "tool_calls" {
			return &CompletionResponse{
				Content:   choice.Message.Content,
				ToolCalls: allToolCalls,
			}, nil
		}

		// Append assistant message (with tool_calls) to conversation.
		params.Messages = append(params.Messages, choice.Message.ToParam())

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
			params.Messages = append(params.Messages,
				openai.ToolMessage(string(resultBytes), tc.ID))
		}
	}

	return &CompletionResponse{
		ToolCalls: allToolCalls,
	}, fmt.Errorf("exceeded maximum tool call rounds (%d)", maxToolRounds)
}
