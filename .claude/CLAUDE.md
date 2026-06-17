# klauscode — project instructions for Claude

A small, dependency-free ReAct AI harness in Go. Drives an OpenAI model through a
Thought/Action/Observation/Final-Answer loop, executing tool calls on the
model's behalf.

## Architecture

Layered with interface-based DI; `cmd/klauscode` is the composition root.

- `internal/llm` — `Client` interface + OpenAI impl over `net/http`. Override the
  API base URL (ending in `/v1`) via `WithBaseURL`; the client appends
  `/chat/completions`. Used for local servers (LM Studio) and httptest.
- `internal/tools` — `Tool` interface and `Registry`, with one package per tool:
  `calculate` (recursive-descent arithmetic evaluator in `eval.go`), `readfile`,
  `writefile`, `editfile`, `bash`, `websearch`, `webfetch`, plus shared text
  helpers in `textutil` (HTML→text, truncation, untrusted-content wrapping).
  Each tool package mirrors `calculate`: a struct, a `New()` constructor, and the
  three `Tool` methods; structural typing means none of them import `tools`.
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
- **Tool argument styles.** Single-arg tools take the raw string inside the
  parentheses (like `calculate`). Multi-arg tools (`write_file`, `edit_file`)
  take a **single-line JSON object** decoded with a tolerant `json.Decoder`;
  JSON escapes newlines as `\n`, so the existing single-line Action parser is
  unchanged. The parser regex is single-line (no `(?s)`), so JSON content must
  stay on one physical line.
- **Single-arg tool descriptions must NOT read like named params.** A signature
  such as `bash(command: str)` makes the model emit `bash(command: "ls -R")`;
  the harness then runs the literal word `command:` (`sh: command:: command not
  found`). Document single-arg tools as `name(<value>)` with a concrete example
  (`bash(ls -R)`), never `name(param: type)`. As a backstop, `normalizeArgs` in
  `parser.go` strips a `name:`/`name=` wrapper **only when the value is quoted**,
  so a real env-var prefix like `FOO=bar ./script` survives. `prompt.go` also
  carries an explicit ARGUMENTS rule + worked example.
- `bash` returns a non-zero exit / timeout as a normal observation (output +
  `[exit code: N]` / `[timed out ...]`), not a Go error — the output is signal
  the model reads to recover.

## Security / prompt injection

The web tools make this a "lethal trifecta" agent. `bash` and the file tools are
**unsandboxed by design** (a path jail is false security; `bash` escapes it).
`web_fetch`/`web_search` wrap their output via `textutil.WrapUntrusted` in
nonce-delimited `[UNTRUSTED WEB CONTENT <id>] … [END …]` markers (forged markers
in the body are stripped first) and the system prompt tells the model to treat
that block as data. This is best-effort defense-in-depth, **not** a guarantee —
the real control is running klauscode sandboxed/least-privilege. Keep these
mitigations when editing the web tools or `prompt.go`.

## Adding a tool

Create a package under `internal/tools/<name>/`, implement `tools.Tool`
(`Name`/`Description`/`Call`), and register it in `cmd/klauscode/main.go`. The
system prompt updates automatically from the registry — no prompt edits needed.
The `Description()` string is the model's only documentation for the tool, so
make it a self-describing signature (and note JSON args / examples where useful).

## Testing

- `go test ./... -cover` — no network; OpenAI client uses httptest, agent loop
  uses a scripted fake `llm.Client`.
- Style: table-driven, given/when/then comments, lowest level possible.
- Coverage target ≥60%; logic layers currently sit at ~80–92%.

## Verify end-to-end

```sh
export OPENAI_API_KEY=sk-...
go run ./cmd/klauscode "What is (12 * 9) + 3?"   # expect 111, trace on stderr

# coding (read/edit/bash) and web research (search/fetch):
go run ./cmd/klauscode "Create hello.go that prints Hello, then run it with go run hello.go"
go run ./cmd/klauscode "Find the Go context package docs and summarize context.WithTimeout"
```

Prefer running coding/web tasks in a sandbox or disposable workspace — see the
README's Security section.
