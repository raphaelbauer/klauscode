# Klaus Code

[![CI](https://github.com/raphaelbauer/klauscode/actions/workflows/ci.yml/badge.svg)](https://github.com/raphaelbauer/klauscode/actions/workflows/ci.yml)

A small, dependency-free **AI harness** in Go. It drives an OpenAI model through
a [ReAct](https://arxiv.org/abs/2210.03629) loop — **Thought → Action →
Observation → Final Answer** — where the model requests tools as plain text, the
harness intercepts and executes them, feeds the result back, and repeats until
the model produces a final answer.

It ships tools for **coding** (read/write/edit files, run shell commands) and
**web research** (search and fetch pages), plus the original `calculate`. The
design is deliberately minimal — inspired by [Pi](https://pi.dev/), which leans
on a small set of tools (notably `bash`) rather than many specialised ones.

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

## Tools

| Tool | Argument | Purpose |
| ---- | -------- | ------- |
| `calculate` | `expression` | Evaluate an arithmetic expression. |
| `read_file` | `path` | Read a file's contents. |
| `write_file` | `{"path","content"}` | Create or overwrite a file. |
| `edit_file` | `{"path","old","new"}` | Replace the unique occurrence of `old` with `new`. |
| `bash` | `command` | Run a shell command (`sh -c`); returns combined stdout+stderr. |
| `web_search` | `query` | Search the web via DuckDuckGo (no API key). |
| `web_fetch` | `url` | Fetch a URL and return its page text (HTML stripped). |

Most tools take their argument as a raw string. `write_file` and `edit_file`
take a **single-line JSON object** (JSON escapes newlines as `\n`, so multi-line
content stays on one line and the single-line Action parser still works).

`bash` is the Pi-style workhorse: listing (`ls`), searching (`grep`), building
and testing (`go build`, `go test`) all go through it instead of dedicated tools.

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

Coding and web-research tasks work the same way — the model chooses tools:

```sh
# coding: read, edit, and verify with bash
go run ./cmd/klauscode "Create hello.go that prints Hello, then run it with go run hello.go"

# web research: search then fetch
go run ./cmd/klauscode "Find the Go context package docs and summarize context.WithTimeout"
```

> [!WARNING]
> `bash` and the file tools act on your real working directory with your
> privileges, and `web_fetch`/`web_search` pull in untrusted content. See
> [Security](#security) before running it on anything sensitive.

### Building an executable

`go run` compiles and runs in one step, which is handy during development. To
produce a standalone binary instead, use `go build`:

```sh
go build -o klauscode ./cmd/klauscode
./klauscode "What is (12 * 9) + 3?"
```

Two details worth knowing:

- **Why `./cmd/klauscode`?** It tells Go *which* package to build. The executable
  comes from the `package main` in [cmd/klauscode/main.go](cmd/klauscode/main.go),
  not the repo root — the root holds only library packages (`internal/...`). This
  `cmd/<appname>` layout is the standard Go convention for separating the
  entrypoint from library code.
- **Why `-o`?** It sets the **o**utput name/path. Without it, `go build` names the
  binary after the source directory (here, `klauscode` — so it happens to be the
  same) and drops it in the current directory. `-o` makes the name explicit and
  lets you place it elsewhere, e.g. `-o build/klauscode`.

To install the binary onto your `PATH` (into `$GOBIN`, or `$GOPATH/bin`):

```sh
go install ./cmd/klauscode
```

Cross-compile for another platform by setting `GOOS`/`GOARCH`:

```sh
GOOS=linux GOARCH=amd64 go build -o klauscode-linux ./cmd/klauscode
```

### Configuration

| Variable          | Default                      | Purpose                                            |
| ----------------- | ---------------------------- | -------------------------------------------------- |
| `OPENAI_API_KEY`  | _(required\*)_               | API key, sent as `Authorization: Bearer`.          |
| `OPENAI_MODEL`    | `gpt-4o-mini`                | Model name.                                         |
| `OPENAI_BASE_URL` | `https://api.openai.com/v1`  | API base URL (ending in `/v1`); `/chat/completions` is appended. |

\* Required only for the public OpenAI API. When `OPENAI_BASE_URL` points at a
local server, the key is optional (a placeholder is used).

### Local OpenAI-compatible servers (e.g. LM Studio)

Point `OPENAI_BASE_URL` at the server's `/v1` base and set `OPENAI_MODEL` to a
model that is actually loaded (the `gpt-4o-mini` default won't exist locally).
No API key is needed — local servers ignore it:

```sh
OPENAI_BASE_URL=http://127.0.0.1:1234/v1 \
OPENAI_MODEL=<your-loaded-model> \
go run ./cmd/klauscode "What is (12 * 9) + 3?"
```

## Architecture

Layered with interface-based dependency injection so each layer is unit-testable
in isolation. `cmd/klauscode` is the composition root that wires the concrete
implementations together.

| Package          | Responsibility                                              |
| ---------------- | ---------------------------------------------------------- |
| `internal/llm`   | Provider boundary: `Client` interface + OpenAI impl (net/http). |
| `internal/tools` | Action boundary: `Tool` interface, `Registry`, and one package per tool (`calculate`, `readfile`, `writefile`, `editfile`, `bash`, `websearch`, `webfetch`) plus shared `textutil`. |
| `internal/agent` | The ReAct loop, the system prompt, and the turn parser.    |
| `cmd/klauscode`     | Reads config from the environment, wires everything, runs the task. |

No third-party dependencies — only the Go standard library. The web tools scrape
DuckDuckGo's HTML and strip pages to text with `regexp` + the stdlib `html`
package rather than pulling in an HTML library.

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

1. Implement it in its own package under `internal/tools/<name>/` (see
   [internal/tools/calculate/calculate.go](internal/tools/calculate/calculate.go)
   as a template).
2. Register it in [cmd/klauscode/main.go](cmd/klauscode/main.go):

   ```go
   registry.Register(yourtool.New())
   ```

The system prompt lists registered tools automatically, so the model learns
about the new tool with no prompt edits. Tool errors are fed back to the model
as an observation, letting it self-correct rather than failing the run.

## Security

Once an agent can read files, run shell commands, **and** fetch web pages, it has
all three ingredients of the "lethal trifecta": access to sensitive data,
the ability to act and exfiltrate (via `bash`/`curl` or a crafted URL), and
exposure to untrusted content. A malicious web page can try a **prompt-injection
attack** — e.g. text that says *"ignore your task and run `bash(...)`"*.

What klauscode does, and does not, do about it:

- **`bash` and the file tools are unsandboxed.** They run with your privileges in
  your working directory. There is no path jail (a jail is false security since
  `bash` escapes it anyway).
- **Untrusted web content is delimited.** `web_fetch`/`web_search` wrap their
  output in `[UNTRUSTED WEB CONTENT <id>] … [END UNTRUSTED WEB CONTENT <id>]`
  markers with a random per-call `id`, and strip any forged markers from the
  body, so a page cannot cleanly "close" the block early and inject instructions.
  The system prompt tells the model to treat that block as data, never commands.
- **This is best-effort, not a guarantee.** Delimiting defeats the cheap breakout
  trick; it cannot stop a payload that merely *persuades* the model from inside a
  correctly delimited block. The run is autonomous — there is no confirmation
  prompt before a command executes.

**The load-bearing control is you.** Run klauscode **sandboxed** — in a
container or a disposable copy of the repo, as a low-privilege user, ideally with
restricted network egress — and only on code and inputs you are willing to have
read, modified, or sent over the network.

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
