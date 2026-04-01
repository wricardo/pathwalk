package pathwalk_test

import (
	"context"
	"encoding/json"
	"testing"

	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/pathwaytest"
)

// ---------------------------------------------------------------------------
// Pathway JSON fixtures
// ---------------------------------------------------------------------------

const agentNodePathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Plan",
        "isStart": true,
        "prompt": "Plan the research topic.",
        "condition": "Exit after planning.",
        "extractVars": [["topic", "string", "Research topic", true]]
      }
    },
    {
      "id": "research",
      "type": "Agent",
      "data": {
        "name": "Research Agent",
        "agentId": "agent-researcher-123",
        "task": "Research {{topic}} and provide a summary.",
        "outputVar": "research_summary"
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Research complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "research", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "research", "target": "end", "data": { "label": "done", "description": "" } }
  ]
}`

const teamParallelPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Prepare",
        "isStart": true,
        "prompt": "Prepare the code for review.",
        "condition": "Exit after preparing.",
        "extractVars": [["code_snippet", "string", "Code to review", true]]
      }
    },
    {
      "id": "review-team",
      "type": "Team",
      "data": {
        "name": "Review Team",
        "strategy": "parallel",
        "agents": [
          {
            "name": "Bug Reviewer",
            "agentId": "agent-bugs-001",
            "task": "Review this code for bugs: {{code_snippet}}",
            "outputVar": "bug_report"
          },
          {
            "name": "Security Reviewer",
            "agentId": "agent-security-002",
            "task": "Review this code for security issues: {{code_snippet}}",
            "outputVar": "security_report"
          },
          {
            "name": "Perf Reviewer",
            "agentId": "agent-perf-003",
            "task": "Review this code for performance: {{code_snippet}}",
            "outputVar": "perf_report"
          }
        ]
      }
    },
    {
      "id": "synthesize",
      "type": "Default",
      "data": {
        "name": "Synthesize",
        "prompt": "Synthesize: {{bug_report}} {{security_report}} {{perf_report}}",
        "condition": "Exit after synthesis."
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Review complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "review-team", "data": { "label": "continue", "description": "" } },
    { "id": "e2", "source": "review-team", "target": "synthesize", "data": { "label": "continue", "description": "" } },
    { "id": "e3", "source": "synthesize", "target": "end", "data": { "label": "done", "description": "" } }
  ]
}`

const teamRacePathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "Start.",
        "condition": "Exit.",
        "extractVars": [["query", "string", "Search query", true]]
      }
    },
    {
      "id": "race",
      "type": "Team",
      "data": {
        "name": "Search Race",
        "strategy": "race",
        "agents": [
          { "name": "Search A", "agentId": "search-a", "task": "Search {{query}} via API A", "outputVar": "search_result" },
          { "name": "Search B", "agentId": "search-b", "task": "Search {{query}} via API B", "outputVar": "search_result" }
        ]
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Done." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "race", "data": { "label": "next", "description": "" } },
    { "id": "e2", "source": "race", "target": "end", "data": { "label": "done", "description": "" } }
  ]
}`

const teamSequencePathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Start",
        "isStart": true,
        "prompt": "Start.",
        "condition": "Exit.",
        "extractVars": [["raw_data", "string", "Raw data", true]]
      }
    },
    {
      "id": "pipeline",
      "type": "Team",
      "data": {
        "name": "ETL Pipeline",
        "strategy": "sequence",
        "agents": [
          { "name": "Extract", "agentId": "extractor", "task": "Extract from {{raw_data}}", "outputVar": "extracted" },
          { "name": "Transform", "agentId": "transformer", "task": "Transform {{extracted}}", "outputVar": "transformed" },
          { "name": "Load", "agentId": "loader", "task": "Load {{transformed}}", "outputVar": "loaded" }
        ]
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Pipeline complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "pipeline", "data": { "label": "next", "description": "" } },
    { "id": "e2", "source": "pipeline", "target": "end", "data": { "label": "done", "description": "" } }
  ]
}`

// ---------------------------------------------------------------------------
// Tests: Parsing
// ---------------------------------------------------------------------------

