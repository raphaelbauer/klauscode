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
  `writefile`, `editfile`, `bash`, `websearch`, `webfetch`, `skill` (serves Agent
  Skill bodies on demand — see below), plus shared text helpers in `textutil`
  (HTML→text, truncation, untrusted-content wrapping). Each tool package mirrors
  `calculate`: a struct, a `New()` constructor, and the three `Tool` methods;
  structural typing means none of them import `tools`.
- `internal/skills` — discovers Agent Skills (`skills/<name>/SKILL.md`) and
  renders the prompt catalog (see Agent Skills below). Standalone, zero-dep.
- `internal/agent` — the ReAct loop (`agent.go`), the system prompt builder
  (`prompt.go`, renders the tool list from the registry **and injects an optional
  Agent Skills catalog + user/project instructions** via `WithSkills` /
  `LoadInstructions`/`WithInstructions`), and the turn parser (`parser.go`).
- `cmd/klauscode` — reads `OPENAI_API_KEY` / `OPENAI_MODEL` / `OPENAI_BASE_URL`,
  wires, runs. When `OPENAI_BASE_URL` is set, the API key is optional (a
  placeholder is used) so local OpenAI-compatible servers work without a key. It
  also resolves `~/.claude` + the cwd and feeds `agent.LoadInstructions` into
  `agent.WithInstructions`, plus `skills.Discover` into the `skill` tool and
  `agent.WithSkills`.

## Instructions files (AGENTS.md / CLAUDE.md)

`prompt.go` loads standing guidance from two scopes and injects it into the
system prompt between the tool list and the footer (so the footer's format rules
and `Let's begin.` stay last):

- **Global** `~/.claude/` and **project** cwd, each trying `AGENTS.md` then
  `CLAUDE.md` (first found wins) via `firstInstructionFile`.
- `LoadInstructions(globalDir, projectDir)` combines them — global first, project
  second under labeled headers — so project instructions take precedence. Missing
  files yield `""` (normal); only a real read error is returned. `New` builds the
  system prompt **after** options are applied so `WithInstructions` can feed it.
- The injected block is marked as **trusted** user instructions, distinct from
  the UNTRUSTED web-content markers — keep that distinction when editing prompts.

## Agent Skills (skills/<name>/SKILL.md)

Skills are named, on-demand capability packets, loaded via **progressive
disclosure** so the prompt stays small even with many installed:

- `skills.Discover(globalDir, projectDir)` scans `<dir>/skills/*/SKILL.md` in two
  scopes: global `~/.claude/skills/` and project `./.claude/skills/` (main.go
  passes `globalDir=~/.claude` and `projectDir=.claude`). A project skill
  **overrides** a global one with the same `name`. A missing `skills/` dir is
  normal (yields nothing); only a real read error aborts. Files lacking a valid
  `name`+`description` are **skipped**, not fatal.
- Each `SKILL.md` has minimal YAML-style frontmatter — `name` + `description`
  only (no third-party YAML; `parseFrontmatter` reads top-level `key: value`
  pairs between `---` fences and trims surrounding quotes). The rest is the body.
- The system prompt lists only `name: description` per skill (via `skills.Catalog`
  → `agent.WithSkills`), injected **between the tool list and the instructions**.
  The model calls `skill(<name>)` (the `skill` tool) to load the full body only
  when it decides the skill is relevant.
- Skill bodies are local, user-authored, and therefore **trusted** content (same
  class as instructions), distinct from the UNTRUSTED web-content markers. A body
  can point the model at bundled files in the skill's `Dir`, which it reads with
  `read_file`.

## Conventions

- **Zero third-party dependencies.** Standard library only. Keep it that way
  unless there's a strong reason; `go.mod` has no `require` block.
- Stop sequence `Observation:` hands control back to the harness before the
  model writes an observation itself.
- Tool errors are returned to the model as `Observation: Error: ...` so it can
  self-correct; the run does not abort on a tool error.
- Final answer takes precedence over an action in the same turn.
- **Final-answer detection is lenient, and a no-action turn is an implicit final
  answer.** Small local models (e.g. Gemma) render the label inconsistently or
  omit it entirely. So `finalRe` is case-insensitive and tolerates markdown
  emphasis / extra spacing (`**Final Answer:**`, `final answer :`), anchored with
  multiline `^` so a mid-sentence mention in a Thought is not a false match. And
  in the loop (agent.go), a turn with neither an Action nor a Final Answer nudges
  the model **once** (preserving malformed-action self-correction). The nudge is
  format-specific: a turn that carries an `Action:` token the parser couldn't honor
  gets `actionFormatNudge` (teaches the single-Action-line + JSON-arg shape)
  instead of the generic `nudgeMessage`. A *second*
  consecutive miss returns that prose as the answer via `stripThoughtPrefix`,
  because in a ReAct loop a turn with no tool call is by definition the final
  response. This guarantees termination instead of spinning to the step limit.
  The loop remembers the most recent *substantive* such turn in `candidateFinal`
  and never treats an **empty** turn as the answer: once a model has produced a
  complete prose reply, the empty turn it often emits in response to the nudge
  must not overwrite it (the symptom was an empty `--- final answer ---`).
- **An Action is honored only as the model's final, complete tool call.**
  `ParseStep` tries `findActionBlock` first: it finds the **last** `Action: name(`
  opener (multiline `^`), then scans forward **quote-aware** for the matching `)` —
  depth tracks `(`/`)` but characters inside a double-quoted string (with `\"`
  escapes) are skipped, so a multi-line / pretty-printed / fenced JSON arg, or
  parens inside a value, parse cleanly. The **"action is final" guard** is kept:
  only whitespace and an optional closing ```` ``` ```` fence may follow the `)`;
  substantive prose after it means the call is a documentation example, so
  `findActionBlock` declines. When it declines (e.g. an unterminated quote),
  `ParseStep` falls back to the original anchored `actionRe` against
  `lastNonEmptyLine(output)` — so no previously-passing case regresses. This dual
  path stops the harness from executing an `Action: …` that appears inside prose
  (the `bash(ls -R)` → `sh: -c: syntax error near unexpected token ')'` bug) while
  newly tolerating how models actually format calls. When neither a final answer
  nor an action is found, the loop nudges (agent.go) rather than acting.
- **Tool argument styles.** Single-arg tools take the raw string inside the
  parentheses (like `calculate`). Multi-arg tools (`write_file`, `edit_file`) take
  a **JSON object** that the quote-aware parser accepts on one line **or across
  several** and **optionally fenced**; the tools decode it via
  `textutil.DecodeJSONArgs` (strips a surrounding code fence via
  `textutil.StripCodeFence`, then a tolerant `json.Decoder` that ignores trailing
  bytes). Newlines inside string values must still be escaped as `\n` (JSON forbids
  raw newlines in strings). On a decode failure the tools return a **self-correcting**
  error naming the exact expected object (e.g. `{"path": str, "content": str}`).
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
