# Plan: Integrate klauscode into VS Code as a Chat Experience

## Current State Analysis

klauscode is a **single-shot CLI agent**: one invocation, one task, one answer, exit.
The `Agent.Run()` method creates a fresh `[]llm.Message` slice per call with no
conversation persistence. There is no concept of sessions, multi-turn chat, or
interactive clarification.

Current flow:
  user -> klauscode "task" -> [ReAct loop] -> Final Answer -> exit

What we need:
  user -> klauscode "hello" -> agent asks clarifying question -> user replies
  -> agent continues with context -> Final Answer -> (session stays alive)
  -> user -> "follow-up" -> agent remembers context -> ...

---

## Architecture: Three Layers of Change

### Layer 1: Session-Aware Agent (core change, no VS Code dependency)

The biggest architectural change: the agent must support **persistent conversation
history** across multiple Run calls within a session.

**New concept: Session**

A Session struct holds the conversation history ([]llm.Message) for a single chat
session. It accumulates across turns.

**New method: Agent.RunSession(ctx, session, userMessage)**

This is the multi-turn variant of Run. It:
1. Takes the session existing history + the new user message
2. Runs the ReAct loop (same as today)
3. On Final Answer: appends the assistant final turn to the session history
4. Returns the answer (session stays alive for the next call)

The existing Run(ctx, task) becomes a thin wrapper that creates a new Session,
appends the task, and calls RunSession. This is **backward compatible** -- the
CLI's single-shot mode still works.

**Why sessions, not just keep the agent alive?**
- The agent's system prompt is baked in at construction time. A session carries
  only the conversation history, so you can create multiple sessions from one agent
  (e.g., parallel chats in VS Code).
- Sessions are serializable (just []llm.Message), enabling persistence.
- The agent itself remains stateless and testable.

### Layer 2: Clarifying Questions (model asks the user)

The model needs a way to **pause the ReAct loop and ask the user a question**
before continuing.

**Approach A: ask tool (recommended, simplest)**

Add a new tool: ask(question) that the model calls when it needs clarification.

The ask tool has an injected callback func(string) (string, error). In CLI chat
mode, the callback prints the question and reads stdin. In VS Code mode, the
callback sends the question to the chat panel and waits for the user's reply.

The system prompt gets an extra section explaining the ask tool.

**Approach B: Detect questions in prose (no new tool)**

If the model's turn has no Action and no Final Answer, but contains a question
mark, treat it as a clarifying question. This is fragile.

**Recommendation: Approach A (ask tool).** It is explicit, testable, and the
model learns the pattern cleanly. It also fits the existing tool-calling paradigm.

### Layer 3: VS Code Integration

Three options, from simplest to most complete:

**Option A: --chat CLI mode + VS Code task**

Add a --chat flag to the CLI that runs an interactive loop:
- klauscode --chat starts a persistent session
- Reads user input from stdin line by line
- For each input, calls RunSession with the session
- Prints the Final Answer to stdout
- The ask tool prints questions to stdout and reads replies from stdin
- User types quit or Ctrl-C to exit

VS Code integration: a simple .vscode/tasks.json that runs klauscode --chat
in an integrated terminal. The user interacts via the terminal.

Pros: zero dependencies, no VS Code API knowledge needed, works today
Cons: terminal-based UI, not a native chat panel

**Option B: VS Code Language Server Protocol (LSP) extension**

Build a minimal VS Code extension that communicates with klauscode via stdio
using a JSON-RPC protocol. Each chat message is a JSON object on stdin/stdout.

Pros: native VS Code chat panel integration possible
Cons: requires TypeScript/VS Code API knowledge, more complex

**Option C: HTTP server mode**

Add a --serve flag that starts klauscode as an HTTP server with a simple REST
API. A VS Code extension calls the API endpoints.

Pros: clean separation, language-agnostic
Cons: most complex, requires session management on the server

---

## Recommended Path: Option A (--chat mode) with ask tool

This is the fastest path to a working chat experience with minimal code changes.

### Implementation Steps

**Step 1: Add Session type** (internal/agent/session.go)
- New file with Session struct and methods
- Tests for basic append/retrieve

**Step 2: Add RunSession method** (internal/agent/agent.go)
- Modify Agent.Run to delegate to RunSession
- RunSession takes a Session and a user message string
- The ReAct loop uses session.Messages() instead of creating a new slice
- On Final Answer, append the assistant message to the session
- Tests: multi-turn conversation, context preservation

**Step 3: Add ask tool** (internal/tools/ask/ask.go)
- Implements Tool interface
- Takes a prompt callback func(string) (string, error) as constructor param
- Call(args) invokes the callback with the question, returns the reply
- Tests: with a mock callback

**Step 4: Add --chat flag** (cmd/klauscode/main.go)
- When --chat is set, create one Session and loop:
  - Print prompt (>)
  - Read line from stdin
  - If empty or quit, break
  - Call RunSession with the session
  - Print the answer
- The ask tool's callback prints the question and reads a line from stdin
- Register the ask tool (only in chat mode, or always -- it is harmless)

**Step 5: Update system prompt** (internal/agent/prompt.go)
- Add ask tool to the tool list in the prompt
- Add a note about multi-turn conversation context

**Step 6: VS Code task** (.vscode/tasks.json)
- Define a task that runs klauscode --chat in an integrated terminal
- User can start it with Cmd+Shift+P -> Run Task -> klauscode chat

---

## Do You Need Sessions?

**Yes, absolutely.** Without sessions, every message is independent. The model
has no memory of previous exchanges. For a chat experience where you ask follow-up
questions or the model asks clarifying questions, you need the conversation history
to persist across turns.

The Session abstraction is the minimal change that enables this. It is also the
foundation for any future VS Code extension (Options B or C) -- they would all
use the same Session type.

---

## Other Simple Ideas

1. **Context from the current file**: In VS Code, pass the active file content
   as part of the user message. The --chat mode could accept a --file flag that
   reads a file and prepends it to the conversation.

2. **Project context auto-injection**: The existing AGENTS.md/CLAUDE.md loading
   already provides project context. In chat mode, this is loaded once at startup
   and stays in the system prompt for the entire session.

3. **Streaming output**: The current implementation waits for the full model
   response. For a better chat UX, add streaming support (SSE) so the user sees
   the answer as it is generated. This requires changes to the LLM client to
   support streaming, and the agent loop to handle partial responses.

4. **Session persistence**: Save sessions to disk (JSON) so they survive a
   restart. Load them back on --chat resume.

5. **Multiple sessions**: VS Code could have multiple chat sessions (one per
   terminal tab). The Session abstraction already supports this naturally.

---

## File Changes Summary

| File | Change |
|------|--------|
| internal/agent/session.go | NEW: Session type |
| internal/agent/agent.go | Add RunSession, refactor Run |
| internal/agent/agent_test.go | Tests for RunSession, multi-turn |
| internal/agent/prompt.go | Add ask tool description |
| internal/tools/ask/ask.go | NEW: ask tool |
| internal/tools/ask/ask_test.go | NEW: tests |
| cmd/klauscode/main.go | Add --chat flag, chat loop |
| .vscode/tasks.json | NEW: VS Code task definition |
| README.md | Document --chat mode |
