package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	pathwalk "github.com/wricardo/pathwalk"
)

// AsTools returns all six GraphQL tools: graphql_query, graphql_mutation,
// graphql_queries, graphql_mutations, graphql_types, and graphql_type.
func (t *GraphQLTool) AsTools() []pathwalk.Tool {
	return []pathwalk.Tool{
		t.queryTool(),
		t.mutationTool(),
		t.queriesListTool(),
		t.mutationsListTool(),
		t.typesListTool(),
		t.typeDescribeTool(),
	}
}

// queriesListTool returns a tool that lists available queries with their signatures.
func (t *GraphQLTool) queriesListTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name:        "graphql_queries",
		Description: "List available GraphQL queries with argument types and return types. Use before writing a query to know what fields exist.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{
					"type":        "string",
					"description": "Optional substring to filter query names",
				},
				"withDescription": map[string]any{
					"type":        "boolean",
					"description": "Include field descriptions as comments. Defaults to false.",
				},
			},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			filter, _ := args["filter"].(string)
			withDesc, _ := args["withDescription"].(bool)
			const q = `{ __schema { queryType { fields {
				name description
				args { name type { ...TR } }
				type { ...TR }
			} } } }
			fragment TR on __Type { kind name ofType { kind name ofType { kind name ofType { kind name } } } }`
			raw, err := t.runIntrospection(ctx, q)
			if err != nil {
				return nil, err
			}
			fields := extractFields(raw, "queryType")
			return formatFieldList(fields, filter, withDesc), nil
		},
	}
}

// mutationsListTool returns a tool that lists available mutations with their signatures.
func (t *GraphQLTool) mutationsListTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name:        "graphql_mutations",
		Description: "List available GraphQL mutations with argument types and return types. Use before writing a mutation to know what fields and input types exist.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{
					"type":        "string",
					"description": "Optional substring to filter mutation names",
				},
				"withDescription": map[string]any{
					"type":        "boolean",
					"description": "Include field descriptions as comments. Defaults to false.",
				},
			},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			filter, _ := args["filter"].(string)
			withDesc, _ := args["withDescription"].(bool)
			const q = `{ __schema { mutationType { fields {
				name description
				args { name type { ...TR } }
				type { ...TR }
			} } } }
			fragment TR on __Type { kind name ofType { kind name ofType { kind name ofType { kind name } } } }`
			raw, err := t.runIntrospection(ctx, q)
			if err != nil {
				return nil, err
			}
			fields := extractFields(raw, "mutationType")
			return formatFieldList(fields, filter, withDesc), nil
		},
	}
}

// typesListTool returns a tool that lists all named non-built-in GraphQL types.
func (t *GraphQLTool) typesListTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name:        "graphql_types",
		Description: "List all named GraphQL types (objects, inputs, enums, interfaces). Use to discover type names before calling graphql_type.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{
					"type":        "string",
					"description": "Optional substring to filter type names",
				},
				"withDescription": map[string]any{
					"type":        "boolean",
					"description": "Include type descriptions as comments. Defaults to false.",
				},
			},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			filter, _ := args["filter"].(string)
			withDesc, _ := args["withDescription"].(bool)
			const q = `{ __schema { types { kind name description } } }`
			raw, err := t.runIntrospection(ctx, q)
			if err != nil {
				return nil, err
			}
			return formatTypesList(raw, filter, withDesc), nil
		},
	}
}

// typeDescribeTool returns a tool that describes the fields of a named GraphQL type
// and expands non-scalar field types 2 levels deep.
func (t *GraphQLTool) typeDescribeTool() pathwalk.Tool {
	return pathwalk.Tool{
		Name:        "graphql_type",
		Description: "Describe a GraphQL type with its fields. Non-scalar field types are expanded 2 levels deep so a single call reveals the full shape of nested types.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "The GraphQL type name, e.g. CreateOrderInput, Order, OrderStatus",
				},
			},
			"required": []string{"name"},
		},
		Fn: func(ctx context.Context, args map[string]any) (any, error) {
			name, ok := args["name"].(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("graphql_type: 'name' is required")
			}
			q := fmt.Sprintf(`{ __type(name: %q) {
				kind name description
				fields { name description type { ...L1 } args { name type { ...TR } } }
				inputFields { name description type { ...L1 } }
				enumValues { name description }
			} }
			fragment TR on __Type { kind name ofType { kind name ofType { kind name ofType { kind name } } } }
			fragment L2 on __Type {
				...TR
				fields { name description type { ...TR } }
				inputFields { name description type { ...TR } }
				enumValues { name description }
			}
			fragment L1 on __Type {
				...TR
				fields { name description type { ...L2 } }
				inputFields { name description type { ...L2 } }
				enumValues { name description }
			}`, name)
			raw, err := t.runIntrospection(ctx, q)
			if err != nil {
				return nil, err
			}
			return formatTypeDefDeep(raw), nil
		},
	}
}

