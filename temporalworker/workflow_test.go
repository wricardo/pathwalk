package temporalworker

import (
	"context"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/pathwaytest"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
)

// ── Test fixtures ──────────────────────────────────────────────────────────

const minimalPathwayJSON = `{
  "nodes": [
    {"id":"n1","type":"Default","data":{"name":"Start","isStart":true,"prompt":"Hello","condition":"done"}},
    {"id":"n2","type":"End Call","data":{"name":"End","text":"Goodbye"}}
  ],
  "edges": [
    {"id":"e1","source":"n1","target":"n2","data":{"label":"continue","description":""}}
  ]
}`

const threeStepPathwayJSON = `{
  "nodes": [
    {"id":"n1","type":"Default","data":{"name":"Start","isStart":true,"prompt":"Step 1","condition":"done"}},
    {"id":"n2","type":"Default","data":{"name":"Middle","prompt":"Step 2","condition":"done"}},
    {"id":"n3","type":"End Call","data":{"name":"End","text":"Done"}}
  ],
  "edges": [
    {"id":"e1","source":"n1","target":"n2","data":{"label":"next","description":""}},
    {"id":"e2","source":"n2","target":"n3","data":{"label":"finish","description":""}}
  ]
}`

const maxTurnsPathwayJSON = `{
  "nodes": [
    {"id":"n1","type":"Default","data":{"name":"Start","isStart":true,"prompt":"loop","condition":"done"}},
    {"id":"n2","type":"Default","data":{"name":"Middle","prompt":"loop","condition":"done"}},
    {"id":"n3","type":"End Call","data":{"name":"End","text":"Done"}}
  ],
  "edges": [
    {"id":"e1","source":"n1","target":"n2","data":{"label":"next","description":""}},
    {"id":"e2","source":"n2","target":"n3","data":{"label":"finish","description":""}}
  ],
  "maxTurns": 1
}`

// ── cachedParsePathway tests ───────────────────────────────────────────────

func clearPathwayCache() {
	pathwayCache.Range(func(key, _ any) bool {
		pathwayCache.Delete(key)
		return true
	})
}

func TestCachedParsePathway_ReturnsCached(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	data := []byte(minimalPathwayJSON)
	p1, err := cachedParsePathway(data)
	require.NoError(t, err)
	p2, err := cachedParsePathway(data)
	require.NoError(t, err)
	if p1 != p2 {
		t.Error("expected same pointer for same input")
	}
}

func TestCachedParsePathway_DifferentInputs(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	p1, err := cachedParsePathway([]byte(minimalPathwayJSON))
	require.NoError(t, err)
	p2, err := cachedParsePathway([]byte(threeStepPathwayJSON))
	require.NoError(t, err)
	if p1 == p2 {
		t.Error("expected different pointers for different inputs")
	}
}

func TestCachedParsePathway_InvalidJSON(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	_, err := cachedParsePathway([]byte("not json"))
	require.Error(t, err)
	_, err = cachedParsePathway([]byte("not json"))
	require.Error(t, err)
}

// ── Activity tests ─────────────────────────────────────────────────────────

func TestExecuteStep_Success(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()

	mockLLM := pathwaytest.NewMockLLMClient()
	mockLLM.OnNode("n1", pathwaytest.MockResponse{Content: "Hello!"})

	acts := &PathwayActivities{LLMClientOverride: mockLLM}
	env.RegisterActivity(acts.ExecuteStep)

	state := pathwalk.NewState("test task")
	input := StepActivityInput{
		PathwayJSON:   []byte(minimalPathwayJSON),
		State:         state,
		CurrentNodeID: "n1",
	}

	val, err := env.ExecuteActivity(acts.ExecuteStep, input)
	require.NoError(t, err)

	var result StepActivityResult
	require.NoError(t, val.Get(&result))
	require.False(t, result.StepResult.Done)
	require.Equal(t, "n2", result.StepResult.NextNodeID)
}

