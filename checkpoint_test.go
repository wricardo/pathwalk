package pathwalk_test

import (
	"context"
	"encoding/json"
	"testing"

	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/pathwaytest"
)

// ---------------------------------------------------------------------------
// Pathway JSON fixtures for checkpoint tests
// ---------------------------------------------------------------------------

// humanInputPathwayJSON: start → checkpoint(human_input) → end
const humanInputPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "Greet the user.",
        "condition": "Exit after greeting."
      }
    },
    {
      "id": "cp1",
      "type": "Checkpoint",
      "data": {
        "name": "Get User Name",
        "checkpointMode": "human_input",
        "checkpointPrompt": "What is your name?",
        "checkpointVariable": "user_name"
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Goodbye!" }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "cp1", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "cp1", "target": "end", "data": { "label": "continue", "description": "" } }
  ]
}`

// humanApprovalPathwayJSON: start → checkpoint(human_approval) → route → approved/rejected → end
const humanApprovalPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Draft",
        "isStart": true,
        "prompt": "Draft an order.",
        "condition": "Exit after drafting."
      }
    },
    {
      "id": "cp1",
      "type": "Checkpoint",
      "data": {
        "name": "Approval Gate",
        "checkpointMode": "human_approval",
        "checkpointPrompt": "Do you approve this order?",
        "checkpointVariable": "approval_status"
      }
    },
    {
      "id": "route-approval",
      "type": "Route",
      "data": {
        "name": "Route Approval",
        "routes": [
          {
            "conditions": [{ "field": "approval_status", "value": "approve", "operator": "is" }],
            "targetNodeId": "approved"
          },
          {
            "conditions": [{ "field": "approval_status", "value": "reject", "operator": "is" }],
            "targetNodeId": "rejected"
          }
        ],
        "fallbackNodeId": "end"
      }
    },
    {
      "id": "approved",
      "type": "Default",
      "data": { "name": "Approved", "prompt": "Process the approved order." }
    },
    {
      "id": "rejected",
      "type": "Default",
      "data": { "name": "Rejected", "prompt": "Handle the rejected order." }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "cp1", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "cp1", "target": "route-approval", "data": { "label": "continue", "description": "" } },
    { "id": "e3", "source": "route-approval", "target": "approved", "data": { "label": "approved", "description": "" } },
    { "id": "e4", "source": "route-approval", "target": "rejected", "data": { "label": "rejected", "description": "" } },
    { "id": "e5", "source": "approved", "target": "end", "data": { "label": "done", "description": "" } },
    { "id": "e6", "source": "rejected", "target": "end", "data": { "label": "done", "description": "" } }
  ]
}`

// llmEvalPathwayJSON: start → checkpoint(llm_eval) → route → pass/fail → end
const llmEvalPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Generate",
        "isStart": true,
        "prompt": "Generate a report.",
        "condition": "Exit after generating."
      }
    },
    {
      "id": "cp1",
      "type": "Checkpoint",
      "data": {
        "name": "Quality Check",
        "checkpointMode": "llm_eval",
        "checkpointPrompt": "Evaluate the report quality.",
        "checkpointCriteria": "The report must contain at least one data point and a conclusion.",
        "checkpointVariable": "quality_result"
      }
    },
    {
      "id": "route-quality",
      "type": "Route",
      "data": {
        "name": "Route Quality",
        "routes": [
          {
            "conditions": [{ "field": "quality_result", "value": "pass", "operator": "is" }],
            "targetNodeId": "pass-node"
          },
          {
            "conditions": [{ "field": "quality_result", "value": "fail", "operator": "is" }],
            "targetNodeId": "fail-node"
          }
        ],
        "fallbackNodeId": "end"
      }
    },
    {
      "id": "pass-node",
      "type": "End Call",
      "data": { "name": "Passed", "text": "Quality check passed." }
    },
    {
      "id": "fail-node",
      "type": "End Call",
      "data": { "name": "Failed", "text": "Quality check failed." }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "cp1", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "cp1", "target": "route-quality", "data": { "label": "continue", "description": "" } },
    { "id": "e3", "source": "route-quality", "target": "pass-node", "data": { "label": "pass", "description": "" } },
    { "id": "e4", "source": "route-quality", "target": "fail-node", "data": { "label": "fail", "description": "" } }
  ]
}`

// autoCheckpointPathwayJSON: start → checkpoint(auto) → route → pass/fail → end
const autoCheckpointPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Collect Data",
        "isStart": true,
        "prompt": "Collect the score.",
        "extractVars": [["score", "integer", "The score value", true]],
        "condition": "Exit after collecting."
      }
    },
    {
      "id": "cp1",
      "type": "Checkpoint",
      "data": {
        "name": "Score Gate",
        "checkpointMode": "auto",
        "checkpointVariable": "gate_result",
        "checkpointConditions": [
          { "field": "score", "operator": ">=", "value": "80" }
        ]
      }
    },
    {
      "id": "route-gate",
      "type": "Route",
      "data": {
        "name": "Route Gate",
        "routes": [
          {
            "conditions": [{ "field": "gate_result", "value": "pass", "operator": "is" }],
            "targetNodeId": "pass-node"
          },
          {
            "conditions": [{ "field": "gate_result", "value": "fail", "operator": "is" }],
            "targetNodeId": "fail-node"
          }
        ],
        "fallbackNodeId": "end"
      }
    },
    {
      "id": "pass-node",
      "type": "End Call",
      "data": { "name": "Passed", "text": "Score gate passed." }
    },
    {
      "id": "fail-node",
      "type": "End Call",
      "data": { "name": "Failed", "text": "Score gate failed." }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "cp1", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "cp1", "target": "route-gate", "data": { "label": "continue", "description": "" } },
    { "id": "e3", "source": "route-gate", "target": "pass-node", "data": { "label": "pass", "description": "" } },
    { "id": "e4", "source": "route-gate", "target": "fail-node", "data": { "label": "fail", "description": "" } }
  ]
}`

