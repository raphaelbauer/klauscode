package agent

import (
	"regexp"
	"strings"

	"klauscode/internal/tools/textutil"
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
	// finalRe captures everything after a "Final Answer:" label to the end of the
	// turn. It is deliberately lenient because small local models render the label
	// inconsistently: it is case-insensitive, tolerates surrounding markdown
	// emphasis (e.g. **Final Answer:**), and allows extra whitespace between the
	// two words and before the colon. The multiline `^` anchor keeps it from
	// matching the phrase mid-sentence inside a Thought, and the `s` flag lets a
	// multi-line answer be captured to the end of input.
	finalRe = regexp.MustCompile(`(?ims)^\s*\**\s*final\s+answer\s*\**\s*:\s*\**\s*(.*)`)
	// actionRe matches a whole line that is an Action and captures the tool name
	// and the raw argument text inside the parentheses. It is anchored to the
	// full line (^…$) so an "Action: …" that merely appears inside prose — e.g.
	// a documentation example the model writes in backticks — cannot match.
	actionRe = regexp.MustCompile(`^Action:\s*([A-Za-z_]\w*)\s*\((.*)\)\s*$`)
	// actionOpenRe matches the start of an Action up to its opening parenthesis,
	// capturing the tool name. Unlike actionRe it does NOT anchor the closing
	// paren to the same line, so the argument may span multiple physical lines
	// (pretty-printed or fenced JSON, a heredoc). The multiline `^` lets it find an
	// opener anywhere; findActionBlock scans for the LAST one so a trailing real
	// call wins over an earlier documentation example.
	actionOpenRe = regexp.MustCompile(`(?m)^\s*Action:\s*([A-Za-z_]\w*)\s*\(`)
)

// ParseStep extracts the action and/or final answer from a model turn.
//
// A final answer takes precedence: if the model wrote one we ignore any action,
// since the run is over. Otherwise an Action is honored only when it is the
// model's final non-empty line. The ReAct loop stops generation at
// "Observation:", so a genuine tool call is the last thing the model writes;
// anything after it (more prose, a worked example) means the "Action:" we would
// otherwise match is documentation, not a live request — running it caused the
// `sh: -c: syntax error` observations from prose like "e.g. `Action: bash(ls -R)`".
func ParseStep(output string) Step {
	var step Step

	if m := finalRe.FindStringSubmatch(output); m != nil {
		step.HasFinal = true
		step.FinalAnswer = strings.TrimSpace(m[1])
		return step
	}

	// Prefer the multi-line, quote- and fence-aware extractor: it parses how models
	// actually format tool calls (pretty-printed JSON, a ```json fence, parens
	// inside string values). It only succeeds on a clean, final action, so when it
	// declines we fall back to the original single-line last-line match unchanged —
	// no previously-passing case regresses.
	if name, args, ok := findActionBlock(output); ok {
		step.HasAction = true
		step.ToolName = name
		step.ToolArgs = normalizeArgs(args)
		return step
	}

	if last := lastNonEmptyLine(output); last != "" {
		if m := actionRe.FindStringSubmatch(last); m != nil {
			step.HasAction = true
			step.ToolName = m[1]
			step.ToolArgs = normalizeArgs(strings.TrimSpace(m[2]))
		}
	}

	return step
}

// findActionBlock extracts the model's final tool call, tolerating multi-line
// arguments. It finds every "Action: name(" opener, then scans forward
// quote-aware for the matching ')': depth tracks '(' / ')' but characters inside a
// double-quoted string (honoring \" escapes) are skipped, so parens within a JSON
// value or shell command do not throw off the match. A surrounding code fence
// around the arguments is stripped.
//
// The "an Action is final" guard is preserved: after the closing ')' only
// whitespace and an optional closing code fence may remain. If substantive prose
// follows, the call is a documentation example rather than a live request.
//
// Openers are tried first-to-last, returning the first that satisfies the guard:
//   - A documentation example that PRECEDES the real call fails the guard — the
//     genuine action is substantive content trailing it — so it is skipped and the
//     real call (later) is returned. "A trailing real call wins" still holds.
//   - A line beginning with "Action:" that sits INSIDE an earlier action's quoted
//     argument value (e.g. write_file content documenting this harness) is a bogus
//     opener: actionOpenRe is line-anchored but NOT string-aware, so it matches.
//     The genuine call is the *earlier* opener, and its quote-aware scan correctly
//     consumes the embedded line as part of its argument and reaches the true end,
//     so it passes the guard first — the bogus opener is never reached. Iterating
//     last-to-first would fail here: a scan started mid-value has shifted string
//     parity and can run to the real end, letting the bogus opener win.
func findActionBlock(output string) (name, args string, ok bool) {
	locs := actionOpenRe.FindAllStringSubmatchIndex(output, -1)
	for _, loc := range locs {
		body, rest, found := scanBalancedArgs(output[loc[1]:])
		if !found {
			continue
		}
		// Only whitespace and an optional closing ``` fence may follow the call.
		if leftover := strings.TrimSpace(rest); leftover != "" && leftover != "```" {
			continue
		}
		return output[loc[2]:loc[3]], textutil.StripCodeFence(body), true
	}
	return "", "", false
}

// scanBalancedArgs scans s, which begins just after an Action's opening '(', for
// the matching closing ')'. It is quote-aware: a '(' or ')' inside a double-quoted
// string (with \" escapes) does not change depth, so parens inside argument values
// are ignored. It returns the argument text between the parens and the remainder
// of s after the closing ')'. found is false when no balancing ')' exists (e.g. an
// unterminated string), so the caller can fall back to the single-line parser.
func scanBalancedArgs(s string) (args, rest string, found bool) {
	depth := 1
	inStr := false
	esc := false
	for i, r := range s {
		if inStr {
			switch {
			case esc:
				esc = false
			case r == '\\':
				esc = true
			case r == '"':
				inStr = false
			}
			continue
		}
		switch r {
		case '"':
			inStr = true
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[:i], s[i+1:], true
			}
		}
	}
	return "", "", false
}

// lastNonEmptyLine returns the last line of s that has non-whitespace content,
// trimmed of surrounding whitespace. It returns "" when s is empty or blank. It
// backs the fallback single-line Action match used when findActionBlock declines
// (e.g. an unterminated quote), where a live tool call sits on the final line.
func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

// stripThoughtPrefix removes a leading "Thought:" label from a turn the harness
// is about to return as an implicit final answer (see Agent.Run), so the user
// sees the model's prose without the internal ReAct scaffolding. Only the label
// word is removed; the text after it — which is the actual answer — is kept.
func stripThoughtPrefix(s string) string {
	s = strings.TrimSpace(s)
	if t := strings.TrimPrefix(s, "Thought:"); t != s {
		return strings.TrimSpace(t)
	}
	return s
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
