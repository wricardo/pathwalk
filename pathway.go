package pathwalk

import (
	"encoding/json"
	"fmt"
	"os"
)

// pathwayJSON mirrors the top-level structure of a pathway JSON file.
type pathwayJSON struct {
	Nodes            []nodeJSON `json:"nodes"`
	Edges            []edgeJSON `json:"edges"`
	GraphQLEndpoint  string     `json:"graphqlEndpoint"`
	MaxTurns         int        `json:"maxTurns"`
	MaxVisitsPerNode int        `json:"maxVisitsPerNode"`
}

type nodeJSON struct {
	ID   string       `json:"id"`
	Type string       `json:"type"`
	Data nodeDataJSON `json:"data"`
}

type nodeDataJSON struct {
	Name        string `json:"name"`
	Text        string `json:"text"`
	Prompt      string `json:"prompt"`
	IsStart     bool   `json:"isStart"`
	IsGlobal    bool   `json:"isGlobal"`
	GlobalLabel string `json:"globalLabel"`
	Condition   string `json:"condition"`

	// [[name, type, description, required], ...]
	ExtractVars []json.RawMessage `json:"extractVars"`

	ModelOptions struct {
		NewTemperature float64 `json:"newTemperature"`
	} `json:"modelOptions"`

	// Webhook fields
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
	Body    any               `json:"body"`

	// Route fields
	Routes         []routeRuleJSON `json:"routes"`
	FallbackNodeID string          `json:"fallbackNodeId"`

	// Per-node visit cap override (0 = use pathway default)
	MaxVisits int `json:"maxVisits"`
}

type routeRuleJSON struct {
	Conditions []routeConditionJSON `json:"conditions"`
	TargetID   string               `json:"targetNodeId"`
}

type routeConditionJSON struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
}

type edgeJSON struct {
	ID     string       `json:"id"`
	Source string       `json:"source"`
	Target string       `json:"target"`
	Data   edgeDataJSON `json:"data"`
}

type edgeDataJSON struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// rawTypeToNodeType maps raw JSON type strings to normalized NodeType constants.
var rawTypeToNodeType = map[string]NodeType{
	"Default":  NodeTypeLLM,
	"End Call": NodeTypeTerminal,
	"Webhook":  NodeTypeWebhook,
	"Route":    NodeTypeRoute,
}

func parseNodeType(raw string) NodeType {
	if nt, ok := rawTypeToNodeType[raw]; ok {
		return nt
	}
	return NodeType(raw)
}

// Pathway holds parsed nodes and edges with lookup indexes.
type Pathway struct {
	Nodes           []*Node
	Edges           []*Edge
	NodeByID        map[string]*Node
	EdgesFrom       map[string][]*Edge // source nodeID → outgoing edges
	StartNode       *Node
	GlobalNodes     []*Node // nodes with IsGlobal == true and a non-empty GlobalLabel
	GraphQLEndpoint string  // optional default GraphQL endpoint

	// MaxTurns caps the total number of node-to-node transitions in a run.
	// 0 means use the engine's WithMaxSteps value (default 50).
	MaxTurns int
	// MaxVisitsPerNode is the default per-node visit cap for all nodes in the pathway.
	// 0 means no limit unless a node's own MaxVisits overrides it.
	MaxVisitsPerNode int
}

// ParsePathway reads a pathway JSON file and returns a Pathway.
func ParsePathway(path string) (*Pathway, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading pathway file: %w", err)
	}
	return ParsePathwayBytes(data)
}

