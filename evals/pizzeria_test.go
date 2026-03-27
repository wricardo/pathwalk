package evals_test

import (
	"os"
	"testing"

	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/evals"
	"github.com/wricardo/pathwalk/pathwaytest"
)

// pizzeriaPathway loads the pathway file once for all cases in this file.
func pizzeriaPathway(t *testing.T) *pathwalk.Pathway {
	t.Helper()
	pp, err := pathwalk.ParsePathway("../examples/pizzeria_ops.json")
	if err != nil {
		t.Fatalf("ParsePathway: %v", err)
	}
	return pp
}

// pizzeriaCases are the eval scenarios for the pizzeria_ops pathway.
// Each case scripts the mock to simulate a realistic LLM response, then
// checks that the pathway routed correctly and extracted the right variables.
var pizzeriaCases = []evals.Case{
	{
		ID:   "order_mgmt_routing",
		Task: "Create an order for John: 2x Margherita, table 4",
		SetupMock: func(m *pathwaytest.MockLLMClient) {
			m.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "This is an order management request — creating a new order.",
			})
			m.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					{Name: "set_variables", Args: map[string]any{"operation_type": "order_mgmt"}},
				},
			})
			m.SetDefault(pathwaytest.MockResponse{Content: "Order created successfully."})
		},
		Expect: evals.Expectation{
			TerminalNode: "end",
			VisitedNodes: []string{"start", "route-op", "order-mgmt", "end"},
			Variables:    map[string]string{"operation_type": "order_mgmt"},
		},
	},
	{
		ID:   "menu_mgmt_routing",
		Task: "Add a new pizza called Truffle Bliss at $18.99",
		SetupMock: func(m *pathwaytest.MockLLMClient) {
			m.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "This is a menu management request — creating a new pizza.",
			})
			m.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					{Name: "set_variables", Args: map[string]any{"operation_type": "menu_mgmt"}},
				},
			})
			m.SetDefault(pathwaytest.MockResponse{Content: "Pizza added to menu."})
		},
		Expect: evals.Expectation{
			TerminalNode: "end",
			VisitedNodes: []string{"start", "route-op", "menu-mgmt", "end"},
			Variables:    map[string]string{"operation_type": "menu_mgmt"},
		},
	},
	{
		ID:   "inventory_mgmt_routing",
		Task: "Check which ingredients are running low and restock them",
		SetupMock: func(m *pathwaytest.MockLLMClient) {
			m.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "This is an inventory management request.",
			})
			m.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					{Name: "set_variables", Args: map[string]any{"operation_type": "inventory_mgmt"}},
				},
			})
			m.SetDefault(pathwaytest.MockResponse{Content: "Inventory checked and restocked."})
		},
		Expect: evals.Expectation{
			TerminalNode: "end",
			VisitedNodes: []string{"start", "route-op", "inventory-mgmt", "end"},
			Variables:    map[string]string{"operation_type": "inventory_mgmt"},
		},
	},
	{
		ID:   "reporting_routing",
		Task: "Give me a revenue summary for today",
		SetupMock: func(m *pathwaytest.MockLLMClient) {
			m.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "This is a reporting request.",
			})
			m.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					{Name: "set_variables", Args: map[string]any{"operation_type": "reporting"}},
				},
			})
			m.SetDefault(pathwaytest.MockResponse{Content: "Report generated."})
		},
		Expect: evals.Expectation{
			TerminalNode: "end",
			VisitedNodes: []string{"start", "route-op", "reporting", "end"},
			Variables:    map[string]string{"operation_type": "reporting"},
		},
	},
	{
		ID:   "unknown_operation_fallback",
		Task: "Do something completely unrelated",
		SetupMock: func(m *pathwaytest.MockLLMClient) {
			m.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "I cannot categorize this request.",
			})
			m.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				ToolCalls: []pathwaytest.MockToolCall{
					// Extracts a value that doesn't match any route condition.
					{Name: "set_variables", Args: map[string]any{"operation_type": "unknown"}},
				},
			})
		},
		Expect: evals.Expectation{
			// Route node's fallbackNodeId is "end" — should land there directly.
			TerminalNode: "end",
			VisitedNodes: []string{"start", "route-op", "end"},
			Variables:    map[string]string{"operation_type": "unknown"},
		},
	},
	{
		ID:   "order_mgmt_no_extract",
		Task: "List all pending orders",
		SetupMock: func(m *pathwaytest.MockLLMClient) {
			m.OnNodePurpose("start", "execute", pathwaytest.MockResponse{
				Content: "Listing pending orders.",
			})
			// Simulate LLM failing to extract the variable — no tool call.
			m.OnNodePurpose("start", "extract_vars", pathwaytest.MockResponse{
				Content: "(no extraction)",
			})
		},
		Expect: evals.Expectation{
			// Without operation_type set, the Route node hits its fallback.
			TerminalNode: "end",
			VisitedNodes: []string{"route-op", "end"},
		},
	},
}

// TestPizzeriaEvals runs all pizzeria eval cases as individual sub-tests.
// Each sub-test is named by Case.ID, so `go test -run TestPizzeriaEvals/order`
// runs only matching cases.
func TestPizzeriaEvals(t *testing.T) {
	pp := pizzeriaPathway(t)

	for _, c := range pizzeriaCases {
		c := c
		t.Run(c.ID, func(t *testing.T) {
			t.Parallel()
			report := evals.Run(pp, []evals.Case{c})
			res := report.Results[0]

			if !res.Passed {
				report.Print(os.Stdout)
				for _, ch := range res.Checks {
					if !ch.Passed {
						t.Errorf("check %q failed: got %q, want %q", ch.Name, ch.Got, ch.Want)
					}
				}
			}
		})
	}
}

// TestPizzeriaEvalsReport runs all cases together and prints the aggregate
// report. Useful for a quick overall score: go test -v -run TestPizzeriaEvalsReport
func TestPizzeriaEvalsReport(t *testing.T) {
	pp := pizzeriaPathway(t)
	report := evals.Run(pp, pizzeriaCases)
	report.Print(os.Stdout)

	if report.Failed > 0 {
		t.Errorf("%d/%d eval cases failed", report.Failed, report.Passed+report.Failed)
	}
}