func TestAgentNodeParse(t *testing.T) {
	pw, err := pathwalk.ParsePathwayBytes([]byte(agentNodePathwayJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	node := pw.NodeByID["research"]
	if node.Type != pathwalk.NodeTypeAgent {
		t.Errorf("expected NodeTypeAgent, got %q", node.Type)
	}
	if node.AgentID != "agent-researcher-123" {
		t.Errorf("agentId: %q", node.AgentID)
	}
	if node.AgentTask != "Research {{topic}} and provide a summary." {
		t.Errorf("task: %q", node.AgentTask)
	}
	if node.AgentOutputVar != "research_summary" {
		t.Errorf("outputVar: %q", node.AgentOutputVar)
	}
}

func TestTeamNodeParse(t *testing.T) {
	pw, err := pathwalk.ParsePathwayBytes([]byte(teamParallelPathwayJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	node := pw.NodeByID["review-team"]
	if node.Type != pathwalk.NodeTypeTeam {
		t.Errorf("expected NodeTypeTeam, got %q", node.Type)
	}
	if node.TeamStrategy != "parallel" {
		t.Errorf("strategy: %q", node.TeamStrategy)
	}
	if len(node.TeamAgents) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(node.TeamAgents))
	}
	a := node.TeamAgents[0]
	if a.Name != "Bug Reviewer" || a.AgentID != "agent-bugs-001" || a.OutputVar != "bug_report" {
		t.Errorf("agent[0]: %+v", a)
	}
}

func TestTeamStrategyParse(t *testing.T) {
	tests := []struct {
		json     string
		strategy string
	}{
		{teamParallelPathwayJSON, "parallel"},
		{teamRacePathwayJSON, "race"},
		{teamSequencePathwayJSON, "sequence"},
	}
	for _, tt := range tests {
		pw, err := pathwalk.ParsePathwayBytes([]byte(tt.json))
		if err != nil {
			t.Fatalf("parse %s: %v", tt.strategy, err)
		}
		for _, n := range pw.Nodes {
			if n.Type == pathwalk.NodeTypeTeam {
				if n.TeamStrategy != tt.strategy {
					t.Errorf("expected strategy %q, got %q", tt.strategy, n.TeamStrategy)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Tests: Agent node suspend/resume
// ---------------------------------------------------------------------------

func TestAgentNodeSuspend(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(agentNodePathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Planning done."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"topic": "quantum computing"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")

	// Agent node suspends
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition for agent node")
	}
	if r2.WaitCondition.Mode != pathwalk.CheckpointModeAgent {
		t.Errorf("expected mode=agent, got %q", r2.WaitCondition.Mode)
	}
	if r2.Reason != "checkpoint" {
		t.Errorf("expected reason=checkpoint, got %q", r2.Reason)
	}

	at := r2.WaitCondition.AgentTask
	if at == nil {
		t.Fatal("expected AgentTask")
	}
	if at.AgentID != "agent-researcher-123" {
		t.Errorf("agentId: %q", at.AgentID)
	}
	// Task template should be resolved with state variables
	if at.Task != "Research quantum computing and provide a summary." {
		t.Errorf("task not resolved: %q", at.Task)
	}
	if at.OutputVar != "research_summary" {
		t.Errorf("outputVar: %q", at.OutputVar)
	}
}

func TestAgentNodeResume(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(agentNodePathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Done."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"topic": "AI"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")
	_, _ = engine.Step(ctx, state, r1.NextNodeID)

	// Resume with child agent output
	r3, err := engine.ResumeStep(ctx, state, "research", pathwalk.CheckpointResponse{
		Vars: map[string]any{"research_summary": "AI is transforming industries."},
	})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}
	if state.Variables["research_summary"] != "AI is transforming industries." {
		t.Errorf("research_summary: %v", state.Variables["research_summary"])
	}
	if r3.NextNodeID != "end" {
		t.Errorf("expected next=end, got %q", r3.NextNodeID)
	}
}

// ---------------------------------------------------------------------------
// Tests: Team node suspend/resume
// ---------------------------------------------------------------------------

func TestTeamNodeSuspend(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(teamParallelPathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Code prepared."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"code_snippet": "func foo() {}"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition")
	}
	if r2.WaitCondition.Mode != pathwalk.CheckpointModeTeam {
		t.Errorf("expected mode=team, got %q", r2.WaitCondition.Mode)
	}
	if r2.WaitCondition.TeamStrategy != "parallel" {
		t.Errorf("expected strategy=parallel, got %q", r2.WaitCondition.TeamStrategy)
	}
	if len(r2.WaitCondition.TeamTasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(r2.WaitCondition.TeamTasks))
	}

	// Verify template resolution
	bug := r2.WaitCondition.TeamTasks[0]
	if bug.Task != "Review this code for bugs: func foo() {}" {
		t.Errorf("task not resolved: %q", bug.Task)
	}
}

func TestTeamNodeResume(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(teamParallelPathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Done."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"code_snippet": "x := 1"}},
		},
	})
	mock.OnNode("synthesize", pathwaytest.MockResponse{Content: "All clear."})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")
	_, _ = engine.Step(ctx, state, r1.NextNodeID)

	// Resume with all child outputs
	r3, err := engine.ResumeStep(ctx, state, "review-team", pathwalk.CheckpointResponse{
		Vars: map[string]any{
			"bug_report":      "No bugs found.",
			"security_report": "No security issues.",
			"perf_report":     "Performance is acceptable.",
		},
	})
	if err != nil {
		t.Fatalf("ResumeStep: %v", err)
	}
	if state.Variables["bug_report"] != "No bugs found." {
		t.Errorf("bug_report: %v", state.Variables["bug_report"])
	}
	if state.Variables["security_report"] != "No security issues." {
		t.Errorf("security_report: %v", state.Variables["security_report"])
	}
	if state.Variables["perf_report"] != "Performance is acceptable." {
		t.Errorf("perf_report: %v", state.Variables["perf_report"])
	}
	if r3.NextNodeID != "synthesize" {
		t.Errorf("expected next=synthesize, got %q", r3.NextNodeID)
	}
}

// ---------------------------------------------------------------------------
// Tests: Race and Sequence strategies parse correctly
// ---------------------------------------------------------------------------

func TestTeamRaceSuspend(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(teamRacePathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Done."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"query": "golang concurrency"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test")

	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	if r2.WaitCondition == nil {
		t.Fatal("expected WaitCondition")
	}
	if r2.WaitCondition.TeamStrategy != "race" {
		t.Errorf("expected strategy=race, got %q", r2.WaitCondition.TeamStrategy)
	}
	if len(r2.WaitCondition.TeamTasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(r2.WaitCondition.TeamTasks))
	}
}

