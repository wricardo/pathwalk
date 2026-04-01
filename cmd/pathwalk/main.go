package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/urfave/cli/v2"
	pathwalk "github.com/wricardo/pathwalk"
	"github.com/wricardo/pathwalk/tools"
)

func main() {
	app := &cli.App{
		Name:  "pathwalk",
		Usage: "Execute conversational pathway JSON files as an agentic pipeline",
		Commands: []*cli.Command{
			runCmd(),
			validateCmd(),
			agentCmd(),
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}

func runCmd() *cli.Command {
	return &cli.Command{
		Name:  "run",
		Usage: "Run a pathway with a given task",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:     "pathway",
				Aliases:  []string{"p"},
				Usage:    "Path to the pathway JSON file",
				Required: true,
			},
			&cli.StringFlag{
				Name:     "task",
				Aliases:  []string{"t"},
				Usage:    "Initial task description",
				Required: true,
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "LLM model name",
				Value: "qwen/qwen3.5-35b-a3b",
			},
			&cli.StringFlag{
				Name:    "api-key",
				Usage:   "API key for the LLM provider",
				EnvVars: []string{"OPENAI_API_KEY"},
			},
			&cli.StringFlag{
				Name:    "base-url",
				Usage:   "Base URL for an OpenAI-compatible LLM API",
				EnvVars: []string{"OPENAI_BASE_URL"},
			},
			&cli.IntFlag{
				Name:  "max-steps",
				Usage: "Maximum number of nodes to traverse",
				Value: 50,
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Print each step's output and routing decision",
			},
			&cli.StringFlag{
				Name:    "graphql-endpoint",
				Usage:   "GraphQL API endpoint (enables graphql_query, graphql_mutation, and schema tools)",
				EnvVars: []string{"GRAPHQL_ENDPOINT"},
			},
			&cli.StringSliceFlag{
				Name:  "graphql-header",
				Usage: "HTTP headers for GraphQL requests, format: Key=Value (repeatable)",
			},
		},
		Action: func(c *cli.Context) error {
			return runPathway(c)
		},
	}
}

func runPathway(c *cli.Context) error {
	// Parse pathway
	pathway, err := pathwalk.ParsePathway(c.String("pathway"))
	if err != nil {
		return fmt.Errorf("parsing pathway: %w", err)
	}

	// Create LLM client
	llm := pathwalk.NewOpenAIClient(
		c.String("api-key"),
		c.String("base-url"),
		c.String("model"),
	)

	// Build engine options
	opts := []pathwalk.EngineOption{
		pathwalk.WithMaxSteps(c.Int("max-steps")),
	}

	// Wire up GraphQL tool: CLI flag takes precedence over pathway default
	endpoint := c.String("graphql-endpoint")
	if endpoint == "" {
		endpoint = pathway.GraphQLEndpoint
	}
	if endpoint != "" {
		headers := parseHeaders(c.StringSlice("graphql-header"))
		gt := &tools.GraphQLTool{Endpoint: endpoint, Headers: headers}
		opts = append(opts, pathwalk.WithTools(gt.AsTools()...))
	}

	engine := pathwalk.NewEngine(pathway, llm, opts...)

	if pathway.StartNode == nil {
		return fmt.Errorf("pathway has no start node")
	}

	ctx := c.Context
	state := pathwalk.NewState(c.String("task"))
	nodeID := pathway.StartNode.ID
	verbose := c.Bool("verbose")
	maxSteps := c.Int("max-steps")
	scanner := bufio.NewScanner(os.Stdin)

	for step := 0; step < maxSteps; step++ {
		result, err := engine.Step(ctx, state, nodeID)
		if err != nil {
			return fmt.Errorf("step error: %w", err)
		}

		if verbose && result.Output != "" {
			fmt.Printf("[%s] %s\n", result.Step.NodeName, truncate(result.Output, 200))
		}

		// Handle checkpoint suspension — prompt the user on stdin.
		if result.WaitCondition != nil {
			wc := result.WaitCondition
			response, err := promptCheckpoint(wc, scanner)
			if err != nil {
				return fmt.Errorf("reading checkpoint input: %w", err)
			}

			resumeResult, err := engine.ResumeStep(ctx, state, wc.NodeID, response)
			if err != nil {
				return fmt.Errorf("resume error: %w", err)
			}
			if resumeResult.Done {
				break
			}
			nodeID = resumeResult.NextNodeID
			continue
		}

		if result.Done {
			if result.Output != "" {
				fmt.Printf("\n=== Result ===\n")
				fmt.Printf("Reason: %s\n", result.Reason)
				fmt.Printf("Output:\n%s\n", result.Output)
			}
			break
		}

		nodeID = result.NextNodeID
	}

	if len(state.Variables) > 0 {
		fmt.Printf("\n=== Variables ===\n")
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(state.Variables)
	}

	if verbose && len(state.Steps) > 0 {
		fmt.Printf("\n=== Steps (%d) ===\n", len(state.Steps))
		for i, s := range state.Steps {
			fmt.Printf("%d. [%s] → %s\n   %s\n",
				i+1, s.NodeName, s.NextNode, truncate(s.Output, 200))
		}
	}

	return nil
}

