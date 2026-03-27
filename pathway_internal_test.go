package pathwalk

import (
	"encoding/json"
	"testing"
)

func TestParseBoolLoose(t *testing.T) {
	cases := []struct {
		name    string
		input   string // raw JSON value
		want    bool
		wantErr bool
	}{
		{"bool true", "true", true, false},
		{"bool false", "false", false, false},
		{"string true", `"true"`, true, false},
		{"string false", `"false"`, false, false},
		{"string yes is invalid", `"yes"`, false, true},
		{"string empty is invalid", `""`, false, true},
		{"number is invalid", `42`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBoolLoose(json.RawMessage(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for input %s, got nil", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseBoolLoose(%s) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestParseExtractVarTuple(t *testing.T) {
	cases := []struct {
		name     string
		input    string // raw JSON array
		wantNil  bool   // expect nil result (malformed)
		wantErr  bool
		wantName string
		wantType string
		wantDesc string
		wantReq  bool
	}{
		{
			name:     "valid 3-element tuple",
			input:    `["age", "integer", "The age"]`,
			wantName: "age", wantType: "integer", wantDesc: "The age", wantReq: false,
		},
		{
			name:     "4-element with bool required",
			input:    `["name", "string", "Full name", true]`,
			wantName: "name", wantType: "string", wantDesc: "Full name", wantReq: true,
		},
		{
			name:     "4-element with string true",
			input:    `["email", "string", "Email address", "true"]`,
			wantName: "email", wantType: "string", wantDesc: "Email address", wantReq: true,
		},
		{
			name:     "4-element with string false",
			input:    `["opt", "string", "Optional field", "false"]`,
			wantName: "opt", wantType: "string", wantDesc: "Optional field", wantReq: false,
		},
		{
			name:    "too short tuple",
			input:   `["name", "string"]`,
			wantNil: true,
		},
		{
			name:    "invalid JSON",
			input:   `not json`,
			wantNil: true,
		},
		{
			name:    "invalid required value",
			input:   `["name", "string", "desc", "yes"]`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vd, err := parseExtractVarTuple(json.RawMessage(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if vd != nil {
					t.Errorf("expected nil result, got %+v", vd)
				}
				return
			}
			if vd == nil {
				t.Fatal("expected non-nil result")
			}
			if vd.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", vd.Name, tc.wantName)
			}
			if vd.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", vd.Type, tc.wantType)
			}
			if vd.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", vd.Description, tc.wantDesc)
			}
			if vd.Required != tc.wantReq {
				t.Errorf("Required = %v, want %v", vd.Required, tc.wantReq)
			}
		})
	}
}

func TestParseToolResponsePathway(t *testing.T) {
	cases := []struct {
		name     string
		input    string // raw JSON
		wantType string
		wantOp   string
		wantVal  string
		wantNode string
		wantName string
	}{
		{
			name:     "object format",
			input:    `{"type":"BlandStatusCode","operator":"==","value":"404","nodeId":"err","name":"Error"}`,
			wantType: "BlandStatusCode", wantOp: "==", wantVal: "404", wantNode: "err", wantName: "Error",
		},
		{
			name:     "object format default type",
			input:    `{"type":"default","nodeId":"fallback"}`,
			wantType: "default", wantNode: "fallback",
		},
		{
			name:     "legacy tuple default completion",
			input:    `["Default/Webhook Completion","","",{"id":"","name":""}]`,
			wantType: "Default/Webhook Completion", wantNode: "", wantName: "",
		},
		{
			name:     "legacy tuple with node ref",
			input:    `["Default/Webhook Completion","","",{"id":"n1","name":"Next Node"}]`,
			wantType: "Default/Webhook Completion", wantNode: "n1", wantName: "Next Node",
		},
		{
			name:     "legacy tuple with condition",
			input:    `["BlandStatusCode","==","404",{"id":"err_node","name":"Error"}]`,
			wantType: "BlandStatusCode", wantOp: "==", wantVal: "404", wantNode: "err_node", wantName: "Error",
		},
		{
			name:     "malformed JSON returns empty",
			input:    `not json at all`,
			wantType: "",
		},
		{
			name:     "single element tuple",
			input:    `["SomeType"]`,
			wantType: "SomeType",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rp := parseToolResponsePathway(json.RawMessage(tc.input))
			if rp.Type != tc.wantType {
				t.Errorf("Type = %q, want %q", rp.Type, tc.wantType)
			}
			if rp.Operator != tc.wantOp {
				t.Errorf("Operator = %q, want %q", rp.Operator, tc.wantOp)
			}
			if rp.Value != tc.wantVal {
				t.Errorf("Value = %q, want %q", rp.Value, tc.wantVal)
			}
			if rp.NodeID != tc.wantNode {
				t.Errorf("NodeID = %q, want %q", rp.NodeID, tc.wantNode)
			}
			if rp.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", rp.Name, tc.wantName)
			}
		})
	}
}

