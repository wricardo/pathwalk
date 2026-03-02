package pathwalk

import (
	"fmt"
	"strings"
)

// State holds the mutable runtime state of a pathway execution.
type State struct {
	Task        string
	Variables   map[string]any
	Steps       []Step
	VisitCounts map[string]int // nodeID → number of times visited this run
}

func newState(task string) *State {
	return &State{
		Task:        task,
		Variables:   make(map[string]any),
		VisitCounts: make(map[string]int),
	}
}

// SetVars merges vars into state, skipping nil values.
func (s *State) SetVars(vars map[string]any) {
	for k, v := range vars {
		if v != nil {
			s.Variables[k] = v
		}
	}
}

// VarsSummary returns a human-readable summary of the current variables.
func (s *State) VarsSummary() string {
	if len(s.Variables) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for k, v := range s.Variables {
		fmt.Fprintf(&b, "  %s = %v\n", k, v)
	}
	return b.String()
}

// StepsSummary returns a concise summary of the steps taken so far.
func (s *State) StepsSummary() string {
	if len(s.Steps) == 0 {
		return "(none)"
	}
	var b strings.Builder
	for i, step := range s.Steps {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, step.NodeName, truncate(step.Output, 200))
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
