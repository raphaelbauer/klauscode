package agent

import (
	"regexp"
	"strings"
)

// Step is the parsed result of a single model turn. A turn either ends the run
// with a final answer or requests a tool via an action; both flags may be false
// if the model produced neither (a malformed turn).
type Step struct {
	HasFinal    bool
	FinalAnswer string

	HasAction bool
	ToolName  string
	ToolArgs  string
}

var (
	// finalRe captures everything after "Final Answer:" to end of input.
	finalRe = regexp.MustCompile(`(?s)Final Answer:\s*(.*)`)
	// actionRe captures the tool name and the raw argument text inside the
	// parentheses of an Action line.
	actionRe = regexp.MustCompile(`Action:\s*([A-Za-z_]\w*)\s*\((.*)\)`)
)

// ParseStep extracts the action and/or final answer from a model turn.
//
// A final answer takes precedence: if the model wrote one we ignore any action,
// since the run is over. Otherwise we look for the last Action line in the turn
// (the most recent intent if the model rambled).
func ParseStep(output string) Step {
	var step Step

	if m := finalRe.FindStringSubmatch(output); m != nil {
		step.HasFinal = true
		step.FinalAnswer = strings.TrimSpace(m[1])
		return step
	}

	if matches := actionRe.FindAllStringSubmatch(output, -1); len(matches) > 0 {
		last := matches[len(matches)-1]
		step.HasAction = true
		step.ToolName = last[1]
		step.ToolArgs = unquote(strings.TrimSpace(last[2]))
	}

	return step
}

// unquote strips a single matched pair of surrounding single or double quotes.
// Models routinely wrap string arguments in quotes (e.g. calculate("12 * 9"))
// because the tool signature reads as a string parameter; those quotes are not
// part of the argument value, so the harness removes them before dispatch.
func unquote(s string) string {
	if len(s) >= 2 {
		q := s[0]
		if (q == '"' || q == '\'') && s[len(s)-1] == q {
			// Only strip a genuine enclosing pair: if the quote also appears
			// inside, these are not wrapping quotes (e.g. `"a" or "b"`).
			if !strings.ContainsRune(s[1:len(s)-1], rune(q)) {
				return s[1 : len(s)-1]
			}
		}
	}
	return s
}
