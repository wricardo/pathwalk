// Package ctxbuild provides pure functions for assembling structured LLM context.
// It formats session history, facts, team state, and task text into XML-tagged
// sections that LLMs parse reliably. All functions are side-effect-free and
// carry no database or persistence dependencies.
//
// Typical usage:
//
//	history := ctxbuild.FormatSessionHistory(runs, currentRunID, 0)
//	facts := ctxbuild.FormatFacts(agentFacts, customerFacts)
//	task := ctxbuild.AssembleContext("Book an appointment for John", history, facts)
package ctxbuild

import (
	"fmt"
	"strings"
	"time"
)

// DefaultSessionTokenBudget is the token budget applied when callers pass <= 0.
const DefaultSessionTokenBudget = 2000

// RunSummary is a lightweight view of a completed run for session history.
type RunSummary struct {
	ID        string
	Agent     string
	Task      string
	Output    string
	Variables string // JSON-encoded variable map, or empty
	StartedAt time.Time
	Completed bool
}

// FactEntry is a single key-value fact.
type FactEntry struct {
	Key   string
	Value string
}

// TeamMsg is a single team message.
type TeamMsg struct {
	From    string
	Content string
	Read    bool
}

// TeamTask is a single team task.
type TeamTask struct {
	ID         string
	Status     string
	AssignedTo string // empty = unassigned
	Title      string
}

// EstimateTokens returns a rough token count (~4 characters per token).
func EstimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	return (len(s) + 3) / 4 // ceiling division
}

// FormatSessionHistory formats completed runs as an XML-tagged block, keeping
// the most recent runs within tokenBudget. Runs are output in chronological
// order (oldest first). If tokenBudget <= 0, DefaultSessionTokenBudget is used.
//
// excludeID skips the run with that ID (typically the current run being built).
func FormatSessionHistory(runs []RunSummary, excludeID string, tokenBudget int) string {
	if tokenBudget <= 0 {
		tokenBudget = DefaultSessionTokenBudget
	}

	var filtered []RunSummary
	for _, r := range runs {
		if r.ID == excludeID || !r.Completed {
			continue
		}
		filtered = append(filtered, r)
	}
	if len(filtered) == 0 {
		return ""
	}

	entries := make([]string, len(filtered))
	for i, r := range filtered {
		entries[i] = formatRunEntry(r)
	}

	// Walk from most recent to oldest, accumulate within budget.
	used := EstimateTokens("<session-history>\n</session-history>")
	keepFrom := len(entries)
	for i := len(entries) - 1; i >= 0; i-- {
		cost := EstimateTokens(entries[i])
		if used+cost > tokenBudget {
			break
		}
		used += cost
		keepFrom = i
	}

	omitted := keepFrom
	var sb strings.Builder
	sb.WriteString("<session-history>\n")
	if omitted > 0 {
		fmt.Fprintf(&sb, "<!-- %d earlier run(s) omitted -->\n", omitted)
	}
	for i := keepFrom; i < len(entries); i++ {
		sb.WriteString(entries[i])
	}
	sb.WriteString("</session-history>")
	return sb.String()
}

func formatRunEntry(r RunSummary) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<run time=%q agent=%q>\n", r.StartedAt.Format("2006-01-02 15:04"), r.Agent)
	fmt.Fprintf(&sb, "<task>%s</task>\n", r.Task)
	if r.Output != "" {
		fmt.Fprintf(&sb, "<output>%s</output>\n", r.Output)
	}
	if r.Variables != "" {
		fmt.Fprintf(&sb, "<vars>%s</vars>\n", r.Variables)
	}
	sb.WriteString("</run>\n")
	return sb.String()
}

// FormatFacts formats agent-level and customer-level facts as XML sections.
// Returns "" if both slices are empty.
func FormatFacts(agentFacts, customerFacts []FactEntry) string {
	if len(agentFacts) == 0 && len(customerFacts) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(agentFacts) > 0 {
		sb.WriteString("<agent-memory>\n")
		for _, f := range agentFacts {
			fmt.Fprintf(&sb, "<fact key=%q>%s</fact>\n", f.Key, f.Value)
		}
		sb.WriteString("</agent-memory>")
	}
	if len(customerFacts) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("<customer-memory>\n")
		for _, f := range customerFacts {
			fmt.Fprintf(&sb, "<fact key=%q>%s</fact>\n", f.Key, f.Value)
		}
		sb.WriteString("</customer-memory>")
	}
	return sb.String()
}

// FormatTeamContext formats unread team messages and current tasks as XML.
// Only unread messages are included. Returns "" if there are no unread messages
// and no tasks.
func FormatTeamContext(msgs []TeamMsg, tasks []TeamTask) string {
	var unread []TeamMsg
	for _, m := range msgs {
		if !m.Read {
			unread = append(unread, m)
		}
	}
	if len(unread) == 0 && len(tasks) == 0 {
		return ""
	}

	var sb strings.Builder
	if len(unread) > 0 {
		sb.WriteString("<team-messages>\n")
		for _, m := range unread {
			fmt.Fprintf(&sb, "<message from=%q>%s</message>\n", m.From, m.Content)
		}
		sb.WriteString("</team-messages>")
	}
	if len(tasks) > 0 {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString("<team-tasks>\n")
		for _, t := range tasks {
			assignee := t.AssignedTo
			if assignee == "" {
				assignee = "unassigned"
			}
			fmt.Fprintf(&sb, "<task id=%q status=%q assigned-to=%q>%s</task>\n", t.ID, t.Status, assignee, t.Title)
		}
		sb.WriteString("</team-tasks>")
	}
	return sb.String()
}

// AssembleContext combines non-empty context sections with the task.
// Sections are joined with double newlines. The task is wrapped in <task> tags
// and placed last so it has recency bias in the LLM's attention.
func AssembleContext(task string, sections ...string) string {
	var parts []string
	for _, s := range sections {
		if s != "" {
			parts = append(parts, s)
		}
	}
	parts = append(parts, "<task>\n"+task+"\n</task>")
	return strings.Join(parts, "\n\n")
}
