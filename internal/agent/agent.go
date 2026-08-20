// Package agent runs the ReAct loop: it prompts the model, parses the Action it
// emits, executes the matching tool, feeds the result back as an Observation,
// and repeats until the model returns a Final Answer.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"klauscode/internal/llm"
	"klauscode/internal/tools"
)

// ToolCalling selects how the agent invokes tools.
//
//   - ToolCallingNative uses the provider's structured function-calling: the model
//     returns machine-readable tool_calls, which removes the free-text Action
//     parsing entirely. This is the default and the most reliable path.
//   - ToolCallingText uses the original ReAct text protocol (nonce labels, stop
//     sequence, ParseStep, nudges). It is the fallback for models/servers without
//     native tool-call support.
//   - ToolCallingAuto tries native first and falls back to text if the server
//     rejects the request (e.g. it does not implement the tools field).
type ToolCalling string

const (
	ToolCallingNative ToolCalling = "native"
	ToolCallingText   ToolCalling = "text"
	ToolCallingAuto   ToolCalling = "auto"
)

// defaultMaxSteps bounds the loop so a confused model cannot run forever. Coding
// workflows (read → edit → run tests → re-edit) take more turns than a one-shot
// calculation, so the default is generous; the limit still backstops a runaway.
const defaultMaxSteps = 1000

// defaultMaxRepeats is the repetition count at which a run is aborted: with 3,
// the first and second repeats of an identical tool call are answered with a
// warning (giving the model two chances to break out) and the third ends the
// run. See repeatGuard for why a model gets stuck this way.
const defaultMaxRepeats = 3

// repeatGuard detects a model stuck re-issuing an identical tool call.
//
// Greedy decoding (the client pins temperature to 0 for deterministic tool use)
// makes a turn a deterministic function of its context, and every identical
// (call, result) pair appended to the history makes the same call more likely
// next turn — a self-reinforcing repetition attractor. Once in it, the model
// cannot escape on its own, and the maxSteps backstop is far too loose to help.
//
// The check is deliberately the simplest thing that catches this: one previous
// call signature and a plain string comparison, no hashing and no history scan,
// because the failure mode is literal back-to-back repetition.
type repeatGuard struct {
	last    string // previous call's signature: name + args
	repeats int    // consecutive re-requests of last (0 = first time seen)
}

// observe records a call and returns how many times in a row it has now been
// repeated: 0 the first time it is seen, 1 on the first repetition, and so on.
func (g *repeatGuard) observe(name, args string) int {
	// The separator cannot appear in a tool name, so no two distinct calls can
	// collide on the same signature.
	sig := name + "\x00" + args
	if sig == g.last {
		g.repeats++
		return g.repeats
	}
	g.last, g.repeats = sig, 0
	return 0
}

// repeatWarning is fed back in place of a repeated tool call's result. It says
// plainly what happened and what to do instead: the model is not reasoning about
// a new observation, it is echoing one already in its context, so the way out is
// to answer or to act differently — never to try the same call again. The
// "Error:" prefix matches the self-correcting convention runTool already uses for
// tool failures, which models react to more reliably than neutral prose.
func repeatWarning(name string, repeats int) string {
	return fmt.Sprintf("Error: you have now requested this exact %s call %d times in a row with identical arguments. It was NOT run again — the result of the previous run is already above and has not changed. Repeating it cannot make progress. Either use that result to answer now, or take a different action (a different tool, or different arguments).", name, repeats+1)
}

// newLabelNonce returns a short random hex id used to suffix the ReAct control
// labels (Action/Observation/…). The Observation label becomes the stop sequence,
// so making it unguessable means content the model writes — even a file that
// quotes the literal word "Observation:" — cannot collide with it and truncate
// generation mid-tool-call. Mirrors textutil's nonce for untrusted web content.
func newLabelNonce() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is unexpected; an empty nonce degrades to the bare
		// labels (the pre-nonce behavior), which still works.
		return ""
	}
	return hex.EncodeToString(b[:])
}

// observationStop is the stop sequence: the model's hallucinated observation
// begins with this nonced label, so generation halts there and the harness
// regains control. Only the nonced form is sent — a bare "Observation:" inside
// generated content no longer matches.
func observationStop(nonce string) string { return "Observation" + nonce + ":" }

// nudgeMessage is fed back as an observation when a turn carries neither a valid
// Action nor a Final Answer, telling the model how to finish or how to call a
// tool so it can self-correct on the next turn. The labels carry the run nonce so
// the nudge reinforces the exact format.
func nudgeMessage(nonce string) string {
	return fmt.Sprintf(`Observation%[1]s: No valid Action found. If the task is complete, write your reply on a line beginning with "Final Answer%[1]s:". Otherwise respond with a single Action line as the last line of your turn.`, nonce)
}