// customApprovalOptionsJSON: checkpoint with custom options instead of default approve/reject
const customApprovalOptionsJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "Start.",
        "condition": "Exit."
      }
    },
    {
      "id": "cp1",
      "type": "Checkpoint",
      "data": {
        "name": "Triage",
        "checkpointMode": "human_approval",
        "checkpointPrompt": "How should we proceed?",
        "checkpointVariable": "triage_decision",
        "checkpointOptions": ["escalate", "resolve", "defer"]
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Done." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "cp1", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "cp1", "target": "end", "data": { "label": "continue", "description": "" } }
  ]
}`

// ---------------------------------------------------------------------------
// Helper
// ---------------------------------------------------------------------------

func parseCheckpointPathway(t *testing.T, jsonData string) *pathwalk.Pathway {
	t.Helper()
	pw, err := pathwalk.ParsePathwayBytes([]byte(jsonData))
	if err != nil {
		t.Fatalf("ParsePathwayBytes: %v", err)
	}
	return pw
}

// ---------------------------------------------------------------------------
// Tests: Parsing
// ---------------------------------------------------------------------------

func TestCheckpointParseJSON(t *testing.T) {
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)

	node, ok := pw.NodeByID["cp1"]
	if !ok {
		t.Fatal("checkpoint node cp1 not found")
	}
	if node.Type != pathwalk.NodeTypeCheckpoint {
		t.Errorf("expected NodeTypeCheckpoint, got %q", node.Type)
	}
	if node.CheckpointMode != pathwalk.CheckpointModeHumanInput {
		t.Errorf("expected CheckpointModeHumanInput, got %q", node.CheckpointMode)
	}
	if node.CheckpointPrompt != "What is your name?" {
		t.Errorf("unexpected prompt: %q", node.CheckpointPrompt)
	}
	if node.CheckpointVariable != "user_name" {
		t.Errorf("unexpected variable: %q", node.CheckpointVariable)
	}
}

func TestCheckpointParseLLMEval(t *testing.T) {
	pw := parseCheckpointPathway(t, llmEvalPathwayJSON)

	node := pw.NodeByID["cp1"]
	if node.CheckpointMode != pathwalk.CheckpointModeLLMEval {
		t.Errorf("expected CheckpointModeLLMEval, got %q", node.CheckpointMode)
	}
	if node.CheckpointCriteria != "The report must contain at least one data point and a conclusion." {
		t.Errorf("unexpected criteria: %q", node.CheckpointCriteria)
	}
}

func TestCheckpointParseAuto(t *testing.T) {
	pw := parseCheckpointPathway(t, autoCheckpointPathwayJSON)

	node := pw.NodeByID["cp1"]
	if node.CheckpointMode != pathwalk.CheckpointModeAuto {
		t.Errorf("expected CheckpointModeAuto, got %q", node.CheckpointMode)
	}
	if len(node.CheckpointConditions) != 1 {
		t.Fatalf("expected 1 checkpoint condition, got %d", len(node.CheckpointConditions))
	}
	cond := node.CheckpointConditions[0]
	if cond.Field != "score" || cond.Operator != ">=" || cond.Value != "80" {
		t.Errorf("unexpected condition: %+v", cond)
	}
}

func TestCheckpointParseCustomOptions(t *testing.T) {
	pw := parseCheckpointPathway(t, customApprovalOptionsJSON)

	node := pw.NodeByID["cp1"]
	if len(node.CheckpointOptions) != 3 {
		t.Fatalf("expected 3 options, got %d", len(node.CheckpointOptions))
	}
	expected := []string{"escalate", "resolve", "defer"}
	for i, opt := range node.CheckpointOptions {
		if opt != expected[i] {
			t.Errorf("option[%d]: expected %q, got %q", i, expected[i], opt)
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Human Input checkpoint (suspends)
// ---------------------------------------------------------------------------

func TestCheckpointHumanInput(t *testing.T) {
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Hello!"})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step 1: execute start node
	r1, err := engine.Step(ctx, state, "start")
	if err != nil {
		t.Fatalf("Step start: %v", err)
	}
	if r1.Done {
		t.Fatal("expected not done after start node")
	}
	if r1.NextNodeID != "cp1" {
		t.Fatalf("expected next=cp1, got %q", r1.NextNodeID)
	}

	// Step 2: hit checkpoint — should suspend
	r2, err := engine.Step(ctx, state, "cp1")
	if err != nil {
		t.Fatalf("Step cp1: %v", err)
	}
	if r2.Done {
		t.Fatal("checkpoint should not mark Done=true")
	}
	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition to be non-nil for human_input checkpoint")
	}
	if r2.WaitCondition.Mode != pathwalk.CheckpointModeHumanInput {
		t.Errorf("expected mode human_input, got %q", r2.WaitCondition.Mode)
	}
	if r2.WaitCondition.Prompt != "What is your name?" {
		t.Errorf("unexpected prompt: %q", r2.WaitCondition.Prompt)
	}
	if r2.WaitCondition.NodeID != "cp1" {
		t.Errorf("expected nodeID=cp1, got %q", r2.WaitCondition.NodeID)
	}
	if r2.Reason != "checkpoint" {
		t.Errorf("expected reason=checkpoint, got %q", r2.Reason)
	}

	// Resume with user input
	r3, err := engine.ResumeStep(ctx, state, "cp1", pathwalk.CheckpointResponse{
		Value: "Alice",
	})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}
	// Verify the variable was stored
	if state.Variables["user_name"] != "Alice" {
		t.Errorf("expected user_name=Alice, got %v", state.Variables["user_name"])
	}
	// Should route to end node
	if r3.NextNodeID != "end" {
		t.Errorf("expected next=end, got %q", r3.NextNodeID)
	}
}

// ---------------------------------------------------------------------------
// Tests: Human Approval checkpoint (suspends, then routes)
// ---------------------------------------------------------------------------

func TestCheckpointHumanApproval_Approve(t *testing.T) {
	pw := parseCheckpointPathway(t, humanApprovalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Order drafted."})
	mock.OnNode("approved", pathwaytest.MockResponse{Content: "Order processed."})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step to checkpoint
	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition for human_approval")
	}
	if r2.WaitCondition.Mode != pathwalk.CheckpointModeHumanApproval {
		t.Errorf("expected human_approval, got %q", r2.WaitCondition.Mode)
	}
	// Default options should be approve/reject
	if len(r2.WaitCondition.Options) != 2 || r2.WaitCondition.Options[0] != "approve" || r2.WaitCondition.Options[1] != "reject" {
		t.Errorf("expected default options [approve, reject], got %v", r2.WaitCondition.Options)
	}

	// Resume with "approve"
	r3, err := engine.ResumeStep(ctx, state, "cp1", pathwalk.CheckpointResponse{Value: "approve"})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}
	if state.Variables["approval_status"] != "approve" {
		t.Errorf("expected approval_status=approve, got %v", state.Variables["approval_status"])
	}
	// Should route to route-approval, which then routes to approved
	if r3.NextNodeID != "route-approval" {
		t.Errorf("expected next=route-approval, got %q", r3.NextNodeID)
	}

	// Step through the route node
	r4, _ := engine.Step(ctx, state, r3.NextNodeID)
	if r4.NextNodeID != "approved" {
		t.Errorf("expected next=approved, got %q", r4.NextNodeID)
	}
}

func TestCheckpointHumanApproval_Reject(t *testing.T) {
	pw := parseCheckpointPathway(t, humanApprovalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Order drafted."})
	mock.OnNode("rejected", pathwaytest.MockResponse{Content: "Order cancelled."})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step to checkpoint
	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition")
	}

	// Resume with "reject"
	r3, _ := engine.ResumeStep(ctx, state, "cp1", pathwalk.CheckpointResponse{Value: "reject"})
	if state.Variables["approval_status"] != "reject" {
		t.Errorf("expected approval_status=reject, got %v", state.Variables["approval_status"])
	}

	// Step through route → should go to rejected
	r4, _ := engine.Step(ctx, state, r3.NextNodeID)
	if r4.NextNodeID != "rejected" {
		t.Errorf("expected next=rejected, got %q", r4.NextNodeID)
	}
}

// ---------------------------------------------------------------------------
// Tests: LLM Eval checkpoint (synchronous, does NOT suspend)
// ---------------------------------------------------------------------------

func TestCheckpointLLMEval_Pass(t *testing.T) {
	pw := parseCheckpointPathway(t, llmEvalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Report generated."})
	// The checkpoint_eval tool call should return pass
	mock.OnNodePurpose("cp1", "checkpoint_eval", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "checkpoint_eval", Args: map[string]any{"result": "pass", "reason": "has data and conclusion"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step through start
	r1, _ := engine.Step(ctx, state, "start")

	// Step through checkpoint — should NOT suspend (llm_eval is synchronous)
	r2, err := engine.Step(ctx, state, r1.NextNodeID)
	if err != nil {
		t.Fatalf("Step cp1: %v", err)
	}
	if r2.WaitCondition != nil {
		t.Fatal("llm_eval checkpoint should NOT return a WaitCondition")
	}
	if state.Variables["quality_result"] != "pass" {
		t.Errorf("expected quality_result=pass, got %v", state.Variables["quality_result"])
	}
	if r2.NextNodeID != "route-quality" {
		t.Errorf("expected next=route-quality, got %q", r2.NextNodeID)
	}

	// Step through route → should go to pass-node
	r3, _ := engine.Step(ctx, state, r2.NextNodeID)
	if r3.NextNodeID != "pass-node" {
		t.Errorf("expected next=pass-node, got %q", r3.NextNodeID)
	}
}

func TestCheckpointLLMEval_Fail(t *testing.T) {
	pw := parseCheckpointPathway(t, llmEvalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Report generated."})
	mock.OnNodePurpose("cp1", "checkpoint_eval", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "checkpoint_eval", Args: map[string]any{"result": "fail", "reason": "missing conclusion"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	if state.Variables["quality_result"] != "fail" {
		t.Errorf("expected quality_result=fail, got %v", state.Variables["quality_result"])
	}

	// Step through route → should go to fail-node
	r3, _ := engine.Step(ctx, state, r2.NextNodeID)
	if r3.NextNodeID != "fail-node" {
		t.Errorf("expected next=fail-node, got %q", r3.NextNodeID)
	}
}

// ---------------------------------------------------------------------------
// Tests: Auto checkpoint (synchronous, deterministic)
// ---------------------------------------------------------------------------

func TestCheckpointAuto_Pass(t *testing.T) {
	pw := parseCheckpointPathway(t, autoCheckpointPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Score collected."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"score": 90}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")

	// Auto checkpoint — synchronous, score=90 >= 80 → pass
	r2, err := engine.Step(ctx, state, r1.NextNodeID)
	if err != nil {
		t.Fatalf("Step cp1: %v", err)
	}
	if r2.WaitCondition != nil {
		t.Fatal("auto checkpoint should NOT return a WaitCondition")
	}
	if state.Variables["gate_result"] != "pass" {
		t.Errorf("expected gate_result=pass, got %v", state.Variables["gate_result"])
	}

	// Step through route → should go to pass-node
	r3, _ := engine.Step(ctx, state, r2.NextNodeID)
	if r3.NextNodeID != "pass-node" {
		t.Errorf("expected next=pass-node, got %q", r3.NextNodeID)
	}
}

func TestCheckpointAuto_Fail(t *testing.T) {
	pw := parseCheckpointPathway(t, autoCheckpointPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Score collected."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"score": 50}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")

	// Auto checkpoint — synchronous, score=50 < 80 → fail
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if state.Variables["gate_result"] != "fail" {
		t.Errorf("expected gate_result=fail, got %v", state.Variables["gate_result"])
	}

	// Step through route → should go to fail-node
	r3, _ := engine.Step(ctx, state, r2.NextNodeID)
	if r3.NextNodeID != "fail-node" {
		t.Errorf("expected next=fail-node, got %q", r3.NextNodeID)
	}
}

// ---------------------------------------------------------------------------
// Tests: State serialization across checkpoint suspension
// ---------------------------------------------------------------------------

func TestCheckpointStateSerialization(t *testing.T) {
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Hello!"})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step to checkpoint
	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition")
	}

	// Serialize state to JSON
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	// Deserialize into a new State
	var restored pathwalk.State
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}

	// Verify restored state
	if restored.Task != "test task" {
		t.Errorf("expected task 'test task', got %q", restored.Task)
	}
	if len(restored.Steps) != len(state.Steps) {
		t.Errorf("steps count mismatch: %d vs %d", len(restored.Steps), len(state.Steps))
	}

	// Resume from restored state — should work identically
	r3, err := engine.ResumeStep(ctx, &restored, "cp1", pathwalk.CheckpointResponse{Value: "Bob"})
	if err != nil {
		t.Fatalf("ResumeStep after restore: %v", err)
	}
	if restored.Variables["user_name"] != "Bob" {
		t.Errorf("expected user_name=Bob, got %v", restored.Variables["user_name"])
	}
	if r3.NextNodeID != "end" {
		t.Errorf("expected next=end, got %q", r3.NextNodeID)
	}
}

// ---------------------------------------------------------------------------
// Tests: Run() behavior with checkpoints
// ---------------------------------------------------------------------------

func TestRunWithCheckpoint(t *testing.T) {
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Hello!"})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()

	// Run should stop at checkpoint with reason "checkpoint"
	result, err := engine.Run(ctx, "test task")
	if err == nil {
		t.Fatal("expected error from Run() when hitting a checkpoint")
	}
	if result == nil {
		t.Fatal("expected non-nil RunResult even on checkpoint")
	}
	if result.Reason != "checkpoint" {
		t.Errorf("expected reason=checkpoint, got %q", result.Reason)
	}
}

// ---------------------------------------------------------------------------
// Tests: ResumeStep with extra variables
// ---------------------------------------------------------------------------

func TestCheckpointResumeStepVars(t *testing.T) {
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Hello!"})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step to checkpoint
	r1, _ := engine.Step(ctx, state, "start")
	_, _ = engine.Step(ctx, state, r1.NextNodeID)

	// Resume with extra vars
	_, err := engine.ResumeStep(ctx, state, "cp1", pathwalk.CheckpointResponse{
		Value: "Alice",
		Vars:  map[string]any{"source": "manual", "confidence": 0.95},
	})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}
	if state.Variables["user_name"] != "Alice" {
		t.Errorf("expected user_name=Alice, got %v", state.Variables["user_name"])
	}
	if state.Variables["source"] != "manual" {
		t.Errorf("expected source=manual, got %v", state.Variables["source"])
	}
	if state.Variables["confidence"] != 0.95 {
		t.Errorf("expected confidence=0.95, got %v", state.Variables["confidence"])
	}
}

// ---------------------------------------------------------------------------
// Tests: ResumeStep error cases
// ---------------------------------------------------------------------------

func TestCheckpointResumeStep_WrongNodeType(t *testing.T) {
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Try to resume on a non-checkpoint node
	_, err := engine.ResumeStep(ctx, state, "start", pathwalk.CheckpointResponse{Value: "test"})
	if err == nil {
		t.Fatal("expected error when resuming on non-checkpoint node")
	}
}

func TestCheckpointResumeStep_MissingNode(t *testing.T) {
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Try to resume on a nonexistent node
	_, err := engine.ResumeStep(ctx, state, "nonexistent", pathwalk.CheckpointResponse{Value: "test"})
	if err == nil {
		t.Fatal("expected error when resuming on missing node")
	}
}

// ---------------------------------------------------------------------------
// Tests: Full workflow with checkpoint in the middle
// ---------------------------------------------------------------------------

func TestCheckpointInFullWorkflow(t *testing.T) {
	// Workflow: start(LLM) → checkpoint(human_approval) → route → approved(LLM) → end
	pw := parseCheckpointPathway(t, humanApprovalPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Order: 2x Margherita for John"})
	mock.OnNode("approved", pathwaytest.MockResponse{Content: "Order confirmed and submitted."})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("Create an order for John: 2x Margherita")

	// Phase 1: LLM drafts the order
	r1, _ := engine.Step(ctx, state, "start")
	if r1.Output != "Order: 2x Margherita for John" {
		t.Errorf("unexpected output: %q", r1.Output)
	}

	// Phase 2: Hit checkpoint — suspended
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition at approval gate")
	}

	// Phase 3: Human approves
	r3, _ := engine.ResumeStep(ctx, state, "cp1", pathwalk.CheckpointResponse{Value: "approve"})

	// Phase 4: Route node dispatches based on approval_status
	r4, _ := engine.Step(ctx, state, r3.NextNodeID)
	if r4.NextNodeID != "approved" {
		t.Fatalf("expected next=approved, got %q", r4.NextNodeID)
	}

	// Phase 5: Approved node processes
	r5, _ := engine.Step(ctx, state, r4.NextNodeID)
	if r5.NextNodeID != "end" {
		t.Fatalf("expected next=end, got %q", r5.NextNodeID)
	}

	// Phase 6: Terminal
	r6, _ := engine.Step(ctx, state, r5.NextNodeID)
	if !r6.Done {
		t.Fatal("expected done at terminal")
	}
	if r6.Reason != "terminal" {
		t.Errorf("expected reason=terminal, got %q", r6.Reason)
	}
}

// ---------------------------------------------------------------------------
// Tests: WaitCondition JSON serialization
// ---------------------------------------------------------------------------

func TestWaitConditionJSON(t *testing.T) {
	wc := pathwalk.WaitCondition{
		Mode:         pathwalk.CheckpointModeHumanApproval,
		NodeID:       "cp1",
		NodeName:     "Approval Gate",
		Prompt:       "Do you approve?",
		Options:      []string{"approve", "reject"},
		VariableName: "approval_status",
	}

	data, err := json.Marshal(wc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored pathwalk.WaitCondition
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.Mode != wc.Mode {
		t.Errorf("mode: %q != %q", restored.Mode, wc.Mode)
	}
	if restored.NodeID != wc.NodeID {
		t.Errorf("nodeID: %q != %q", restored.NodeID, wc.NodeID)
	}
	if restored.Prompt != wc.Prompt {
		t.Errorf("prompt: %q != %q", restored.Prompt, wc.Prompt)
	}
	if len(restored.Options) != 2 {
		t.Errorf("expected 2 options, got %d", len(restored.Options))
	}
}

// ---------------------------------------------------------------------------
// Tests: Structured form checkpoint (extractVars on human_input)
// ---------------------------------------------------------------------------

// structuredFormPathwayJSON: start → checkpoint(human_input with extractVars) → end
const structuredFormPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "Greet the user.",
        "condition": "Exit after greeting."
      }
    },
    {
      "id": "cp1",
      "type": "Checkpoint",
      "data": {
        "name": "Schedule Callback",
        "checkpointMode": "human_input",
        "checkpointPrompt": "When should we call you back?",
        "extractVars": [
          ["customer_name", "string", "Customer full name", true],
          ["phone_number", "string", "Phone number to call", true],
          ["callback_time", "datetime", "Preferred callback date and time", true],
          ["item_count", "integer", "Number of items to discuss", false],
          ["is_urgent", "boolean", "Is this urgent?", false]
        ]
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Callback scheduled." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "cp1", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "cp1", "target": "end", "data": { "label": "continue", "description": "" } }
  ]
}`