func TestExecuteStep_InvalidPathway(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()

	acts := &PathwayActivities{}
	env.RegisterActivity(acts.ExecuteStep)

	input := StepActivityInput{
		PathwayJSON:   []byte("not valid json"),
		State:         pathwalk.NewState("test"),
		CurrentNodeID: "n1",
	}

	_, err := env.ExecuteActivity(acts.ExecuteStep, input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ParsePathway")
}

func TestExecuteStep_LLMClientOverride(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()

	mockLLM := pathwaytest.NewMockLLMClient()
	mockLLM.SetDefault(pathwaytest.MockResponse{Content: "from mock"})

	acts := &PathwayActivities{LLMClientOverride: mockLLM}
	env.RegisterActivity(acts.ExecuteStep)

	input := StepActivityInput{
		PathwayJSON:   []byte(minimalPathwayJSON),
		State:         pathwalk.NewState("test"),
		CurrentNodeID: "n1",
	}

	val, err := env.ExecuteActivity(acts.ExecuteStep, input)
	require.NoError(t, err)

	var result StepActivityResult
	require.NoError(t, val.Get(&result))
	require.Equal(t, 1, mockLLM.CallCount("n1"), "expected mock to be called")
}

func TestExecuteStep_TerminalNode(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()

	mockLLM := pathwaytest.NewMockLLMClient()
	acts := &PathwayActivities{LLMClientOverride: mockLLM}
	env.RegisterActivity(acts.ExecuteStep)

	state := pathwalk.NewState("test")
	state.Steps = append(state.Steps, pathwalk.Step{NodeID: "n1", NodeName: "Start", Output: "Hello!"})
	input := StepActivityInput{
		PathwayJSON:   []byte(minimalPathwayJSON),
		State:         state,
		CurrentNodeID: "n2",
	}

	val, err := env.ExecuteActivity(acts.ExecuteStep, input)
	require.NoError(t, err)

	var result StepActivityResult
	require.NoError(t, val.Get(&result))
	require.True(t, result.StepResult.Done)
	require.Equal(t, "terminal", result.StepResult.Reason)
}

func TestExecuteStep_GraphQLToolInjection(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()

	// Pathway with a graphqlEndpoint set — activity should inject graphql tools.
	const pathwayWithGQL = `{
  "nodes": [
    {"id":"n1","type":"Default","data":{"name":"Start","isStart":true,"prompt":"Query something","condition":"done"}},
    {"id":"n2","type":"End Call","data":{"name":"End","text":"Done"}}
  ],
  "edges": [{"id":"e1","source":"n1","target":"n2","data":{"label":"continue","description":""}}],
  "graphqlEndpoint": "http://localhost:9999/graphql"
}`

	mockLLM := pathwaytest.NewMockLLMClient()
	mockLLM.SetDefault(pathwaytest.MockResponse{Content: "result"})

	acts := &PathwayActivities{LLMClientOverride: mockLLM}
	env.RegisterActivity(acts.ExecuteStep)

	val, err := env.ExecuteActivity(acts.ExecuteStep, StepActivityInput{
		PathwayJSON:   []byte(pathwayWithGQL),
		State:         pathwalk.NewState("test"),
		CurrentNodeID: "n1",
	})
	require.NoError(t, err)

	var result StepActivityResult
	require.NoError(t, val.Get(&result))

	// The LLM should have been called with graphql tools in the request.
	require.NotEmpty(t, mockLLM.Calls)
	toolNames := make([]string, 0)
	for _, tool := range mockLLM.Calls[0].Request.Tools {
		toolNames = append(toolNames, tool.Name)
	}
	require.Contains(t, toolNames, "graphql_query", "graphql tools should be injected when graphqlEndpoint is set")
	require.Contains(t, toolNames, "graphql_mutation")
}

// ── Workflow tests ─────────────────────────────────────────────────────────

// completionCallbackStub is a dummy activity function registered under the name
// "HandleComplete" so the Temporal test environment can mock it.
func completionCallbackStub(_ context.Context, _ CompletionCallbackInput) error {
	return nil
}

func TestPathwayWorkflow_SingleStep(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, in StepActivityInput) (*StepActivityResult, error) {
			s := pathwalk.NewState("test")
			if in.CurrentNodeID == "n1" {
				s.Steps = append(s.Steps, pathwalk.Step{NodeID: "n1", NodeName: "Start", Output: "Hello!"})
				return &StepActivityResult{
					State:      s,
					StepResult: &pathwalk.StepResult{NextNodeID: "n2", Output: "Hello!"},
				}, nil
			}
			// n2 = terminal
			s.Steps = append(s.Steps,
				pathwalk.Step{NodeID: "n1", NodeName: "Start", Output: "Hello!"},
				pathwalk.Step{NodeID: "n2", NodeName: "End", Output: "Goodbye"},
			)
			return &StepActivityResult{
				State:      s,
				StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "Hello!"},
			}, nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(minimalPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result pathwalk.RunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "terminal", result.Reason)
	require.Equal(t, "Hello!", result.Output)
}

