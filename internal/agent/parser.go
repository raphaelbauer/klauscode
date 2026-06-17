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
		step.ToolArgs = normalizeArgs(strings.TrimSpace(last[2]))
	}

	return step
}

// namedArgRe matches a leading `name:` / `name=` keyword-argument wrapper, e.g.
// the `command:` in `bash(command: "ls -R")`. The captured group is the value.
var namedArgRe = regexp.MustCompile(`^[A-Za-z_]\w*\s*[:=]\s*(.+)$`)

// normalizeArgs turns the raw text inside an Action's parentheses into the value
// a single-arg tool expects. It strips surrounding quotes and, as a backstop,
// the `name:`/`name=` keyword wrapper that models emit by imitating a tool's
// `name(param: type)` signature (the cause of `sh: command:: command not found`).
//
// The wrapper is only stripped when its value is a fully quoted string, so a
// genuine shell command keeps its meaning — an env-var prefix like
// `FOO=bar ./script` has an unquoted value and is left untouched.
func normalizeArgs(s string) string {
	if m := namedArgRe.FindStringSubmatch(s); m != nil {
		if v := unquote(m[1]); v != m[1] {
			return v
		}
	}
	return unquote(s)
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