func TestCheckpointStructuredForm(t *testing.T) {
	pw := parseCheckpointPathway(t, structuredFormPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Hello!"})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step to checkpoint
	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition")
	}

	wc := r2.WaitCondition
	if wc.Mode != pathwalk.CheckpointModeHumanInput {
		t.Errorf("expected human_input, got %q", wc.Mode)
	}
	if wc.Prompt != "When should we call you back?" {
		t.Errorf("unexpected prompt: %q", wc.Prompt)
	}

	// Verify Variables are populated from extractVars
	if len(wc.Variables) != 5 {
		t.Fatalf("expected 5 variables, got %d", len(wc.Variables))
	}

	// Check each variable definition
	expected := []struct {
		Name     string
		Type     string
		Required bool
	}{
		{"customer_name", "string", true},
		{"phone_number", "string", true},
		{"callback_time", "datetime", true},
		{"item_count", "integer", false},
		{"is_urgent", "boolean", false},
	}
	for i, exp := range expected {
		v := wc.Variables[i]
		if v.Name != exp.Name {
			t.Errorf("var[%d] name: expected %q, got %q", i, exp.Name, v.Name)
		}
		if v.Type != exp.Type {
			t.Errorf("var[%d] type: expected %q, got %q", i, exp.Type, v.Type)
		}
		if v.Required != exp.Required {
			t.Errorf("var[%d] required: expected %v, got %v", i, exp.Required, v.Required)
		}
	}

	// Resume with structured form data via Vars
	_, err := engine.ResumeStep(ctx, state, "cp1", pathwalk.CheckpointResponse{
		Vars: map[string]any{
			"customer_name": "John Smith",
			"phone_number":  "+15551234567",
			"callback_time": "2026-04-01T14:00:00Z",
			"item_count":    3,
			"is_urgent":     true,
		},
	})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}

	// Verify all variables were stored in state
	if state.Variables["customer_name"] != "John Smith" {
		t.Errorf("customer_name: %v", state.Variables["customer_name"])
	}
	if state.Variables["phone_number"] != "+15551234567" {
		t.Errorf("phone_number: %v", state.Variables["phone_number"])
	}
	if state.Variables["callback_time"] != "2026-04-01T14:00:00Z" {
		t.Errorf("callback_time: %v", state.Variables["callback_time"])
	}
	if state.Variables["item_count"] != 3 {
		t.Errorf("item_count: %v", state.Variables["item_count"])
	}
	if state.Variables["is_urgent"] != true {
		t.Errorf("is_urgent: %v", state.Variables["is_urgent"])
	}
}