func TestPathwayWorkflow_MultipleSteps(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	callCount := 0
	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ StepActivityInput) (*StepActivityResult, error) {
			callCount++
			s := pathwalk.NewState("test")
			if callCount < 3 {
				nextNodes := []string{"n2", "n3"}
				return &StepActivityResult{
					State:      s,
					StepResult: &pathwalk.StepResult{NextNodeID: nextNodes[callCount-1]},
				}, nil
			}
			return &StepActivityResult{
				State:      s,
				StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "Done"},
			}, nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(threeStepPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.Equal(t, 3, callCount, "expected 3 activity calls")
}

func TestPathwayWorkflow_MaxStepsExceeded(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&StepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{NextNodeID: "n2"},
		}, nil)

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(minimalPathwayJSON),
		Task:        "test",
		MaxSteps:    2,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result pathwalk.RunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "max_steps", result.Reason)
}

func TestPathwayWorkflow_MaxTurnsCap(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	callCount := 0
	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ StepActivityInput) (*StepActivityResult, error) {
			callCount++
			return &StepActivityResult{
				State:      pathwalk.NewState("test"),
				StepResult: &pathwalk.StepResult{NextNodeID: "n2"},
			}, nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(maxTurnsPathwayJSON), // maxTurns: 1
		Task:        "test",
		MaxSteps:    50,
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result pathwalk.RunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "max_steps", result.Reason)
	require.Equal(t, 1, callCount, "maxTurns=1 should cap to 1 activity call")
}

func TestPathwayWorkflow_InvalidPathwayJSON(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte("not json"),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	require.Contains(t, err.Error(), "parsing pathway")
}

func TestPathwayWorkflow_ActivityError(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errActivityFailed)

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(minimalPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
}

var errActivityFailed = &testError{msg: "simulated activity failure"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestPathwayWorkflow_QueryHandler(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&StepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "done"},
		}, nil)

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(minimalPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	val, err := env.QueryWorkflow("get-result")
	require.NoError(t, err)

	var snapshot RunSnapshot
	require.NoError(t, val.Get(&snapshot))
	require.Equal(t, "done", snapshot.Output)
}

func TestPathwayWorkflow_CompletionCallback(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterActivityWithOptions(completionCallbackStub, activity.RegisterOptions{
		Name: "HandleComplete",
	})

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&StepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "done"},
		}, nil)

	callbackCalled := false
	env.OnActivity("HandleComplete", mock.Anything, mock.Anything).
		Return(func(_ context.Context, in CompletionCallbackInput) error {
			callbackCalled = true
			require.Equal(t, "exec-123", in.Data)
			require.NotNil(t, in.Result)
			require.Equal(t, "terminal", in.Result.Reason)
			return nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON:            []byte(minimalPathwayJSON),
		Task:                   "test",
		CompletionTaskQueue:    "my-queue",
		CompletionActivityName: "HandleComplete",
		CompletionData:         "exec-123",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, callbackCalled, "completion callback should have been called")
}

func TestPathwayWorkflow_CompletionCallbackOnMaxSteps(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterActivityWithOptions(completionCallbackStub, activity.RegisterOptions{
		Name: "HandleComplete",
	})

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&StepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{NextNodeID: "n2"},
		}, nil)

	callbackCalled := false
	env.OnActivity("HandleComplete", mock.Anything, mock.Anything).
		Return(func(_ context.Context, in CompletionCallbackInput) error {
			callbackCalled = true
			require.NotNil(t, in.Result)
			require.Equal(t, "max_steps", in.Result.Reason)
			return nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON:            []byte(minimalPathwayJSON),
		Task:                   "test",
		MaxSteps:               1,
		CompletionTaskQueue:    "my-queue",
		CompletionActivityName: "HandleComplete",
		CompletionData:         "data",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, callbackCalled, "completion callback should fire on max_steps")
}

func TestPathwayWorkflow_NoCallbackWhenNotConfigured(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&StepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "done"},
		}, nil)

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(minimalPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestPathwayWorkflow_EmptyCurrentNodeID(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&StepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{NextNodeID: ""},
		}, nil).Once()

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(minimalPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result pathwalk.RunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "missing_node", result.Reason)
}

func TestPathwayWorkflow_DefaultMaxSteps(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	callCount := 0
	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ StepActivityInput) (*StepActivityResult, error) {
			callCount++
			return &StepActivityResult{
				State:      pathwalk.NewState("test"),
				StepResult: &pathwalk.StepResult{NextNodeID: "n2"},
			}, nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(minimalPathwayJSON),
		Task:        "test",
		MaxSteps:    0, // should default to 50
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result pathwalk.RunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "max_steps", result.Reason)
	require.Equal(t, 50, callCount, "default MaxSteps should be 50")
}