// actionFormatNudge is used in place of nudgeMessage when a turn carries an
// Action token the parser could not honor (a malformed call). It teaches the
// exact format — including the JSON-argument shape for write_file/edit_file — so
// the model can self-correct rather than guess again at the generic nudge.
func actionFormatNudge(nonce string) string {
	return fmt.Sprintf(`Observation%[1]s: Your Action could not be parsed. Write the call as a single "Action%[1]s: tool_name(arguments)" that is the LAST thing in your turn. For write_file/edit_file the argument is a JSON object, e.g. Action%[1]s: write_file({"path": "file.txt", "content": "line one\nline two"}); the JSON may span multiple lines but newlines inside string values must be escaped as \n.`, nonce)
}

// emptyResultPlaceholder stands in for a tool result that is the empty string,
// so every observation fed back to the model carries visible content.
const emptyResultPlaceholder = "(no output)"

// Agent drives a single task to completion through the ReAct loop.
type Agent struct {
	client        llm.Client
	tools         *tools.Registry
	system        string // text-path system prompt (ReAct format contract)
	systemNative  string // native-path system prompt (no ReAct scaffolding)
	instructions  string // optional user/project instructions injected into the prompt
	skillsCatalog string // optional Agent Skills catalog injected into the prompt
	nonce         string // per-run id suffixed to ReAct labels; "" means bare labels
	toolCalling   ToolCalling
	maxSteps      int
	maxRepeats    int       // consecutive identical tool calls tolerated before aborting
	trace         io.Writer // optional; receives each turn for visibility
}

// Option configures an Agent.
type Option func(*Agent)

// WithMaxSteps overrides the maximum number of loop iterations.
func WithMaxSteps(n int) Option {
	return func(a *Agent) {
		if n > 0 {
			a.maxSteps = n
		}
	}
}

// WithMaxRepeats overrides how many consecutive identical tool calls are
// tolerated before the run is aborted (see repeatGuard). Each repeat before the
// limit is answered with a warning instead of running the tool.
func WithMaxRepeats(n int) Option {
	return func(a *Agent) {
		if n > 0 {
			a.maxRepeats = n
		}
	}
}

// WithTrace writes each model turn and observation to w for visibility.
func WithTrace(w io.Writer) Option {
	return func(a *Agent) { a.trace = w }
}

// WithInstructions injects user/project instructions (e.g. from AGENTS.md /
// CLAUDE.md, see LoadInstructions) into the system prompt.
func WithInstructions(s string) Option {
	return func(a *Agent) { a.instructions = s }
}

// WithSkills injects an Agent Skills catalog (see skills.Catalog) into the system
// prompt. The catalog lists each skill's name and description; the model loads a
// skill's full instructions on demand via the skill tool.
func WithSkills(catalog string) Option {
	return func(a *Agent) { a.skillsCatalog = catalog }
}

// WithLabelNonce fixes the per-run nonce suffixed to the ReAct labels (it is
// otherwise random). Intended for tests that need deterministic labels.
func WithLabelNonce(nonce string) Option {
	return func(a *Agent) { a.nonce = nonce }
}

// WithToolCalling selects how the agent invokes tools (see ToolCalling). The
// zero value / an empty mode defaults to ToolCallingNative.
func WithToolCalling(mode ToolCalling) Option {
	return func(a *Agent) { a.toolCalling = mode }
}