func TestCheckpointStructuredFormNoVars(t *testing.T) {
	// When a human_input checkpoint has no extractVars, Variables should be nil/empty
	pw := parseCheckpointPathway(t, humanInputPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Hello!"})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition")
	}
	// No extractVars on the original human_input pathway → Variables should be empty
	if len(r2.WaitCondition.Variables) != 0 {
		t.Errorf("expected 0 variables, got %d", len(r2.WaitCondition.Variables))
	}
	// VariableName should still be set for the simple single-value case
	if r2.WaitCondition.VariableName != "user_name" {
		t.Errorf("expected variable_name=user_name, got %q", r2.WaitCondition.VariableName)
	}
}

func TestCheckpointStructuredFormSerialization(t *testing.T) {
	wc := pathwalk.WaitCondition{
		Mode:     pathwalk.CheckpointModeHumanInput,
		NodeID:   "cp1",
		NodeName: "Schedule Callback",
		Prompt:   "When should we call you back?",
		Variables: []pathwalk.VariableDef{
			{Name: "callback_time", Type: "datetime", Description: "Preferred time", Required: true},
			{Name: "notes", Type: "string", Description: "Notes", Required: false},
		},
	}

	data, err := json.Marshal(wc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored pathwalk.WaitCondition
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(restored.Variables) != 2 {
		t.Fatalf("expected 2 variables, got %d", len(restored.Variables))
	}
	if restored.Variables[0].Name != "callback_time" {
		t.Errorf("var[0] name: %q", restored.Variables[0].Name)
	}
	if restored.Variables[0].Type != "datetime" {
		t.Errorf("var[0] type: %q", restored.Variables[0].Type)
	}
	if !restored.Variables[0].Required {
		t.Error("var[0] should be required")
	}
}

// ---------------------------------------------------------------------------
// Tests: Wait mode checkpoint (duration-based and event-based)
// ---------------------------------------------------------------------------

// waitDurationPathwayJSON: start → checkpoint(wait, 24h) → end
const waitDurationPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Schedule",
        "isStart": true,
        "prompt": "Schedule a follow-up.",
        "condition": "Exit after scheduling."
      }
    },
    {
      "id": "wait1",
      "type": "Checkpoint",
      "data": {
        "name": "Wait 24h",
        "checkpointMode": "wait",
        "checkpointPrompt": "Waiting 24 hours before follow-up.",
        "checkpointVariable": "wait_result",
        "waitDuration": "24h"
      }
    },
    {
      "id": "followup",
      "type": "Default",
      "data": {
        "name": "Follow Up",
        "prompt": "Perform the follow-up action.",
        "condition": "Exit when done."
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Follow-up complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "wait1", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "wait1", "target": "followup", "data": { "label": "continue", "description": "" } },
    { "id": "e3", "source": "followup", "target": "end", "data": { "label": "done", "description": "" } }
  ]
}`

// waitEventPathwayJSON: start → checkpoint(wait, no duration — event-driven) → end
const waitEventPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "Initiate process.",
        "condition": "Exit after initiating."
      }
    },
    {
      "id": "wait-event",
      "type": "Checkpoint",
      "data": {
        "name": "Wait for Webhook",
        "checkpointMode": "wait",
        "checkpointPrompt": "Waiting for external webhook confirmation.",
        "checkpointVariable": "webhook_payload"
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Process complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "wait-event", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "wait-event", "target": "end", "data": { "label": "continue", "description": "" } }
  ]
}`

