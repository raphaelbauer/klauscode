// Package agent runs the ReAct loop: it prompts the model, parses the Action it
// emits, executes the matching tool, feeds the result back as an Observation,
// and repeats until the model returns a Final Answer.
package agent

import (
	"context"
	"fmt"
	"io"
	"strings"

	"klauscode/internal/llm"
	"klauscode/internal/tools"
)

// observationStop halts model generation right before it would write an
// observation, handing control back to the harness so it can run the tool.
const observationStop = "Observation:"

// defaultMaxSteps bounds the loop so a confused model cannot run forever. Coding
// workflows (read → edit → run tests → re-edit) take more turns than a one-shot
// calculation, so the default is generous; the limit still backstops a runaway.
const defaultMaxSteps = 1000

// nudgeMessage is fed back as an observation when a turn carries neither a valid
// Action nor a Final Answer, telling the model how to finish or how to call a
// tool so it can self-correct on the next turn.
const nudgeMessage = `Observation: No valid Action found. If the task is complete, write your reply on a line beginning with "Final Answer:". Otherwise respond with a single Action line as the last line of your turn.`

// actionFormatNudge is used in place of nudgeMessage when a turn carries an
// "Action:" token the parser could not honor (a malformed call). It teaches the
// exact format — including the JSON-argument shape for write_file/edit_file — so
// the model can self-correct rather than guess again at the generic nudge.
const actionFormatNudge = `Observation: Your Action could not be parsed. Write the call as a single "Action: tool_name(arguments)" that is the LAST thing in your turn. For write_file/edit_file the argument is a JSON object, e.g. Action: write_file({"path": "file.txt", "content": "line one\nline two"}); the JSON may span multiple lines but newlines inside string values must be escaped as \n.`

// Agent drives a single task to completion through the ReAct loop.
type Agent struct {
	client        llm.Client
	tools         *tools.Registry
	system        string
	instructions  string // optional user/project instructions injected into the prompt
	skillsCatalog string // optional Agent Skills catalog injected into the prompt
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

// New builds an Agent. The system prompt is rendered from the registry so it
// always reflects the tools actually available; it is built after options are
// applied so WithInstructions can feed it.
func New(client llm.Client, reg *tools.Registry, opts ...Option) *Agent {
	a := &Agent{
		client:   client,
		tools:    reg,
		maxSteps: defaultMaxSteps,
	}
	for _, opt := range opts {
		opt(a)
	}
	a.system = BuildSystemPrompt(reg, a.skillsCatalog, a.instructions)
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

	for i := 0; i < a.maxSteps; i++ {
		output, err := a.client.Complete(ctx, messages, []string{observationStop})
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
			a.tracef("Observation: %s\n", observation)
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: "Observation: " + observation,
			})
			continue
		}

		// The turn has no Action and no "Final Answer:" prefix. If it nonetheless
		// contains an "Action:" token, the model tried to call a tool but malformed
		// it, so steer it with the format-specific nudge instead of the generic one.
		nudge := nudgeMessage
		if strings.Contains(output, "Action:") {
			nudge = actionFormatNudge
		}
		prose := stripThoughtPrefix(output)

		if prose == "" {
			// An empty turn carries no answer. If the model already produced a
			// substantive prose reply on an earlier turn, return that rather than
			// discarding it; otherwise nudge it to produce something usable.
			if candidateFinal != "" {
				return candidateFinal, nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: nudge})
			continue
		}

		// Substantive prose with no tool call. The first time, nudge the model
		// toward the format — it may have malformed an action it meant to call, so
		// the nudge gives it one chance to self-correct — but remember the prose so
		// a follow-up empty turn cannot lose it. A second consecutive miss means
		// the model has stopped requesting tools and is just talking to the user:
		// in a ReAct loop a turn with no tool call is, by definition, the final
		// response (small models routinely omit the "Final Answer:" prefix), so we
		// return that prose instead of looping to the step limit.
		candidateFinal = prose
		consecutiveMisses++
		if consecutiveMisses == 1 {
			messages = append(messages, llm.Message{Role: "user", Content: nudge})
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