// ---------------------------------------------------------------------------
// Tests: Serialization
// ---------------------------------------------------------------------------

func TestAgentTaskSerialization(t *testing.T) {
	wc := pathwalk.WaitCondition{
		Mode:   pathwalk.CheckpointModeAgent,
		NodeID: "research",
		AgentTask: &pathwalk.AgentTask{
			Name:      "Researcher",
			AgentID:   "agent-123",
			Task:      "Research AI",
			OutputVar: "result",
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
	if restored.AgentTask == nil || restored.AgentTask.AgentID != "agent-123" {
		t.Errorf("agent task not restored: %+v", restored.AgentTask)
	}
}

func TestTeamTasksSerialization(t *testing.T) {
	wc := pathwalk.WaitCondition{
		Mode:         pathwalk.CheckpointModeTeam,
		NodeID:       "team",
		TeamStrategy: "parallel",
		TeamTasks: []pathwalk.AgentTask{
			{Name: "A", AgentID: "a1", Task: "do A", OutputVar: "out_a"},
			{Name: "B", AgentID: "b1", Task: "do B", OutputVar: "out_b"},
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
	if restored.TeamStrategy != "parallel" {
		t.Errorf("strategy: %q", restored.TeamStrategy)
	}
	if len(restored.TeamTasks) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(restored.TeamTasks))
	}
}

// ---------------------------------------------------------------------------
// Tests: Run() with agent/team nodes
// ---------------------------------------------------------------------------

func TestRunWithAgentNode(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(agentNodePathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Done."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"topic": "test"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	result, err := engine.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error from Run() hitting agent node")
	}
	if result.Reason != "checkpoint" {
		t.Errorf("expected reason=checkpoint, got %q", result.Reason)
	}
}

// ---------------------------------------------------------------------------
// Tests: Full workflow — agent + team + checkpoint in one pathway
// ---------------------------------------------------------------------------

const fullOrchestrationPathwayJSON = `{
  "nodes": [
    {
      "id": "start",
      "type": "Default",
      "data": {
        "name": "Plan",
        "isStart": true,
        "prompt": "Plan the work.",
        "condition": "Exit.",
        "extractVars": [["task_desc", "string", "Task description", true]]
      }
    },
    {
      "id": "research",
      "type": "Agent",
      "data": {
        "name": "Researcher",
        "agentId": "agent-research",
        "task": "Research: {{task_desc}}",
        "outputVar": "research"
      }
    },
    {
      "id": "approval",
      "type": "Checkpoint",
      "data": {
        "name": "Approve Research",
        "checkpointMode": "human_approval",
        "checkpointPrompt": "Research complete. Approve to proceed with implementation?",
        "checkpointVariable": "approved"
      }
    },
    {
      "id": "route",
      "type": "Route",
      "data": {
        "name": "Route",
        "routes": [
          { "conditions": [{"field": "approved", "value": "approve", "operator": "is"}], "targetNodeId": "impl-team" }
        ],
        "fallbackNodeId": "end"
      }
    },
    {
      "id": "impl-team",
      "type": "Team",
      "data": {
        "name": "Implementation Team",
        "strategy": "parallel",
        "agents": [
          { "name": "Coder", "agentId": "agent-code", "task": "Implement: {{research}}", "outputVar": "code" },
          { "name": "Tester", "agentId": "agent-test", "task": "Write tests for: {{research}}", "outputVar": "tests" }
        ]
      }
    },
    {
      "id": "end",
      "type": "End Call",
      "data": { "name": "Done", "text": "Complete." }
    }
  ],
  "edges": [
    { "id": "e1", "source": "start", "target": "research", "data": { "label": "next", "description": "" } },
    { "id": "e2", "source": "research", "target": "approval", "data": { "label": "next", "description": "" } },
    { "id": "e3", "source": "approval", "target": "route", "data": { "label": "next", "description": "" } },
    { "id": "e4", "source": "route", "target": "impl-team", "data": { "label": "approved", "description": "" } },
    { "id": "e5", "source": "impl-team", "target": "end", "data": { "label": "done", "description": "" } }
  ]
}`

func TestStepLogCompleteness(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(agentNodePathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Done."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"topic": "testing"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test task")

	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	// Suspend step should have descriptive output
	suspendStep := r2.Step
	if suspendStep.Output == "" {
		t.Error("suspend step should have non-empty Output")
	}
	if suspendStep.NodeName != "Research Agent" {
		t.Errorf("suspend step node name: %q", suspendStep.NodeName)
	}

	// Resume with child traces
	childSteps := []pathwalk.Step{
		{NodeID: "child-n1", NodeName: "Child Start", Output: "Starting research..."},
		{NodeID: "child-n2", NodeName: "Child End", Output: "Research complete."},
	}
	r3, _ := engine.ResumeStep(ctx, state, "research", pathwalk.CheckpointResponse{
		Vars: map[string]any{"research_summary": "Findings here."},
		ChildRuns: []pathwalk.ChildRun{
			{
				Name:    "Research Agent",
				AgentID: "agent-researcher-123",
				Output:  "Findings here.",
				Steps:   childSteps,
			},
		},
	})

	// Resume step should have ResumeValue (empty here since we used Vars) and ChildRuns
	resumeStep := r3.Step
	if len(resumeStep.ChildRuns) != 1 {
		t.Fatalf("expected 1 ChildRun, got %d", len(resumeStep.ChildRuns))
	}
	if resumeStep.ChildRuns[0].Name != "Research Agent" {
		t.Errorf("child run name: %q", resumeStep.ChildRuns[0].Name)
	}
	if len(resumeStep.ChildRuns[0].Steps) != 2 {
		t.Errorf("expected 2 child steps, got %d", len(resumeStep.ChildRuns[0].Steps))
	}
	if resumeStep.ChildRuns[0].Output != "Findings here." {
		t.Errorf("child output: %q", resumeStep.ChildRuns[0].Output)
	}

	// Vars on resume step should be just the response vars, not entire state
	if resumeStep.Vars["research_summary"] != "Findings here." {
		t.Errorf("resume step vars should contain response vars: %v", resumeStep.Vars)
	}

	// Full state should be complete
	if len(state.Steps) < 3 {
		t.Errorf("expected at least 3 steps in state, got %d", len(state.Steps))
	}
}

func TestCheckpointResumeLogsValue(t *testing.T) {
	pw, _ := pathwalk.ParsePathwayBytes([]byte(humanApprovalPathwayJSON))
	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Drafted."})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("test")

	r1, _ := engine.Step(ctx, state, "start")
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)

	// Suspend step should describe the checkpoint
	if r2.Step.Output == "" {
		t.Error("checkpoint suspend step should have output describing the checkpoint")
	}

	r3, _ := engine.ResumeStep(ctx, state, "cp1", pathwalk.CheckpointResponse{Value: "approve"})

	// Resume step should record what was submitted
	if r3.Step.ResumeValue != "approve" {
		t.Errorf("expected ResumeValue=approve, got %q", r3.Step.ResumeValue)
	}
}

