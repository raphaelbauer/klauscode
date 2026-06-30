// Package agent runs the ReAct loop: it prompts the model, parses the Action it
// emits, executes the matching tool, feeds the result back as an Observation,
// and repeats until the model returns a Final Answer.
package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"klauscode/internal/llm"
	"klauscode/internal/tools"
)

// defaultMaxSteps bounds the loop so a confused model cannot run forever. Coding
// workflows (read → edit → run tests → re-edit) take more turns than a one-shot
// calculation, so the default is generous; the limit still backstops a runaway.
const defaultMaxSteps = 1000

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

// Agent drives a single task to completion through the ReAct loop.
type Agent struct {
	client        llm.Client
	tools         *tools.Registry
	system        string
	instructions  string // optional user/project instructions injected into the prompt
	skillsCatalog string // optional Agent Skills catalog injected into the prompt
	nonce         string // per-run id suffixed to ReAct labels; "" means bare labels
	maxSteps      int
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

// New builds an Agent. The system prompt is rendered from the registry so it
// always reflects the tools actually available; it is built after options are
// applied so WithInstructions and the label nonce can feed it.
func New(client llm.Client, reg *tools.Registry, opts ...Option) *Agent {
	a := &Agent{
		client:   client,
		tools:    reg,
		maxSteps: defaultMaxSteps,
	}
	for _, opt := range opts {
		opt(a)
	}
	if a.nonce == "" {
		a.nonce = newLabelNonce()
	}
	a.system = BuildSystemPrompt(reg, a.skillsCatalog, a.instructions, a.nonce)
	return a
}

// Run executes the task and returns the model's final answer.
func (a *Agent) Run(ctx context.Context, task string) (string, error) {
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

	stop := observationStop(a.nonce)
	obsLabel := "Observation" + a.nonce + ": "

	for i := 0; i < a.maxSteps; i++ {
		output, err := a.client.Complete(ctx, messages, []string{stop})
		if err != nil {
			return "", fmt.Errorf("model call failed: %w", err)
		}
		a.tracef("--- model turn %d ---\n%s\n", i+1, output)
		messages = append(messages, llm.Message{Role: "assistant", Content: output})

		step := ParseStep(output)

		if step.HasFinal {
			return step.FinalAnswer, nil
		}

		if step.HasAction {
			consecutiveMisses = 0
			candidateFinal = ""
			observation := a.runTool(step)
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

// runTool executes the requested tool and returns the text to feed back as the
// observation. Tool errors become the observation so the model can self-correct
// on the next turn instead of failing the whole run.
func (a *Agent) runTool(step Step) string {
	result, err := a.tools.Execute(step.ToolName, step.ToolArgs)
	if err != nil {
		return "Error: " + err.Error()
	}
	return result
}

func (a *Agent) tracef(format string, args ...any) {
	if a.trace != nil {
		fmt.Fprintf(a.trace, format, args...)
	}
}
