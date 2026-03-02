// Package pathwaytest provides test helpers for the pathwalk library,
// including a mock LLM client for unit testing without hitting a real API.
package pathwaytest

import (
	"context"
	"fmt"
	"sync"

	aipe "github.com/wricardo/pathwalk"
)

// MockResponse is a scripted LLM response for a specific node and call purpose.
type MockResponse struct {
	// Content is the text content the LLM should return.
	Content string
	// ToolCalls is the list of tool calls the LLM should make before returning Content.
	ToolCalls []MockToolCall
	// Error, if non-nil, causes Complete to return this error.
	Error error
}

// MockToolCall scripts a single tool invocation in a mock response.
type MockToolCall struct {
	// Name is the tool function name.
	Name string
	// Args is the arguments the LLM passes to the tool.
	Args map[string]any
}

// MockLLMClient is a test double for aipe.LLMClient. It lets tests control LLM
// behaviour per node ID and call purpose without hitting a real API.
//
// Usage:
//
//	mock := pathwaytest.NewMockLLMClient()
//	mock.OnNode("node-id-1", pathwaytest.MockResponse{Content: "hello"})
//	mock.OnNodePurpose("node-id-2", "extract_vars", pathwaytest.MockResponse{
//	    ToolCalls: []pathwaytest.MockToolCall{
//	        {Name: "set_variables", Args: map[string]any{"operation_type": "reporting"}},
//	    },
//	})
//	engine := aipe.NewEngine(pathway, mock)
type MockLLMClient struct {
	mu sync.Mutex

	// nodeResponses maps nodeID → []MockResponse (consumed in order).
	nodeResponses map[string][]MockResponse

	// purposeResponses maps "nodeID:purpose" → []MockResponse (consumed in order).
	// More specific than nodeResponses; checked first.
	purposeResponses map[string][]MockResponse

	// defaultResponse is returned when no matching entry is found.
	defaultResponse MockResponse

	// Calls records every CompletionRequest received, in order.
	Calls []RecordedCall
}

// RecordedCall captures a single call made to the mock.
type RecordedCall struct {
	NodeID  string
	Purpose string
	Request aipe.CompletionRequest
}

// NewMockLLMClient creates an empty MockLLMClient with an empty default response.
func NewMockLLMClient() *MockLLMClient {
	return &MockLLMClient{
		nodeResponses:    make(map[string][]MockResponse),
		purposeResponses: make(map[string][]MockResponse),
	}
}

// OnNode queues a response to be returned when the engine calls the LLM from
// the given nodeID, regardless of call purpose.
// Multiple calls to OnNode for the same nodeID are consumed in order.
func (m *MockLLMClient) OnNode(nodeID string, resp MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodeResponses[nodeID] = append(m.nodeResponses[nodeID], resp)
}

// OnNodePurpose queues a response that is only used when both nodeID AND
// purpose match. Purpose values: "execute", "extract_vars", "route".
func (m *MockLLMClient) OnNodePurpose(nodeID, purpose string, resp MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := nodeID + ":" + purpose
	m.purposeResponses[key] = append(m.purposeResponses[key], resp)
}

// SetDefault sets the response returned when no matching entry exists.
func (m *MockLLMClient) SetDefault(resp MockResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.defaultResponse = resp
}

// Complete implements aipe.LLMClient. It looks up the scripted response, executes
// any scripted tool calls (so that tool Fns in the request run and accumulate
// results), and returns the mocked content.
func (m *MockLLMClient) Complete(ctx context.Context, req aipe.CompletionRequest) (*aipe.CompletionResponse, error) {
	nodeID := aipe.NodeIDFromContext(ctx)
	purpose := aipe.CallPurposeFromContext(ctx)

	m.mu.Lock()
	// Record the call
	m.Calls = append(m.Calls, RecordedCall{NodeID: nodeID, Purpose: purpose, Request: req})

	// Look up scripted response (purpose-specific first, then node-only, then default)
	resp, found := m.dequeue(nodeID, purpose)
	if !found {
		resp = m.defaultResponse
	}
	m.mu.Unlock()

	if resp.Error != nil {
		return nil, resp.Error
	}

	// Execute scripted tool calls
	toolMap := make(map[string]aipe.Tool, len(req.Tools))
	for _, t := range req.Tools {
		toolMap[t.Name] = t
	}

	var allCalls []aipe.ToolCall
	for i, tc := range resp.ToolCalls {
		tcResult := aipe.ToolCall{
			ID:   fmt.Sprintf("mock-tc-%d", i),
			Name: tc.Name,
			Args: tc.Args,
		}
		tool, ok := toolMap[tc.Name]
		if ok && tool.Fn != nil {
			result, err := tool.Fn(ctx, tc.Args)
			if err != nil {
				tcResult.Error = err.Error()
				tcResult.Result = "error: " + err.Error()
			} else {
				tcResult.Result = result
			}
		} else {
			tcResult.Result = tc.Args // return args as-is if no Fn
		}
		allCalls = append(allCalls, tcResult)
	}

	return &aipe.CompletionResponse{
		Content:   resp.Content,
		ToolCalls: allCalls,
	}, nil
}

// dequeue removes and returns the next response for the given nodeID+purpose.
// Must be called with m.mu held.
func (m *MockLLMClient) dequeue(nodeID, purpose string) (MockResponse, bool) {
	// Check purpose-specific first
	key := nodeID + ":" + purpose
	if q, ok := m.purposeResponses[key]; ok && len(q) > 0 {
		resp := q[0]
		m.purposeResponses[key] = q[1:]
		return resp, true
	}

	// Check node-only
	if q, ok := m.nodeResponses[nodeID]; ok && len(q) > 0 {
		resp := q[0]
		m.nodeResponses[nodeID] = q[1:]
		return resp, true
	}

	return MockResponse{}, false
}

// CallCount returns the number of times Complete was called for the given nodeID.
func (m *MockLLMClient) CallCount(nodeID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, c := range m.Calls {
		if c.NodeID == nodeID {
			count++
		}
	}
	return count
}