// promptCheckpoint prints a checkpoint prompt and reads the response from stdin.
// It retries on empty input and validates approval options.
func promptCheckpoint(wc *pathwalk.WaitCondition, scanner *bufio.Scanner) (pathwalk.CheckpointResponse, error) {
	fmt.Printf("\n--- Checkpoint: %s ---\n", wc.NodeName)
	fmt.Printf("%s\n", wc.Prompt)

	for {
		switch wc.Mode {
		case pathwalk.CheckpointModeHumanApproval:
			options := strings.Join(wc.Options, "/")
			fmt.Printf("[%s]: ", options)
		case pathwalk.CheckpointModeHumanInput:
			fmt.Printf("> ")
		}

		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				return pathwalk.CheckpointResponse{}, err
			}
			return pathwalk.CheckpointResponse{}, fmt.Errorf("unexpected end of input")
		}
		value := strings.TrimSpace(scanner.Text())

		if value == "" {
			fmt.Println("Input required.")
			continue
		}

		// Validate approval options.
		if wc.Mode == pathwalk.CheckpointModeHumanApproval && len(wc.Options) > 0 {
			valid := false
			for _, opt := range wc.Options {
				if strings.EqualFold(value, opt) {
					value = opt // normalize to exact option string
					valid = true
					break
				}
			}
			if !valid {
				fmt.Printf("Invalid choice. Please enter one of: %s\n", strings.Join(wc.Options, ", "))
				continue
			}
		}

		return pathwalk.CheckpointResponse{Value: value}, nil
	}
}

func parseHeaders(pairs []string) map[string]string {
	out := make(map[string]string, len(pairs))
	for _, p := range pairs {
		for i, ch := range p {
			if ch == '=' {
				out[p[:i]] = p[i+1:]
				break
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func agentCmd() *cli.Command {
	return &cli.Command{
		Name:  "agent",
		Usage: "Run a free-form GraphQL agent (no pathway required)",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "endpoint",
				Aliases: []string{"e"},
				Usage:   "GraphQL API endpoint",
				EnvVars: []string{"GRAPHQL_ENDPOINT"},
			},
			&cli.StringSliceFlag{
				Name:  "graphql-header",
				Usage: "HTTP headers for GraphQL requests, format: Key=Value (repeatable)",
			},
			&cli.StringFlag{
				Name:  "model",
				Usage: "LLM model name",
				Value: "qwen/qwen3.5-35b-a3b",
			},
			&cli.StringFlag{
				Name:    "api-key",
				Usage:   "API key for the LLM provider",
				EnvVars: []string{"OPENAI_API_KEY"},
			},
			&cli.StringFlag{
				Name:    "base-url",
				Usage:   "Base URL for an OpenAI-compatible LLM API",
				EnvVars: []string{"OPENAI_BASE_URL"},
			},
			&cli.StringFlag{
				Name:    "task",
				Aliases: []string{"t"},
				Usage:   "One-shot task (skips interactive prompt, exits when done)",
			},
		},
		Action: func(c *cli.Context) error {
			return runAgentCmd(c)
		},
	}
}

func runAgentCmd(c *cli.Context) error {
	llm := pathwalk.NewOpenAIClient(
		c.String("api-key"),
		c.String("base-url"),
		c.String("model"),
	)

	endpoint := c.String("endpoint")
	headers := parseHeaders(c.StringSlice("graphql-header"))
	gt := &tools.GraphQLTool{Endpoint: endpoint, Headers: headers}

	// Build schema context for the system prompt; best-effort (no fatal on failure).
	var schemaCtx string
	if endpoint != "" {
		var err error
		schemaCtx, err = gt.BuildSchemaContext(c.Context)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not fetch schema: %v\n", err)
		}
	}

	systemPrompt := pathwalk.BuildAgentSystemPrompt(schemaCtx)
	agent := pathwalk.NewAgentWithModel(llm, gt.AsTools(), systemPrompt, c.String("model"))

	return agent.RunInteractive(c.Context, os.Stdin, os.Stdout, c.String("task"))
}

func validateCmd() *cli.Command {
	return &cli.Command{
		Name:      "validate",
		Usage:     "Validate a pathway JSON file against the schema and structural rules",
		ArgsUsage: "<pathway.json>",
		Action: func(c *cli.Context) error {
			if c.NArg() == 0 {
				return fmt.Errorf("usage: pathwalk validate <pathway.json>")
			}
			path := c.Args().First()

			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}

			result := pathwalk.ValidatePathwayBytes(data)

			if len(result.SchemaErrors) > 0 {
				fmt.Fprintf(os.Stderr, "Schema errors:\n")
				for _, e := range result.SchemaErrors {
					fmt.Fprintf(os.Stderr, "  - %s\n", e)
				}
			} else {
				fmt.Printf("Schema: ok\n")
			}

			if result.ParseError != nil {
				fmt.Fprintf(os.Stderr, "Parse error: %s\n", result.ParseError)
			} else {
				fmt.Printf("Parse:  ok\n")
			}

			if !result.Valid() {
				os.Exit(1)
			}
			fmt.Printf("%s is valid\n", path)
			return nil
		},
	}
}