// ParsePathwayBytes parses a pathway from raw JSON bytes.
func ParsePathwayBytes(data []byte) (*Pathway, error) {
	var raw pathwayJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing pathway JSON: %w", err)
	}

	pp := &Pathway{
		NodeByID:         make(map[string]*Node),
		EdgesFrom:        make(map[string][]*Edge),
		GraphQLEndpoint:  raw.GraphQLEndpoint,
		MaxTurns:         raw.MaxTurns,
		MaxVisitsPerNode: raw.MaxVisitsPerNode,
	}

	for _, rn := range raw.Nodes {
		n := &Node{
			ID:          rn.ID,
			Type:        parseNodeType(rn.Type),
			Name:        rn.Data.Name,
			IsStart:     rn.Data.IsStart,
			IsGlobal:    rn.Data.IsGlobal,
			GlobalLabel: rn.Data.GlobalLabel,
			Prompt:      rn.Data.Prompt,
			Text:        rn.Data.Text,
			Condition:   rn.Data.Condition,
			Temperature: rn.Data.ModelOptions.NewTemperature,

			// Terminal node
			TerminalText: rn.Data.Text,

			// Webhook
			WebhookURL:     rn.Data.URL,
			WebhookMethod:  rn.Data.Method,
			WebhookHeaders: rn.Data.Headers,
			WebhookBody:    rn.Data.Body,

			// Route
			FallbackNodeID: rn.Data.FallbackNodeID,

			MaxVisits: rn.Data.MaxVisits,
		}

		// Parse extractVars — each element is [name, type, description, required]
		for i, raw := range rn.Data.ExtractVars {
			var tuple []json.RawMessage
			if err := json.Unmarshal(raw, &tuple); err != nil || len(tuple) < 3 {
				continue
			}
			var name, typ, desc string
			if err := json.Unmarshal(tuple[0], &name); err != nil {
				return nil, fmt.Errorf("node %q extractVars[%d]: invalid name: %w", rn.Data.Name, i, err)
			}
			if err := json.Unmarshal(tuple[1], &typ); err != nil {
				return nil, fmt.Errorf("node %q extractVars[%d]: invalid type: %w", rn.Data.Name, i, err)
			}
			if err := json.Unmarshal(tuple[2], &desc); err != nil {
				return nil, fmt.Errorf("node %q extractVars[%d]: invalid description: %w", rn.Data.Name, i, err)
			}
			required := false
			if len(tuple) >= 4 {
				if err := json.Unmarshal(tuple[3], &required); err != nil {
					return nil, fmt.Errorf("node %q extractVars[%d]: invalid required flag: %w", rn.Data.Name, i, err)
				}
			}
			n.ExtractVars = append(n.ExtractVars, VariableDef{
				Name: name, Type: typ, Description: desc, Required: required,
			})
		}

		// Parse route rules
		for _, rr := range rn.Data.Routes {
			rule := RouteRule{TargetID: rr.TargetID}
			for _, rc := range rr.Conditions {
				rule.Conditions = append(rule.Conditions, RouteCondition{
					Field: rc.Field, Operator: rc.Operator, Value: rc.Value,
				})
			}
			n.Routes = append(n.Routes, rule)
		}

		if n.Type == NodeTypeWebhook && n.WebhookMethod == "" {
			n.WebhookMethod = "POST"
		}

		pp.Nodes = append(pp.Nodes, n)
		pp.NodeByID[n.ID] = n
		if n.IsStart {
			if pp.StartNode != nil {
				return nil, fmt.Errorf("pathway has multiple start nodes: %q and %q", pp.StartNode.ID, n.ID)
			}
			pp.StartNode = n
		}
		if n.IsGlobal && n.GlobalLabel != "" {
			pp.GlobalNodes = append(pp.GlobalNodes, n)
		}
	}

	for _, re := range raw.Edges {
		e := &Edge{
			ID:     re.ID,
			Source: re.Source,
			Target: re.Target,
			Label:  re.Data.Label,
			Desc:   re.Data.Description,
		}
		pp.Edges = append(pp.Edges, e)
		pp.EdgesFrom[e.Source] = append(pp.EdgesFrom[e.Source], e)
	}

	if pp.StartNode == nil {
		return nil, fmt.Errorf("no start node found in pathway")
	}

	return pp, nil
}