// runIntrospection executes an introspection query and returns the parsed data map.
func (t *GraphQLTool) runIntrospection(ctx context.Context, query string) (map[string]any, error) {
	result, err := t.execute(ctx, map[string]any{"query": query})
	if err != nil {
		return nil, err
	}
	m, ok := result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected introspection response type")
	}
	data, _ := m["data"].(map[string]any)
	return data, nil
}

// ── formatting helpers ────────────────────────────────────────────────────────

type gqlTypeRef struct {
	Kind   string      `json:"kind"`
	Name   string      `json:"name"`
	OfType *gqlTypeRef `json:"ofType"`
}

func typeRefFrom(v any) *gqlTypeRef {
	if v == nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	tr := &gqlTypeRef{
		Kind: strVal(m["kind"]),
		Name: strVal(m["name"]),
	}
	if of, ok := m["ofType"]; ok {
		tr.OfType = typeRefFrom(of)
	}
	return tr
}

func formatTypeRef(tr *gqlTypeRef) string {
	if tr == nil {
		return "?"
	}
	switch tr.Kind {
	case "NON_NULL":
		return formatTypeRef(tr.OfType) + "!"
	case "LIST":
		return "[" + formatTypeRef(tr.OfType) + "]"
	default:
		if tr.Name != "" {
			return tr.Name
		}
		return "?"
	}
}

