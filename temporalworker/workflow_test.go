package temporalworker

import (
	"context"
	"fmt"
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

// ── Checkpoint / signal tests ──────────────────────────────────────────────

// checkpointPathwayJSON: start → checkpoint(human_input) → end
const checkpointPathwayJSON = `{
  "nodes": [
    {"id":"n1","type":"Default","data":{"name":"Start","isStart":true,"prompt":"Hello","condition":"done"}},
    {"id":"cp1","type":"Checkpoint","data":{"name":"Ask Name","checkpointMode":"human_input","checkpointPrompt":"What is your name?","checkpointVariable":"user_name"}},
    {"id":"n2","type":"End Call","data":{"name":"End","text":"Goodbye"}}
  ],
  "edges": [
    {"id":"e1","source":"n1","target":"cp1","data":{"label":"continue","description":""}},
    {"id":"e2","source":"cp1","target":"n2","data":{"label":"continue","description":""}}
  ]
}`

// twoCheckpointPathwayJSON: start → cp1 → cp2 → end
const twoCheckpointPathwayJSON = `{
  "nodes": [
    {"id":"n1","type":"Default","data":{"name":"Start","isStart":true,"prompt":"Hello","condition":"done"}},
    {"id":"cp1","type":"Checkpoint","data":{"name":"First Question","checkpointMode":"human_input","checkpointPrompt":"First?","checkpointVariable":"answer1"}},
    {"id":"cp2","type":"Checkpoint","data":{"name":"Second Question","checkpointMode":"human_input","checkpointPrompt":"Second?","checkpointVariable":"answer2"}},
    {"id":"n2","type":"End Call","data":{"name":"End","text":"Done"}}
  ],
  "edges": [
    {"id":"e1","source":"n1","target":"cp1","data":{"label":"continue","description":""}},
    {"id":"e2","source":"cp1","target":"cp2","data":{"label":"continue","description":""}},
    {"id":"e3","source":"cp2","target":"n2","data":{"label":"continue","description":""}}
  ]
}`

// TestExecuteResumeStep_Success tests the ExecuteResumeStep activity directly.
// ResumeStep does not call the LLM — it stores the response value and routes to
// the next node — so no mock LLM is required.
func TestExecuteResumeStep_Success(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()

	acts := &PathwayActivities{} // no LLM needed
	env.RegisterActivity(acts.ExecuteResumeStep)

	state := pathwalk.NewState("test task")
	input := ResumeStepActivityInput{
		PathwayJSON:  []byte(checkpointPathwayJSON),
		State:        state,
		ResumeNodeID: "cp1",
		Signal:       ResumeSignal{Value: "Alice"},
	}

	val, err := env.ExecuteActivity(acts.ExecuteResumeStep, input)
	require.NoError(t, err)

	var result ResumeStepActivityResult
	require.NoError(t, val.Get(&result))
	require.False(t, result.StepResult.Done)
	require.Equal(t, "n2", result.StepResult.NextNodeID)
	require.Equal(t, "Alice", result.State.Variables["user_name"], "signal value stored in checkpoint variable")
}

// TestExecuteResumeStep_InvalidPathway verifies that an unparseable pathway is rejected.
func TestExecuteResumeStep_InvalidPathway(t *testing.T) {
	t.Cleanup(clearPathwayCache)
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestActivityEnvironment()

	acts := &PathwayActivities{}
	env.RegisterActivity(acts.ExecuteResumeStep)

	input := ResumeStepActivityInput{
		PathwayJSON:  []byte("not valid json"),
		State:        pathwalk.NewState("test"),
		ResumeNodeID: "cp1",
		Signal:       ResumeSignal{Value: "anything"},
	}

	_, err := env.ExecuteActivity(acts.ExecuteResumeStep, input)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ParsePathway")
}

// TestPathwayWorkflow_Checkpoint_ResumesOnSignal verifies the full checkpoint cycle:
// the workflow routes through two steps (n1 → cp1), blocks at the checkpoint, the
// resume signal unblocks it, and the workflow completes.
func TestPathwayWorkflow_Checkpoint_ResumesOnSignal(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	// First call: route n1→cp1. Second call: suspend at checkpoint.
	stepCallCount := 0
	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ StepActivityInput) (*StepActivityResult, error) {
			stepCallCount++
			s := pathwalk.NewState("test")
			if stepCallCount == 1 {
				return &StepActivityResult{State: s, StepResult: &pathwalk.StepResult{NextNodeID: "cp1"}}, nil
			}
			// Second call: checkpoint node — suspend.
			return &StepActivityResult{
				State: s,
				StepResult: &pathwalk.StepResult{
					WaitCondition: &pathwalk.WaitCondition{
						Mode:         pathwalk.CheckpointModeHumanInput,
						NodeID:       "cp1",
						NodeName:     "Ask Name",
						VariableName: "user_name",
					},
				},
			}, nil
		})

	// Signal is buffered in resumeCh; consumed when the workflow calls Receive.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ResumeSignalName, ResumeSignal{Value: "Alice"})
	}, 0)

	env.OnActivity((*PathwayActivities).ExecuteResumeStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&ResumeStepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "Hello Alice"},
		}, nil)

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(checkpointPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result pathwalk.RunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "terminal", result.Reason)
	require.Equal(t, "Hello Alice", result.Output)
	require.Equal(t, 2, stepCallCount, "expected ExecuteStep called for n1 and cp1")
}