func TestCheckpointWaitDuration(t *testing.T) {
	pw := parseCheckpointPathway(t, waitDurationPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Scheduled."})
	mock.OnNode("followup", pathwaytest.MockResponse{Content: "Followed up."})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step through start
	r1, _ := engine.Step(ctx, state, "start")
	if r1.NextNodeID != "wait1" {
		t.Fatalf("expected next=wait1, got %q", r1.NextNodeID)
	}

	// Step hits wait checkpoint — suspends
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition for wait mode")
	}
	if r2.WaitCondition.Mode != pathwalk.CheckpointModeWait {
		t.Errorf("expected mode=wait, got %q", r2.WaitCondition.Mode)
	}
	if r2.WaitCondition.WaitDuration != "24h" {
		t.Errorf("expected duration=24h, got %q", r2.WaitCondition.WaitDuration)
	}
	if r2.WaitCondition.Prompt != "Waiting 24 hours before follow-up." {
		t.Errorf("unexpected prompt: %q", r2.WaitCondition.Prompt)
	}
	if r2.Reason != "checkpoint" {
		t.Errorf("expected reason=checkpoint, got %q", r2.Reason)
	}

	// Caller sleeps 24h... then resumes
	r3, err := engine.ResumeStep(ctx, state, "wait1", pathwalk.CheckpointResponse{
		Value: "timer_expired",
	})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}
	if state.Variables["wait_result"] != "timer_expired" {
		t.Errorf("expected wait_result=timer_expired, got %v", state.Variables["wait_result"])
	}
	if r3.NextNodeID != "followup" {
		t.Errorf("expected next=followup, got %q", r3.NextNodeID)
	}

	// Continue to follow-up and terminal
	r4, _ := engine.Step(ctx, state, r3.NextNodeID)
	if r4.NextNodeID != "end" {
		t.Errorf("expected next=end, got %q", r4.NextNodeID)
	}
}

