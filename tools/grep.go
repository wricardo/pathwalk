package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	pathwalk "github.com/wricardo/pathwalk"
)

// GrepTool searches text using grep patterns.
// Requires the `grep` binary to be installed and available in PATH.
type GrepTool struct{}

// AsTools returns the grep tool.
func (GrepTool) AsTools() []pathwalk.Tool {
	return []pathwalk.Tool{
		{
			Name:        "grep",
			Description: "Search for lines matching a pattern in text. Use standard grep patterns or regex.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{
						"type":        "string",
						"description": "The text to search in",
					},
					"pattern": map[string]any{
						"type":        "string",
						"description": "The grep pattern or regex to search for, e.g. 'error|warning' or '^\\[ERROR\\]'",
					},
					"flags": map[string]any{
						"type":        "string",
						"description": "Optional grep flags like '-i' (case insensitive), '-v' (invert match), '-n' (show line numbers). Space-separated.",
					},
				},
				"required": []string{"text", "pattern"},
			},
			Fn: grepExecute,
		},
	}
}

func grepExecute(ctx context.Context, args map[string]any) (any, error) {
	text, ok := args["text"].(string)
	if !ok || text == "" {
		return "", nil // empty text = no matches
	}

	pattern, ok := args["pattern"].(string)
	if !ok || pattern == "" {
		return nil, fmt.Errorf("grep: 'pattern' argument must be a non-empty string")
	}

	var cmdArgs []string
	if flags, ok := args["flags"].(string); ok && flags != "" {
		// Split flags on whitespace and add to command
		cmdArgs = strings.Fields(flags)
	}
	cmdArgs = append(cmdArgs, pattern)

	cmd := exec.CommandContext(ctx, "grep", cmdArgs...)
	cmd.Stdin = strings.NewReader(text)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// grep exit code 1 = no matches (not an error condition)
		exitErr, ok := err.(*exec.ExitError)
		if ok && exitErr.ExitCode() == 1 {
			return "", nil // no matches found
		}
		// Other exit codes (2+) are real errors
		stderrMsg := stderr.String()
		if stderrMsg != "" {
			return nil, fmt.Errorf("grep: %s", stderrMsg)
		}
		return nil, fmt.Errorf("grep: command failed: %w", err)
	}

	return stdout.String(), nil
}
