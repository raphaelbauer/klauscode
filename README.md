# Klaus Code

A small, dependency-free **AI harness** in Go. It drives an OpenAI model through
a [ReAct](https://arxiv.org/abs/2210.03629) loop — **Thought → Action →
Observation → Final Answer** — where the model requests tools as plain text, the
harness intercepts and executes them, feeds the result back, and repeats until
the model produces a final answer.

The proof-of-concept ships one tool, `calculate`, backed by a hand-written
arithmetic evaluator.

## How it works

```
┌────────┐  prompt   ┌─────────┐  Action: calculate(2+3)   ┌──────────┐
│  user  │──────────▶│  agent  │──────────────────────────▶│  tools   │
└────────┘           │ (loop)  │◀──────────────────────────│ registry │
                     └─────────┘  Observation: 5           └──────────┘
                          │  Final Answer: 5
                          ▼
                        stdout
```

1. The agent sends the system prompt + the user's task to the model.
2. The model replies with a `Thought:` and an `Action: tool(args)` line.
   Generation is stopped at `Observation:` so the harness regains control.
3. The harness parses the action, runs the matching tool, and appends
   `Observation: <result>` to the conversation.
4. Steps 2–3 repeat until the model emits `Final Answer:`.

The whole loop lives in [internal/agent/agent.go](internal/agent/agent.go).

## Usage

```sh
export OPENAI_API_KEY=sk-...
# optional, defaults to gpt-4o-mini
export OPENAI_MODEL=gpt-4o-mini

go run ./cmd/klauscode "What is (12 * 9) + 3?"
```

The Thought/Action/Observation trace is printed to **stderr**; the final answer
is printed to **stdout**, so you can capture just the answer:

```sh
go run ./cmd/klauscode "What is 7 * 6?" 2>/dev/null
# 42
```

## Architecture

Layered with interface-based dependency injection so each layer is unit-testable
in isolation. `cmd/klauscode` is the composition root that wires the concrete
implementations together.

| Package          | Responsibility                                              |
| ---------------- | ---------------------------------------------------------- |
| `internal/llm`   | Provider boundary: `Client` interface + OpenAI impl (net/http). |
| `internal/tools` | Action boundary: `Tool` interface, `Registry`, expression evaluator, `calculate`. |
| `internal/agent` | The ReAct loop, the system prompt, and the turn parser.    |
| `cmd/klauscode`     | Reads config from the environment, wires everything, runs the task. |

No third-party dependencies — only the Go standard library.

## Adding a tool

A tool is anything implementing the `Tool` interface
([internal/tools/tool.go](internal/tools/tool.go)):

```go
type Tool interface {
    Name() string        // identifier used in the Action line
    Description() string // one line rendered into the system prompt
    Call(args string) (string, error) // args = raw text inside the parentheses
}
```

1. Implement it (see [internal/tools/calculate.go](internal/tools/calculate.go)
   as a template).
2. Register it in [cmd/klauscode/main.go](cmd/klauscode/main.go):

   ```go
   registry.Register(tools.NewYourTool())
   ```

The system prompt lists registered tools automatically, so the model learns
about the new tool with no prompt edits. Tool errors are fed back to the model
as an observation, letting it self-correct rather than failing the run.

## Testing

```sh
go test ./... -cover
```

Tests run with no network access: the OpenAI client is tested against
`httptest`, and the agent loop is driven by a scripted fake `llm.Client`.

## Limitations (proof-of-concept)

No streaming, retries/backoff, token accounting, conversation persistence, or
concurrency. The `Tool` interface and `Registry` make adding tools the natural
next step.