// New builds an Agent. The system prompt is rendered from the registry so it
// always reflects the tools actually available; it is built after options are
// applied so WithInstructions and the label nonce can feed it.
func New(client llm.Client, reg *tools.Registry, opts ...Option) *Agent {
	a := &Agent{
		client:     client,
		tools:      reg,
		maxSteps:   defaultMaxSteps,
		maxRepeats: defaultMaxRepeats,
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.nonce == "" {
		a.nonce = newLabelNonce()
	}
	if a.toolCalling == "" {
		a.toolCalling = ToolCallingNative
	}
	// The text prompt is always built: it backs both the text mode and the auto
	// mode's fallback. The native prompt is built only when a native path may run.
	a.system = BuildSystemPrompt(reg, a.skillsCatalog, a.instructions, a.nonce)
	if a.toolCalling != ToolCallingText {
		a.systemNative = BuildNativeSystemPrompt(reg, a.skillsCatalog, a.instructions)
	}
	return a
}

// Run executes the task and returns the model's final answer, dispatching on the
// configured tool-calling mode.
func (a *Agent) Run(ctx context.Context, task string) (string, error) {
	switch a.toolCalling {
	case ToolCallingText:
		return a.runText(ctx, task)
	case ToolCallingAuto:
		answer, startedOK, err := a.runNative(ctx, task)
		// Fall back to the text path only when the very first native request was
		// rejected by the server (a *llm.StatusError) — the signal that it does not
		// implement the tools field. A transport error, or a failure after the run
		// has already progressed, is a genuine error and is surfaced as-is.
		if err != nil && !startedOK {
			var se *llm.StatusError
			if errors.As(err, &se) {
				a.tracef("--- native tool-calling unsupported (%v); falling back to text mode ---\n", err)
				return a.runText(ctx, task)
			}
		}
		return answer, err
	default: // ToolCallingNative
		answer, _, err := a.runNative(ctx, task)
		return answer, err
	}
}

// runText executes the task using the original ReAct text protocol: the model
// emits Action/Final Answer labels which ParseStep extracts, malformed turns are
// steered with nudges, and the nonced Observation label is the stop sequence.
func (a *Agent) runText(ctx context.Context, task string) (string, error) {
	messages := []llm.Message{
		{Role: "system", Content: a.system},
		{Role: "user", Content: task},
	}

	// consecutiveMisses counts turns in a row that carried neither an Action nor a
	// Final Answer; it resets whenever the model makes a tool call. candidateFinal
	// holds the most recent *substantive* such turn, so a later empty turn (often
	// the model's reply to the nudge once it considers itself done) cannot
	// overwrite a good prose answer it already produced.
	consecutiveMisses := 0
	candidateFinal := ""

	// guard is run-scoped, not Agent state: an Agent is reusable across Run calls
	// and must not carry one run's history into the next.
	var guard repeatGuard

	stop := observationStop(a.nonce)
	obsLabel := "Observation" + a.nonce + ": "

	for i := 0; i < a.maxSteps; i++ {
		resp, err := a.client.Complete(ctx, llm.Request{Messages: messages, Stop: []string{stop}})
		if err != nil {
			return "", fmt.Errorf("model call failed: %w", err)
		}
		output := resp.Content
		a.tracef("--- model turn %d ---\n%s\n", i+1, output)
		messages = append(messages, llm.Message{Role: "assistant", Content: output})

		step := ParseStep(output)

		if step.HasFinal {
			return step.FinalAnswer, nil
		}

		if step.HasAction {
			consecutiveMisses = 0
			candidateFinal = ""
			observation, err := a.runToolGuarded(&guard, step, step.ToolArgs)
			if err != nil {
				return "", err
			}
			a.tracef("%s%s\n", obsLabel, observation)
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: obsLabel + observation,
			})
			continue
		}

		// The turn has neither a parseable Action nor a Final Answer. Two cases:

		// (a) It carries an "Action:" token, so the model tried to call a tool but
		// malformed it. That is never a final answer — returning the raw "Action: …"
		// text to the user would be wrong — so always steer it with the
		// format-specific nudge and let it retry, without touching candidateFinal
		// (else a following empty turn could resurface the malformed scaffolding).
		// The maxSteps backstop stops a model that can never get the format right.
		if actionTokenRe.MatchString(output) {
			messages = append(messages, llm.Message{Role: "user", Content: actionFormatNudge(a.nonce)})
			continue
		}

		// (b) No "Action:" token: the model is addressing the user, not calling a
		// tool. In a ReAct loop a turn with no tool call is the final response
		// (small models routinely omit the "Final Answer:" prefix). Nudge once to
		// give a malformed answer a chance to reformat — remembering the prose so a
		// follow-up empty turn cannot lose it — and return it on the second
		// consecutive miss instead of looping to the step limit.
		prose := stripThoughtPrefix(output)
		if prose == "" {
			// An empty turn carries no answer. If the model already produced a
			// substantive prose reply on an earlier turn, return that rather than
			// discarding it; otherwise nudge it to produce something usable.
			if candidateFinal != "" {
				return candidateFinal, nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: nudgeMessage(a.nonce)})
			continue
		}
		candidateFinal = prose
		consecutiveMisses++
		if consecutiveMisses == 1 {
			messages = append(messages, llm.Message{Role: "user", Content: nudgeMessage(a.nonce)})
			continue
		}
		return prose, nil
	}

	return "", fmt.Errorf("reached step limit (%d) without a final answer", a.maxSteps)
}

