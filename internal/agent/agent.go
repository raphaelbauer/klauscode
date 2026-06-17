// Package agent runs the ReAct loop: it prompts the model, parses the Action it
// emits, executes the matching tool, feeds the result back as an Observation,
// and repeats until the model returns a Final Answer.
package agent

import (
	"context"
	"fmt"
	"io"

	"klauscode/internal/llm"
	"klauscode/internal/tools"
)

// observationStop halts model generation right before it would write an
// observation, handing control back to the harness so it can run the tool.
const observationStop = "Observation:"

// defaultMaxSteps bounds the loop so a confused model cannot run forever. Coding
// workflows (read → edit → run tests → re-edit) take more turns than a one-shot
// calculation, so the default is generous; the limit still backstops a runaway.
const defaultMaxSteps = 25

// Agent drives a single task to completion through the ReAct loop.
type Agent struct {
	client   llm.Client
	tools    *tools.Registry
	system   string
	maxSteps int
	trace    io.Writer // optional; receives each turn for visibility
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

// New builds an Agent. The system prompt is rendered from the registry so it
// always reflects the tools actually available.
func New(client llm.Client, reg *tools.Registry, opts ...Option) *Agent {
	a := &Agent{
		client:   client,
		tools:    reg,
		system:   BuildSystemPrompt(reg),
		maxSteps: defaultMaxSteps,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Run executes the task and returns the model's final answer.
func (a *Agent) Run(ctx context.Context, task string) (string, error) {
	messages := []llm.Message{
		{Role: "system", Content: a.system},
		{Role: "user", Content: task},
	}

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

		if !step.HasAction {
			// The model broke format. Nudge it back rather than aborting.
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: "Observation: No valid Action found. Respond with either an Action line or a Final Answer.",
			})
			continue
		}

		observation := a.runTool(step)
		a.tracef("Observation: %s\n", observation)
		messages = append(messages, llm.Message{
			Role:    "user",
			Content: "Observation: " + observation,
		})
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