func TestCheckpointWaitEvent(t *testing.T) {
	pw := parseCheckpointPathway(t, waitEventPathwayJSON)
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Initiated."})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	// Step through start
	r1, _ := engine.Step(ctx, state, "start")

	// Step hits event wait — suspends with no duration
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition")
	}
	if r2.WaitCondition.Mode != pathwalk.CheckpointModeWait {
		t.Errorf("expected mode=wait, got %q", r2.WaitCondition.Mode)
	}
	if r2.WaitCondition.WaitDuration != "" {
		t.Errorf("expected empty duration for event wait, got %q", r2.WaitCondition.WaitDuration)
	}

	// External event fires — resume with payload data
	r3, err := engine.ResumeStep(ctx, state, "wait-event", pathwalk.CheckpointResponse{
		Value: "confirmed",
		Vars:  map[string]any{"webhook_status": "200", "confirmation_id": "abc123"},
	})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}
	if state.Variables["webhook_payload"] != "confirmed" {
		t.Errorf("expected webhook_payload=confirmed, got %v", state.Variables["webhook_payload"])
	}
	if state.Variables["confirmation_id"] != "abc123" {
		t.Errorf("expected confirmation_id=abc123, got %v", state.Variables["confirmation_id"])
	}
	if r3.NextNodeID != "end" {
		t.Errorf("expected next=end, got %q", r3.NextNodeID)
	}
}

func TestCheckpointWaitParseJSON(t *testing.T) {
	pw := parseCheckpointPathway(t, waitDurationPathwayJSON)
	node := pw.NodeByID["wait1"]
	if node.Type != pathwalk.NodeTypeCheckpoint {
		t.Errorf("expected checkpoint, got %q", node.Type)
	}
	if node.CheckpointMode != pathwalk.CheckpointModeWait {
		t.Errorf("expected wait mode, got %q", node.CheckpointMode)
	}
	if node.WaitDuration != "24h" {
		t.Errorf("expected 24h, got %q", node.WaitDuration)
	}
}

func TestCheckpointResponseJSON(t *testing.T) {
	cr := pathwalk.CheckpointResponse{
		Value: "approve",
		Vars:  map[string]any{"notes": "looks good"},
	}

	data, err := json.Marshal(cr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var restored pathwalk.CheckpointResponse
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if restored.Value != "approve" {
		t.Errorf("value: %q", restored.Value)
	}
	if restored.Vars["notes"] != "looks good" {
		t.Errorf("vars: %v", restored.Vars)
	}
}
