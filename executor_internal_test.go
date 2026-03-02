package pathwalk

// Internal tests for unexported functions in executor.go and router.go.
// Uses package pathwalk (not _test) to access unexported symbols.

import "testing"

func TestParseChannelDirective(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantTool     string
		wantArgKey   string
		wantArgValue string
		wantOK       bool
	}{
		{
			name:         "graphql mutation",
			input:        `<|channel|>commentary to=graphql <|constrain|>json<|message|>{"query":"mutation { createOrder }"}`,
			wantTool:     "graphql",
			wantArgKey:   "query",
			wantArgValue: "mutation { createOrder }",
			wantOK:       true,
		},
		{
			name:         "custom tool name",
			input:        `<|channel|>to=my_tool<|message|>{"key":"val"}`,
			wantTool:     "my_tool",
			wantArgKey:   "key",
			wantArgValue: "val",
			wantOK:       true,
		},
		{
			name:   "missing channel tag",
			input:  `<|message|>{"query":"x"}`,
			wantOK: false,
		},
		{
			name:   "missing message tag",
			input:  `<|channel|>to=graphql {"query":"x"}`,
			wantOK: false,
		},
		{
			name:   "message before channel",
			input:  `<|message|>{"q":"x"}<|channel|>to=graphql`,
			wantOK: false,
		},
		{
			name:   "no to= attribute",
			input:  `<|channel|>commentary<|message|>{"q":"x"}`,
			wantOK: false,
		},
		{
			name:   "invalid JSON payload",
			input:  `<|channel|>to=graphql<|message|>not-json`,
			wantOK: false,
		},
		{
			name:   "plain text, no directive",
			input:  `The order has been created successfully.`,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolName, args, ok := parseChannelDirective(tc.input)
			if ok != tc.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if toolName != tc.wantTool {
				t.Errorf("toolName=%q, want %q", toolName, tc.wantTool)
			}
			if tc.wantArgKey != "" {
				got, exists := args[tc.wantArgKey]
				if !exists {
					t.Errorf("arg %q missing; args=%v", tc.wantArgKey, args)
				} else if got != tc.wantArgValue {
					t.Errorf("args[%q]=%q, want %q", tc.wantArgKey, got, tc.wantArgValue)
				}
			}
		})
	}
}

func TestConditionSummary(t *testing.T) {
	tests := []struct {
		name   string
		conds  []RouteCondition
		want   string
	}{
		{
			name:  "empty conditions",
			conds: []RouteCondition{},
			want:  "conditions matched",
		},
		{
			name:  "nil conditions",
			conds: nil,
			want:  "conditions matched",
		},
		{
			name: "single condition",
			conds: []RouteCondition{
				{Field: "operation_type", Operator: "is", Value: "order_mgmt"},
			},
			want: `operation_type is "order_mgmt"`,
		},
		{
			name: "multiple conditions",
			conds: []RouteCondition{
				{Field: "score", Operator: ">=", Value: "100"},
				{Field: "status", Operator: "is", Value: "active"},
			},
			want: `score >= "100" (+1)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := conditionSummary(tc.conds)
			if got != tc.want {
				t.Errorf("conditionSummary=%q, want %q", got, tc.want)
			}
		})
	}
}
