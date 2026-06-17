# klauscode — project instructions for Claude

A small, dependency-free ReAct AI harness in Go. Drives an OpenAI model through a
Thought/Action/Observation/Final-Answer loop, executing tool calls on the
model's behalf.

## Architecture

Layered with interface-based DI; `cmd/klauscode` is the composition root.

- `internal/llm` — `Client` interface + OpenAI impl over `net/http`. Override the
  API base URL (ending in `/v1`) via `WithBaseURL`; the client appends
  `/chat/completions`. Used for local servers (LM Studio) and httptest.
- `internal/tools` — `Tool` interface, `Registry`, the recursive-descent
  arithmetic evaluator (`eval.go`), and the `calculate` tool.
- `internal/agent` — the ReAct loop (`agent.go`), the system prompt builder
  (`prompt.go`, renders the tool list from the registry), and the turn parser
  (`parser.go`).
- `cmd/klauscode` — reads `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL`,
  wires, runs. When `OPENAI_BASE_URL` is set, the API key is optional (a
  placeholder is used) so local OpenAI-compatible servers work without a key.

## Conventions

- **Zero third-party dependencies.** Standard library only. Keep it that way
  unless there's a strong reason; `go.mod` has no `require` block.
- Stop sequence `Observation:` hands control back to the harness before the
  model writes an observation itself.
- Tool errors are returned to the model as `Observation: Error: ...` so it can
  self-correct; the run does not abort on a tool error.
- Final answer takes precedence over an action in the same turn.

## Adding a tool

Implement `tools.Tool` and register it in `cmd/klauscode/main.go`. The system
prompt updates automatically from the registry — no prompt edits needed.

## Testing

- `go test ./... -cover` — no network; OpenAI client uses httptest, agent loop
  uses a scripted fake `llm.Client`.
- Style: table-driven, given/when/then comments, lowest level possible.
- Coverage target ≥60%; logic layers currently sit at ~80–92%.

## Verify end-to-end

```sh
export OPENAI_API_KEY=sk-...
go run ./cmd/klauscode "What is (12 * 9) + 3?"   # expect 111, trace on stderr
```
