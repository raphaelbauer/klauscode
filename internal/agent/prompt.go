package agent

import (
	"strings"

	"klauscode/internal/tools"
)

// promptHeader and promptFooter wrap the dynamically rendered tool list. The
// body is the exact ReAct contract the harness relies on: the model emits
// Thought/Action lines, the harness supplies Observation lines, and the model
// finishes with a Final Answer.
const promptHeader = `You are a goal-oriented AI agent. You solve tasks by iterating through a loop of Thought, Action, and Observation.

You have access to the following tools:
`

const promptFooter = `
CRITICAL FORMAT RULES:
You must strictly follow this exact format for every turn. Do not skip steps.

Thought: [Reason about what you need to do next]
Action: [tool_name]([arguments])
Observation: [Do not write this yourself. The system will provide this.]

When you have the final answer to the user's request, use this format:
Thought: I have found the answer.
Final Answer: [Your definitive response to the user]

Let's begin.`

// BuildSystemPrompt renders the system prompt, listing the registered tools so
// adding a tool automatically updates the instructions the model sees.
func BuildSystemPrompt(reg *tools.Registry) string {
	var b strings.Builder
	b.WriteString(promptHeader)
	for _, t := range reg.List() {
		b.WriteString("- ")
		b.WriteString(t.Description())
		b.WriteString("\n")
	}
	b.WriteString(promptFooter)
	return b.String()
}