func TestFullOrchestration(t *testing.T) {
	pw, err := pathwalk.ParsePathwayBytes([]byte(fullOrchestrationPathwayJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	mock := pathwaytest.NewMockLLMClient()
	mock.OnNode("start", pathwaytest.MockResponse{Content: "Planned."})
	mock.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
		ToolCalls: []pathwaytest.MockToolCall{
			{Name: "set_variables", Args: map[string]any{"task_desc": "build a REST API"}},
		},
	})

	engine := pathwalk.NewEngine(pw, mock)
	ctx := context.Background()
	state := pathwalk.NewState("build a REST API")

	// Phase 1: Plan
	r1, _ := engine.Step(ctx, state, "start")

	// Phase 2: Agent node suspends for research
	r2, _ := engine.Step(ctx, state, r1.NextNodeID)
	if r2.WaitCondition == nil || r2.WaitCondition.Mode != pathwalk.CheckpointModeAgent {
		t.Fatal("expected agent suspension")
	}

	// Resume with research result
	r3, _ := engine.ResumeStep(ctx, state, "research", pathwalk.CheckpointResponse{
		Vars: map[string]any{"research": "REST API best practices: use proper status codes..."},
	})

	// Phase 3: Human approval checkpoint
	r4, _ := engine.Step(ctx, state, r3.NextNodeID)
	if r4.WaitCondition == nil || r4.WaitCondition.Mode != pathwalk.CheckpointModeHumanApproval {
		t.Fatal("expected human approval suspension")
	}

	// Approve
	r5, _ := engine.ResumeStep(ctx, state, "approval", pathwalk.CheckpointResponse{Value: "approve"})

	// Phase 4: Route dispatches to impl-team
	r6, _ := engine.Step(ctx, state, r5.NextNodeID)
	if r6.NextNodeID != "impl-team" {
		t.Fatalf("expected next=impl-team, got %q", r6.NextNodeID)
	}

	// Phase 5: Team node suspends for parallel work
	r7, _ := engine.Step(ctx, state, r6.NextNodeID)
	if r7.WaitCondition == nil || r7.WaitCondition.Mode != pathwalk.CheckpointModeTeam {
		t.Fatal("expected team suspension")
	}
	if len(r7.WaitCondition.TeamTasks) != 2 {
		t.Fatalf("expected 2 team tasks, got %d", len(r7.WaitCondition.TeamTasks))
	}

	// Resume with both agent outputs
	r8, _ := engine.ResumeStep(ctx, state, "impl-team", pathwalk.CheckpointResponse{
		Vars: map[string]any{
			"code":  "package main\nfunc main() { ... }",
			"tests": "func TestMain(t *testing.T) { ... }",
		},
	})

	// Phase 6: Terminal
	r9, _ := engine.Step(ctx, state, r8.NextNodeID)
	if !r9.Done || r9.Reason != "terminal" {
		t.Fatalf("expected terminal, got done=%v reason=%q", r9.Done, r9.Reason)
	}

	// Verify all state accumulated correctly
	if state.Variables["task_desc"] != "build a REST API" {
		t.Errorf("task_desc: %v", state.Variables["task_desc"])
	}
	if state.Variables["research"] == nil {
		t.Error("research not in state")
	}
	if state.Variables["code"] == nil {
		t.Error("code not in state")
	}
	if state.Variables["tests"] == nil {
		t.Error("tests not in state")
	}
}