// TestPathwayWorkflow_Checkpoint_CallbackAfterResume verifies that the completion
// callback fires with the correct result after a checkpoint is resumed.
func TestPathwayWorkflow_Checkpoint_CallbackAfterResume(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterActivityWithOptions(completionCallbackStub, activity.RegisterOptions{
		Name: "HandleComplete",
	})

	stepCallCount := 0
	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ StepActivityInput) (*StepActivityResult, error) {
			stepCallCount++
			s := pathwalk.NewState("test")
			if stepCallCount == 1 {
				return &StepActivityResult{State: s, StepResult: &pathwalk.StepResult{NextNodeID: "cp1"}}, nil
			}
			return &StepActivityResult{
				State: s,
				StepResult: &pathwalk.StepResult{
					WaitCondition: &pathwalk.WaitCondition{
						Mode:   pathwalk.CheckpointModeHumanInput,
						NodeID: "cp1",
					},
				},
			}, nil
		})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ResumeSignalName, ResumeSignal{Value: "approved"})
	}, 0)

	env.OnActivity((*PathwayActivities).ExecuteResumeStep, mock.Anything, mock.Anything, mock.Anything).
		Return(&ResumeStepActivityResult{
			State:      pathwalk.NewState("test"),
			StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "done after resume"},
		}, nil)

	callbackCalled := false
	env.OnActivity("HandleComplete", mock.Anything, mock.Anything).
		Return(func(_ context.Context, in CompletionCallbackInput) error {
			callbackCalled = true
			require.Equal(t, "terminal", in.Result.Reason)
			require.Equal(t, "done after resume", in.Result.Output)
			require.Empty(t, in.Err)
			return nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON:            []byte(checkpointPathwayJSON),
		Task:                   "test",
		CompletionTaskQueue:    "my-queue",
		CompletionActivityName: "HandleComplete",
		CompletionData:         "run-xyz",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, callbackCalled, "completion callback should fire after checkpoint resume")
}

