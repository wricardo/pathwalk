---
name: pathwalk-engineer
description: "Engineering skill for the pathwalk Go library — a conversational pathway executor that runs directed graph JSON files as agentic pipelines. Use when: (1) Building or wiring a pathwalk Engine in a Go application, (2) Writing or editing pathway JSON files, (3) Implementing custom LLM clients or tools, (4) Writing tests with MockLLMClient, (5) Integrating pathwalk with Temporal for durable execution, (6) Debugging routing, variable extraction, or node execution issues, (7) Adding node-level webhook tools to a pathway."
---

# Pathwalk Engineer

Reference for implementing, testing, and debugging with the [pathwalk](https://github.com/wricardo/pathwalk) Go library.

## What Pathwalk Does

Pathwalk executes **pathway JSON files** as agentic pipelines. A pathway is a directed graph where:
- **Nodes** do work (call an LLM, make an HTTP request, evaluate conditions, or terminate)
- **Edges** define possible next nodes
- The **Engine** walks the graph step by step until a terminal condition

## Decision Table

| Goal | Reference |
|------|-----------|
| Parse and run a pathway | [engine-api.md](references/engine-api.md) |
| Write or edit pathway JSON | [pathway-json.md](references/pathway-json.md) |
| Add tools (GraphQL, webhooks) | [tools.md](references/tools.md) |
| Write tests without a real LLM | [testing.md](references/testing.md) |
| Durable execution via Temporal | [temporal.md](references/temporal.md) |

## Quick Start

```go
import (
    "github.com/wricardo/pathwalk"
    "github.com/wricardo/pathwalk/tools"
)

pathway, err := pathwalk.ParsePathway("my_pathway.json")
if err != nil {
    log.Fatal(err)
}

llm := pathwalk.NewOpenAIClient(os.Getenv("OPENAI_API_KEY"), "", "gpt-4o")

gql := &tools.GraphQLTool{Endpoint: "http://localhost:4000/graphql"}

engine := pathwalk.NewEngine(pathway, llm,
    pathwalk.WithTools(gql.Tools()...),
)

result, err := engine.Run(ctx, "Create an order for John: 2x Margherita")
fmt.Println(result.Output)
fmt.Println(result.Variables)
```

## Node Types at a Glance

| JSON type | NodeType constant | What happens |
|-----------|-------------------|--------------|
| `"Default"` | `NodeTypeLLM` | 1–3 LLM calls: execute → extract_vars → route |
| `"End Call"` | `NodeTypeTerminal` | Returns `TerminalText`, ends run |
| `"Webhook"` | `NodeTypeWebhook` | HTTP call with `{{var}}` body template |
| `"Route"` | `NodeTypeRoute` | Pure-Go condition evaluation, no LLM |

## LLM Call Purposes (critical for mocking)

An LLM node makes up to three separate LLM calls per visit:

| Purpose | When | Tool exposed |
|---------|------|--------------|
| `"execute"` | Always — runs the node's prompt | global + node tools |
| `"extract_vars"` | Only if `extractVars` is non-empty | `set_variables` |
| `"route"` | Only if >1 outgoing edge | `select_route` |

Context keys `NodeIDContextKey` and `CallPurposeContextKey` are set before each call so mocks can distinguish them.

## RunResult Reason Values

| Reason | Meaning |
|--------|---------|
| `"terminal"` | Reached a Terminal node — normal success |
| `"max_steps"` | Hit the step cap (default 50) |
| `"dead_end"` | Node has no outgoing edges and is not Terminal |
| `"error"` | Execution error (LLM failure, webhook failure) |
| `"missing_node"` | Edge or route pointed to a node ID not in the pathway |
| `"max_node_visits"` | A node was visited more than its `MaxVisits` limit |

## Key Gotchas

- `NewEngine` **panics** on nil pathway or nil LLM — validate inputs before calling
- `ParsePathwayBytes` enforces **referential integrity**: every edge source/target, route `targetNodeId`, `fallbackNodeId`, and tool `responsePathway.nodeId` must reference a real node
- `extractVars` tuples that are malformed (fewer than 3 elements) cause a **parse error**, not a silent skip
- Route condition operators must be one of: `is`, `equals`, `==`, `is not`, `not equals`, `!=`, `contains`, `not contains`, `>`, `>=`, `<`, `<=`
- `Run()` returns **both** a non-nil `*RunResult` and a non-nil `error` when `Reason` is `"error"` or `"missing_node"` — always check both
- Variable extraction failure is **non-fatal**: the engine logs a warning and continues without the vars
- The `Output` field of `RunResult` is the last LLM/webhook output **before** the terminal node — the terminal node's text is NOT in `Output`, only in `Reason == "terminal"`