// extractFields pulls the fields list from __schema.queryType or mutationType.
func extractFields(data map[string]any, key string) []map[string]any {
	if data == nil {
		return nil
	}
	schema, _ := data["__schema"].(map[string]any)
	if schema == nil {
		return nil
	}
	typeObj, _ := schema[key].(map[string]any)
	if typeObj == nil {
		return nil
	}
	raw, _ := typeObj["fields"].([]any)
	var out []map[string]any
	for _, f := range raw {
		if m, ok := f.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// formatFieldList renders query/mutation fields as compact text.
func formatFieldList(fields []map[string]any, filter string, withDescription bool) string {
	var b strings.Builder
	for _, f := range fields {
		name := strVal(f["name"])
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}

		// Build arg list
		var argParts []string
		if rawArgs, ok := f["args"].([]any); ok {
			for _, a := range rawArgs {
				am, ok := a.(map[string]any)
				if !ok {
					continue
				}
				argParts = append(argParts, strVal(am["name"])+": "+formatTypeRef(typeRefFrom(am["type"])))
			}
		}

		retType := formatTypeRef(typeRefFrom(f["type"]))
		sig := name
		if len(argParts) > 0 {
			sig += "(" + strings.Join(argParts, ", ") + ")"
		}
		sig += ": " + retType

		if withDescription {
			if desc := strVal(f["description"]); desc != "" {
				fmt.Fprintf(&b, "%s  # %s\n", sig, desc)
				continue
			}
		}
		fmt.Fprintf(&b, "%s\n", sig)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatTypesList renders all named non-built-in, non-scalar types as compact text.
func formatTypesList(data map[string]any, filter string, withDescription bool) string {
	if data == nil {
		return "(no data)"
	}
	schema, _ := data["__schema"].(map[string]any)
	if schema == nil {
		return "(no schema)"
	}
	raw, _ := schema["types"].([]any)
	var b strings.Builder
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := strVal(m["name"])
		if strings.HasPrefix(name, "__") {
			continue
		}
		kind := strVal(m["kind"])
		if kind == "SCALAR" {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(name), strings.ToLower(filter)) {
			continue
		}
		line := fmt.Sprintf("%s (%s)", name, strings.ToLower(kind))
		if withDescription {
			if desc := strVal(m["description"]); desc != "" {
				line += "  # " + desc
			}
		}
		fmt.Fprintln(&b, line)
	}
	return strings.TrimRight(b.String(), "\n")
}

// formatTypeDefDeep renders a type and appends separate blocks for each
// non-scalar type encountered in its fields (level 1) and in those types'
// fields (level 2). Each block uses formatTypeDef. Types are deduplicated.
func formatTypeDefDeep(data map[string]any) string {
	if data == nil {
		return "(no data)"
	}
	typeObj, _ := data["__type"].(map[string]any)
	if typeObj == nil {
		return "(type not found)"
	}

	seen := map[string]bool{strVal(typeObj["name"]): true}

	// Collect level-1 non-scalar types from root's fields.
	var level1 []map[string]any
	for _, fm := range allFieldMaps(typeObj) {
		if tm, ok := fm["type"].(map[string]any); ok {
			if named := namedTypeObj(tm); named != nil {
				if n := strVal(named["name"]); n != "" && !seen[n] {
					seen[n] = true
					level1 = append(level1, named)
				}
			}
		}
	}

	// Collect level-2 non-scalar types from level-1 fields.
	var level2 []map[string]any
	for _, st := range level1 {
		for _, fm := range allFieldMaps(st) {
			if tm, ok := fm["type"].(map[string]any); ok {
				if named := namedTypeObj(tm); named != nil {
					if n := strVal(named["name"]); n != "" && !seen[n] {
						seen[n] = true
						level2 = append(level2, named)
					}
				}
			}
		}
	}

	var b strings.Builder
	b.WriteString(formatTypeDef(data))
	for _, st := range level1 {
		b.WriteString("\n\n")
		b.WriteString(formatTypeDef(map[string]any{"__type": st}))
	}
	for _, st := range level2 {
		b.WriteString("\n\n")
		b.WriteString(formatTypeDef(map[string]any{"__type": st}))
	}
	return b.String()
}

// allFieldMaps returns all field entries from both "fields" and "inputFields"
// of a type object.
func allFieldMaps(typeObj map[string]any) []map[string]any {
	var result []map[string]any
	for _, key := range []string{"fields", "inputFields"} {
		if raw, ok := typeObj[key].([]any); ok {
			for _, f := range raw {
				if m, ok := f.(map[string]any); ok {
					result = append(result, m)
				}
			}
		}
	}
	return result
}

// namedTypeObj unwraps NON_NULL and LIST wrappers and returns the named type
// map for non-scalar types (OBJECT, INPUT_OBJECT, INTERFACE, ENUM, UNION).
// Returns nil for SCALAR types or when no named type can be found.
func namedTypeObj(typeMap map[string]any) map[string]any {
	if typeMap == nil {
		return nil
	}
	switch strVal(typeMap["kind"]) {
	case "NON_NULL", "LIST":
		if of, ok := typeMap["ofType"].(map[string]any); ok {
			return namedTypeObj(of)
		}
		return nil
	case "SCALAR":
		return nil
	default: // OBJECT, INPUT_OBJECT, INTERFACE, ENUM, UNION
		return typeMap
	}
}

// renderGroupedFields groups fields by their resolved type string and renders
// each group on a single line: "  name1 name2: TypeName". Fields that carry
// arguments are rendered individually on their own line because their
// signatures differ. Groups are sorted alphabetically by type string; names
// within each group are also sorted alphabetically.
func renderGroupedFields(fields []any) string {
	type group struct {
		typeStr string
		names   []string
	}

	seen := map[string]int{}
	var groups []group
	var withArgs []string

	for _, f := range fields {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		name := strVal(fm["name"])
		typeStr := formatTypeRef(typeRefFrom(fm["type"]))

		// Fields with arguments cannot be grouped — render them standalone.
		if args, ok := fm["args"].([]any); ok && len(args) > 0 {
			var argParts []string
			for _, a := range args {
				am, _ := a.(map[string]any)
				argParts = append(argParts, strVal(am["name"])+": "+formatTypeRef(typeRefFrom(am["type"])))
			}
			withArgs = append(withArgs, fmt.Sprintf("  %s(%s): %s", name, strings.Join(argParts, ", "), typeStr))
			continue
		}

		if idx, ok := seen[typeStr]; ok {
			groups[idx].names = append(groups[idx].names, name)
		} else {
			seen[typeStr] = len(groups)
			groups = append(groups, group{typeStr: typeStr, names: []string{name}})
		}
	}

	sort.Slice(groups, func(i, j int) bool { return groups[i].typeStr < groups[j].typeStr })
	for i := range groups {
		sort.Strings(groups[i].names)
	}

	var b strings.Builder
	for _, s := range withArgs {
		fmt.Fprintln(&b, s)
	}
	for _, g := range groups {
		fmt.Fprintf(&b, "  %s: %s\n", strings.Join(g.names, " "), g.typeStr)
	}
	return b.String()
}

// formatTypeDef renders a type definition as compact text.
// Fields sharing the same type are collapsed onto a single line.
// Enum values are rendered inline: enum Foo { A B C }.
func formatTypeDef(data map[string]any) string {
	if data == nil {
		return "(no data)"
	}
	typeObj, _ := data["__type"].(map[string]any)
	if typeObj == nil {
		return "(type not found)"
	}

	kind := strVal(typeObj["kind"])
	name := strVal(typeObj["name"])
	var b strings.Builder

	switch kind {
	case "ENUM":
		var vals []string
		if raw, ok := typeObj["enumValues"].([]any); ok {
			for _, v := range raw {
				vm, _ := v.(map[string]any)
				if n := strVal(vm["name"]); n != "" {
					vals = append(vals, n)
				}
			}
		}
		fmt.Fprintf(&b, "enum %s { %s }", name, strings.Join(vals, " "))

	case "INPUT_OBJECT":
		fmt.Fprintf(&b, "input %s {\n", name)
		if fields, ok := typeObj["inputFields"].([]any); ok {
			b.WriteString(renderGroupedFields(fields))
		}
		b.WriteString("}")

	default: // OBJECT, INTERFACE
		fmt.Fprintf(&b, "type %s {\n", name)
		if fields, ok := typeObj["fields"].([]any); ok {
			b.WriteString(renderGroupedFields(fields))
		}
		b.WriteString("}")
	}

	if desc := strVal(typeObj["description"]); desc != "" {
		return "# " + desc + "\n" + b.String()
	}
	return b.String()
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