// runNative executes the task using the provider's native function-calling. Each
// turn either returns tool_calls (which we execute and feed back as role:"tool"
// messages) or returns plain content — and content with no tool_calls is, by
// definition, the model's final answer. There is no text parsing, nonce, stop
// sequence, or nudge machinery on this path.
//
// startedOK reports whether at least one model response was received; the caller
// (auto mode) uses it to distinguish a server that rejected the very first
// request — a candidate for the text fallback — from a mid-run failure.
func (a *Agent) runNative(ctx context.Context, task string) (answer string, startedOK bool, err error) {
	specs := toolSpecs(a.tools)
	messages := []llm.Message{
		{Role: "system", Content: a.systemNative},
		{Role: "user", Content: task},
	}

	// guard is run-scoped, not Agent state: an Agent is reusable across Run calls
	// and must not carry one run's history into the next.
	var guard repeatGuard

	for i := 0; i < a.maxSteps; i++ {
		resp, err := a.client.Complete(ctx, llm.Request{Messages: messages, Tools: specs})
		if err != nil {
			return "", startedOK, fmt.Errorf("model call failed: %w", err)
		}
		startedOK = true
		a.tracef("--- model turn %d ---\n", i+1)

		if len(resp.ToolCalls) == 0 {
			// No tool calls: the model is addressing the user, so this is the answer.
			a.tracef("%s\n", resp.Content)
			return resp.Content, startedOK, nil
		}

		// Echo the assistant's tool-call turn back into history so the following
		// role:"tool" results are correctly paired by tool_call_id.
		messages = append(messages, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})
		for _, tc := range resp.ToolCalls {
			args := a.tools.MapToolCallArgs(tc.Name, tc.Arguments)
			a.tracef("--- tool call: %s(%s) ---\n", tc.Name, tc.Arguments)
			// The signature is the RAW arguments JSON, i.e. exactly what the model
			// emitted, not the mapped form.
			step := Step{ToolName: tc.Name, ToolArgs: args}
			observation, err := a.runToolGuarded(&guard, step, tc.Arguments)
			if err != nil {
				return "", startedOK, err
			}
			a.tracef("--- result ---\n%s\n", observation)
			// A skipped call still gets its role:"tool" message: every tool_call_id in
			// an assistant turn must be answered or the provider rejects the next
			// request. Only the content differs.
			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: tc.ID,
				Name:       tc.Name,
				Content:    observation,
			})
		}
	}

	return "", startedOK, fmt.Errorf("reached step limit (%d) without a final answer", a.maxSteps)
}

// toolSpecs builds the native function-call specs from the registry. Only tools
// that expose a JSON Schema (implement tools.Schematic) are offered; a tool
// without one is silently skipped on the native path.
func toolSpecs(reg *tools.Registry) []llm.ToolSpec {
	var specs []llm.ToolSpec
	for _, t := range reg.List() {
		schema, ok := reg.Schema(t.Name())
		if !ok {
			continue
		}
		specs = append(specs, llm.ToolSpec{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  schema,
		})
	}
	return specs
}

// runToolGuarded runs step's tool unless the model has just requested the exact
// same call — see repeatGuard for why that happens and why it cannot recover on
// its own. sigArgs is the argument text the repeat is judged on: the raw
// arguments the model emitted, which differs from step.ToolArgs on the native
// path (where the latter has already been mapped for Call).
//
// On a repeat the tool is NOT run: its output is already verbatim in the model's
// context and cannot have changed, so re-running only costs time and feeds the
// pattern. The warning takes the result's place, giving the model something new
// to react to. A model that repeats anyway past a.maxRepeats ends the run with an
// error, which is far more diagnostic than spinning out the step limit.
func (a *Agent) runToolGuarded(guard *repeatGuard, step Step, sigArgs string) (string, error) {
	repeats := guard.observe(step.ToolName, sigArgs)
	if repeats == 0 {
		return a.runTool(step), nil
	}
	if repeats >= a.maxRepeats {
		return "", fmt.Errorf("model repeated the same %s call %d times in a row without making progress", step.ToolName, repeats+1)
	}
	a.tracef("--- repeated tool call: %s (not run) ---\n", step.ToolName)
	return repeatWarning(step.ToolName, repeats), nil
}

// runTool executes the requested tool and returns the text to feed back as the
// observation. Tool errors become the observation so the model can self-correct
// on the next turn instead of failing the whole run.
func (a *Agent) runTool(step Step) string {
	result, err := a.tools.Execute(step.ToolName, step.ToolArgs)
	if err != nil {
		return "Error: " + err.Error()
	}
	if result == "" {
		// A tool can legitimately succeed with no output (a grep that matched
		// nothing, a build that printed nothing). Say so explicitly: a blank
		// observation reads to the model as a broken tool, and on the native path
		// it would also produce a role:"tool" message with empty content, which
		// providers reject.
		return emptyResultPlaceholder
	}
	return result
}

func (a *Agent) tracef(format string, args ...any) {
	if a.trace != nil {
		fmt.Fprintf(a.trace, format, args...)
	}
}