// TestParsePathwayWithNodeTools verifies that ParsePathwayBytes correctly parses
// node-level tools from pathway JSON, including all new fields.
func TestParsePathwayWithNodeTools(t *testing.T) {
	raw := `{
		"nodes": [{
			"id": "n1",
			"type": "Default",
			"data": {
				"name": "Start",
				"isStart": true,
				"prompt": "Hello",
				"tools": [
					{
						"name": "save_data",
						"description": "Save data to backend",
						"type": "webhook",
						"behavior": "feed_context",
						"config": {
							"url": "https://example.com/api",
							"method": "POST",
							"headers": {"Authorization": "Bearer token"},
							"body": "{\"key\":\"{{value}}\"}",
							"timeout": 15,
							"retries": 2,
							"response_data": ["id", "status"]
						},
						"speech": "One moment please",
						"extractVars": [["result_id", "string", "The result ID", true]],
						"responsePathways": [
							{"type": "BlandStatusCode", "operator": "==", "value": "404", "nodeId": "error_node", "name": "Not Found"},
							{"type": "default", "nodeId": "next_node"}
						]
					},
					{
						"name": "empty_tool",
						"description": "No config",
						"type": "webhook",
						"behavior": "feed_context",
						"config": {
							"url": "https://example.com",
							"body": ""
						},
						"extractVars": [],
						"responsePathways": []
					}
				]
			}
		}],
		"edges": []
	}`

	pp, err := ParsePathwayBytes([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	node := pp.NodeByID["n1"]
	if len(node.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(node.Tools))
	}

	tool := node.Tools[0]
	if tool.Name != "save_data" {
		t.Errorf("Name = %q, want save_data", tool.Name)
	}
	if tool.Type != "webhook" {
		t.Errorf("Type = %q, want webhook", tool.Type)
	}
	if tool.Behavior != "feed_context" {
		t.Errorf("Behavior = %q, want feed_context", tool.Behavior)
	}
	if tool.URL != "https://example.com/api" {
		t.Errorf("URL = %q", tool.URL)
	}
	if tool.Method != "POST" {
		t.Errorf("Method = %q, want POST", tool.Method)
	}
	if tool.Headers["Authorization"] != "Bearer token" {
		t.Errorf("Authorization header = %q", tool.Headers["Authorization"])
	}
	if tool.Timeout != 15 {
		t.Errorf("Timeout = %d, want 15", tool.Timeout)
	}
	if tool.Retries != 2 {
		t.Errorf("Retries = %d, want 2", tool.Retries)
	}
	if tool.Speech != "One moment please" {
		t.Errorf("Speech = %q", tool.Speech)
	}
	if len(tool.ExtractVars) != 1 || tool.ExtractVars[0].Name != "result_id" || !tool.ExtractVars[0].Required {
		t.Errorf("ExtractVars = %+v", tool.ExtractVars)
	}
	if len(tool.ResponsePathways) != 2 {
		t.Fatalf("ResponsePathways len = %d, want 2", len(tool.ResponsePathways))
	}
	rp0 := tool.ResponsePathways[0]
	if rp0.Type != "BlandStatusCode" || rp0.Operator != "==" || rp0.Value != "404" || rp0.NodeID != "error_node" {
		t.Errorf("ResponsePathway[0] = %+v", rp0)
	}
	rp1 := tool.ResponsePathways[1]
	if rp1.Type != "default" || rp1.NodeID != "next_node" {
		t.Errorf("ResponsePathway[1] = %+v", rp1)
	}

	// Second tool should have defaults
	tool2 := node.Tools[1]
	if tool2.Method != "POST" {
		t.Errorf("empty tool Method = %q, want POST (default)", tool2.Method)
	}
}
