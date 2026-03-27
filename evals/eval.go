// Package evals provides a lightweight evaluation framework for pathwalk
// pathways. It runs a set of scripted Cases against a Pathway and scores each
// one against declared Expectations, producing a Report you can track over time.
//
// Quick start:
//
//	cases := []evals.Case{
//	    {
//	        ID:   "order_happy_path",
//	        Task: "Create an order for John",
//	        SetupMock: func(m *pathwaytest.MockLLMClient) {
//	            m.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
//	                ToolCalls: []pathwaytest.MockToolCall{
//	                    {Name: "set_variables", Args: map[string]any{"operation_type": "order_mgmt"}},
//	                },
//	            })
//	            m.SetDefault(pathwaytest.MockResponse{Content: "done"})
//	        },
//	        Expect: evals.Expectation{
//	            TerminalNode:  "end",
//	            VisitedNodes:  []string{"order-mgmt"},
//	            Variables:     map[string]string{"operation_type": "order_mgmt"},
//	        },
//	    },
//	}
//	report := evals.Run(pathway, cases)
//	report.Print(os.Stdout)
package evals

import (
	"context"
	"fmt"
	"io"
	"strings"

	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/pathwaytest"
)

// Case defines a single eval scenario.
type Case struct {
	// ID is a short identifier used in reports and test names.
	ID string
	// Task is the input string passed to engine.Run.
	Task string
	// SetupMock configures mock responses before the run. If nil, the mock
	// returns empty responses for every call.
	SetupMock func(*pathwaytest.MockLLMClient)
	// Expect describes what the pathway should produce.
	Expect Expectation
}

// Expectation describes what a correct run should look like.
type Expectation struct {
	// TerminalNode is the ID of the node that should appear last in Steps.
	// Leave empty to skip this check.
	TerminalNode string
	// Variables is a subset of state variables that must match exactly.
	// Leave nil to skip variable checks.
	Variables map[string]string
	// VisitedNodes lists node IDs that must appear somewhere in Steps, in any
	// order. Leave nil to skip.
	VisitedNodes []string
	// Reason is the expected RunResult.Reason. Defaults to "terminal".
	Reason string
}

// Check is a single pass/fail assertion within a [Result].
type Check struct {
	// Name identifies the assertion, e.g. "reason", "terminal_node",
	// "var:operation_type", or "visited:order-mgmt".
	Name string
	// Passed reports whether the assertion succeeded.
	Passed bool
	// Got is the actual value observed during the run.
	Got string
	// Want is the expected value declared in the [Expectation].
	Want string
}

// Result is the outcome of running a single [Case].
type Result struct {
	// Case is the eval scenario that produced this result.
	Case Case
	// Passed is true when every Check passed and Error is nil.
	Passed bool
	// Checks holds the individual assertions evaluated for this case.
	Checks []Check
	// Error is non-nil when the engine itself returned an error during the run.
	Error error
}

// Report is the aggregate output of a full eval run.
type Report struct {
	// Results holds one entry per Case, in the same order as the input slice.
	Results []Result
	// Passed is the number of cases where Result.Passed is true.
	Passed int
	// Failed is the number of cases where Result.Passed is false.
	Failed int
}

// PassRate returns the fraction of cases that passed (0.0–1.0).
func (r Report) PassRate() float64 {
	total := r.Passed + r.Failed
	if total == 0 {
		return 0
	}
	return float64(r.Passed) / float64(total)
}

// Run executes every Case against pathway and returns a [Report].
// Each case receives its own fresh engine and [pathwaytest.MockLLMClient], so
// cases do not share state. The pathway is read-only and is not modified.
func Run(pathway *pathwalk.Pathway, cases []Case) Report {
	var report Report
	for _, c := range cases {
		res := runCase(pathway, c)
		report.Results = append(report.Results, res)
		if res.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
	}
	return report
}

// Print writes a human-readable summary of the report to w. Each case is
// listed with a PASS or FAIL prefix; for failing cases the individual checks
// that did not pass are shown with their got/want values.
func (r Report) Print(w io.Writer) {
	total := r.Passed + r.Failed
	fmt.Fprintf(w, "Eval results: %d/%d passed (%.0f%%)\n", r.Passed, total, r.PassRate()*100)
	for _, res := range r.Results {
		status := "PASS"
		if !res.Passed {
			status = "FAIL"
		}
		fmt.Fprintf(w, "  [%s] %s\n", status, res.Case.ID)
		if res.Error != nil {
			fmt.Fprintf(w, "         error: %v\n", res.Error)
		}
		for _, ch := range res.Checks {
			if !ch.Passed {
				fmt.Fprintf(w, "         FAIL %s: got %q, want %q\n", ch.Name, ch.Got, ch.Want)
			}
		}
	}
}

func runCase(pathway *pathwalk.Pathway, c Case) Result {
	result := Result{Case: c}

	mock := pathwaytest.NewMockLLMClient()
	if c.SetupMock != nil {
		c.SetupMock(mock)
	}

	engine := pathwalk.NewEngine(pathway, mock)
	run, err := engine.Run(context.Background(), c.Task)
	if err != nil {
		result.Error = err
		result.Checks = append(result.Checks, Check{
			Name: "no_error", Passed: false, Got: err.Error(), Want: "(no error)",
		})
		return result
	}

	// Check termination reason.
	wantReason := c.Expect.Reason
	if wantReason == "" {
		wantReason = "terminal"
	}
	result.Checks = append(result.Checks, Check{
		Name:   "reason",
		Passed: run.Reason == wantReason,
		Got:    run.Reason,
		Want:   wantReason,
	})

	// Check terminal node (last step's NodeID).
	if c.Expect.TerminalNode != "" {
		lastID := ""
		if len(run.Steps) > 0 {
			lastID = run.Steps[len(run.Steps)-1].NodeID
		}
		result.Checks = append(result.Checks, Check{
			Name:   "terminal_node",
			Passed: lastID == c.Expect.TerminalNode,
			Got:    lastID,
			Want:   c.Expect.TerminalNode,
		})
	}

	// Check that specific nodes were visited.
	if len(c.Expect.VisitedNodes) > 0 {
		visitedIDs := make([]string, len(run.Steps))
		for i, s := range run.Steps {
			visitedIDs[i] = s.NodeID
		}
		path := strings.Join(visitedIDs, "→")
		for _, nodeID := range c.Expect.VisitedNodes {
			found := false
			for _, id := range visitedIDs {
				if id == nodeID {
					found = true
					break
				}
			}
			result.Checks = append(result.Checks, Check{
				Name:   "visited:" + nodeID,
				Passed: found,
				Got:    path,
				Want:   "path contains " + nodeID,
			})
		}
	}

	// Check variable values.
	for k, want := range c.Expect.Variables {
		got := fmt.Sprintf("%v", run.Variables[k])
		result.Checks = append(result.Checks, Check{
			Name:   "var:" + k,
			Passed: got == want,
			Got:    got,
			Want:   want,
		})
	}

	// Overall: pass only if every check passed.
	result.Passed = true
	for _, ch := range result.Checks {
		if !ch.Passed {
			result.Passed = false
			break
		}
	}
	return result
}