// TestPathwayWorkflow_MultipleCheckpoints verifies that two sequential checkpoint
// nodes each block on their own signal and both receive the correct response.
// Uses a counter to differentiate the three ExecuteStep calls (n1, cp1, cp2)
// since in.CurrentNodeID is not reliably populated in the test environment when
// signals are registered.
func TestPathwayWorkflow_MultipleCheckpoints(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	// Call 1 → n1 (route to cp1). Call 2 → cp1 (first checkpoint). Call 3 → cp2 (second checkpoint).
	stepCallCount := 0
	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ StepActivityInput) (*StepActivityResult, error) {
			stepCallCount++
			s := pathwalk.NewState("test")
			switch stepCallCount {
			case 1:
				return &StepActivityResult{State: s, StepResult: &pathwalk.StepResult{NextNodeID: "cp1"}}, nil
			case 2:
				return &StepActivityResult{
					State: s,
					StepResult: &pathwalk.StepResult{
						WaitCondition: &pathwalk.WaitCondition{
							Mode:   pathwalk.CheckpointModeHumanInput,
							NodeID: "cp1",
						},
					},
				}, nil
			default: // call 3+: cp2
				return &StepActivityResult{
					State: s,
					StepResult: &pathwalk.StepResult{
						WaitCondition: &pathwalk.WaitCondition{
							Mode:   pathwalk.CheckpointModeHumanInput,
							NodeID: "cp2",
						},
					},
				}, nil
			}
		})

	// Both signals are buffered in resumeCh; each Receive call consumes one in order.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ResumeSignalName, ResumeSignal{Value: "first"})
	}, 0)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ResumeSignalName, ResumeSignal{Value: "second"})
	}, 0)

	resumeCallCount := 0
	env.OnActivity((*PathwayActivities).ExecuteResumeStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ ResumeStepActivityInput) (*ResumeStepActivityResult, error) {
			resumeCallCount++
			s := pathwalk.NewState("test")
			if resumeCallCount == 1 {
				// First resume: continue to cp2.
				return &ResumeStepActivityResult{
					State:      s,
					StepResult: &pathwalk.StepResult{NextNodeID: "cp2"},
				}, nil
			}
			// Second resume: done.
			return &ResumeStepActivityResult{
				State:      s,
				StepResult: &pathwalk.StepResult{Done: true, Reason: "terminal", Output: "all done"},
			}, nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON: []byte(twoCheckpointPathwayJSON),
		Task:        "test",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result pathwalk.RunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "terminal", result.Reason)
	require.Equal(t, "all done", result.Output)
	require.Equal(t, 3, stepCallCount, "expected ExecuteStep for n1, cp1, cp2")
	require.Equal(t, 2, resumeCallCount, "expected ExecuteResumeStep for each checkpoint")
}

// TestPathwayWorkflow_Checkpoint_ResumeError verifies that when ExecuteResumeStep
// returns an error the workflow calls the completion callback with Err set and
// returns a workflow error.
func TestPathwayWorkflow_Checkpoint_ResumeError(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()

	env.RegisterActivityWithOptions(completionCallbackStub, activity.RegisterOptions{
		Name: "HandleComplete",
	})

	// Call 1 → route to cp1. Call 2 → suspend at checkpoint.
	stepCallCount := 0
	env.OnActivity((*PathwayActivities).ExecuteStep, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ *PathwayActivities, _ context.Context, _ StepActivityInput) (*StepActivityResult, error) {
			stepCallCount++
			s := pathwalk.NewState("test")
			if stepCallCount == 1 {
				return &StepActivityResult{State: s, StepResult: &pathwalk.StepResult{NextNodeID: "cp1"}}, nil
			}
			return &StepActivityResult{
				State: s,
				StepResult: &pathwalk.StepResult{
					WaitCondition: &pathwalk.WaitCondition{
						Mode:   pathwalk.CheckpointModeHumanInput,
						NodeID: "cp1",
					},
				},
			}, nil
		})

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(ResumeSignalName, ResumeSignal{Value: "Alice"})
	}, 0)

	env.OnActivity((*PathwayActivities).ExecuteResumeStep, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("simulated resume failure"))

	callbackCalled := false
	env.OnActivity("HandleComplete", mock.Anything, mock.Anything).
		Return(func(_ context.Context, in CompletionCallbackInput) error {
			callbackCalled = true
			require.NotEmpty(t, in.Err)
			return nil
		})

	env.ExecuteWorkflow(PathwayWorkflow, PathwayInput{
		PathwayJSON:            []byte(checkpointPathwayJSON),
		Task:                   "test",
		CompletionTaskQueue:    "my-queue",
		CompletionActivityName: "HandleComplete",
		CompletionData:         "run-err",
	})

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.True(t, callbackCalled, "completion callback should be called with error details")
}
