# Controller & Frontends

The `control.Controller` is the transport-agnostic session driver that sits between every frontend and the agent core. It owns the session lifecycle, handles commands (Send/Cancel/Approve/Compact/…), and emits everything that happens as a typed event stream. The point is one orchestration layer behind every frontend — a terminal TUI, a desktop webview, or an HTTP/SSE server each drive the Controller identically and none re-implement turn lifecycle, cancellation, or approval.

## The Controller

```go
type Controller struct {
    runner       agent.Runner           // Agent or Coordinator
    executor     *agent.Agent           // direct access to the executor
    sink         event.Sink             // event output
    policy       permission.Policy      // permission rules
    host         *plugin.Host           // MCP plugin host
    commands     []command.Command      // custom slash commands
    skills       []skill.Skill          // active skills
    hooks        *hook.Runner           // shell hooks
    mem          *memory.Set            // memory files + store
    cp           *checkpoint.Store      // checkpoint store for rewind
    // ... approval, plan mode, bypass, background jobs, etc.
}
```

### Commands (Frontend → Controller)

| Command | Description |
|---------|-------------|
| `Send(input)` | Start a turn with auto-plan and composition |
| `Submit(input)` | One-call entry: slash dispatch, @-refs, plan mode |
| `Cancel()` | Abort the in-flight turn |
| `Approve(id, allow, session, persist)` | Answer a pending approval request |
| `AnswerQuestion(id, answers)` | Answer a pending ask request |
| `SetPlanMode(on)` | Toggle plan mode (cache-friendly) |
| `SetAutoPlan(mode)` | Change auto-plan setting |
| `Compact(ctx, instructions)` | Run one compaction pass |
| `NewSession()` | Rotate to a fresh session |
| `Rewind(turn, scope)` | Restore code/conversation/both to a checkpoint |
| `Fork(turn)` / `ForkNamed(turn, name)` | Branch the conversation |
| `SwitchBranch(ref)` | Load another branch |
| `RunShell(command)` | Execute a shell command directly (bypass model) |
| `SetBypass(on)` | Toggle YOLO mode |
| `AddMCPServer(spec)` / `RemoveMCPServer(name)` | Hot-add/remove MCP servers |

### Run Guarded

Every turn runs inside `runGuarded()` — a background goroutine under a fresh cancellable context. It guards against concurrent turns and emits `TurnDone` when finished. A no-op if a turn is already in flight.

### Compose

`Compose(input)` folds all session-level context onto the user's message before sending it to the agent:
- Pending memory notes (added mid-session but not yet in the prefix)
- Plan-mode framing (when plan mode is active)
- Background job completion notes

Composing at the turn tail — never into the cache-stable system prefix — is how new memory takes effect this session without busting the prompt cache.

### Submit Dispatch

`Submit(input)` is the one-call entry for simple frontends (HTTP/SSE, desktop):

1. **Memory quick-add** — `#<note>` or `/remember <text>` adds a note.
2. **Shell command** — `!<command>` runs a shell command directly.
3. **Built-in slash commands** — `/compact`, `/new`, `/tree`, `/branch`, `/switch`, `/rewind` are handled locally.
4. **MCP prompts** — `/mcp__<server>__<prompt>` resolves to a turn.
5. **Custom commands** — `/<name>` resolves from `.reasonix/commands/`.
6. **Skills** — `/<name>` resolves from the skill store.
7. **Normal turn** — resolve `@`-references and start a turn.

### Plan Approval Flow

1. User sends input while plan mode is on.
2. Agent runs in read-only mode (writers blocked).
3. Agent produces a plan as its text answer.
4. Controller emits an `ApprovalRequest` with `planApprovalTool`.
5. Frontend renders the approval UI.
6. On approval → plan mode off, auto-approve writers, seed todos from plan, execute.
7. On rejection → stay in plan mode for revision.

### Interactive Approval

`EnableInteractiveApproval()` swaps the executor's gate for one that routes "ask" decisions to the frontend via `ApprovalRequest` events. It also wires the controller as the executor's `Asker` so the `ask` tool can question the user.

Approval requests are serialized by `promptMu` — at most one is outstanding at a time.

### Auto-Plan Classification

When `auto_plan = "on"` is configured, the controller may automatically enter plan mode for complex-looking tasks:
- `maybeAutoPlan()` checks if the raw input (excluding @-reference payloads) suggests complexity.
- An optional `auto_plan_classifier` provider (e.g. deepseek-flash) is called for borderline cases.
- Simple inputs bypass plan mode; complex ones enter it automatically.

### Session Persistence

- Sessions are saved as JSONL files (one message per line) under the configured session directory.
- `Snapshot()` persists the current session to disk.
- `Resume()` loads a saved session and rebinds checkpoints.
- The controller auto-saves on turn completion and after significant events.

### Memory Integration

The controller implements `memory.Queue` so the `remember`/`forget` tools can fold a turn-tail note about a just-made memory change into the next turn. This applies the memory change in the current session without touching the cache-stable prefix.

## Frontends

### Bubble Tea TUI (`internal/cli/chat_tui.go`)

The primary frontend — a rich terminal interface built with the Charm stack (bubbletea v2, lipgloss v2, bubbles v2):
- **Composer** — bottom input area with autocomplete for `/` (slash commands) and `@` (file/resource references).
- **Transcript** — scrollable message history with markdown rendering, tool cards, reasoning display.
- **Status line** — model name, context gauge, cache hit rate, cost.
- **Slash command overlays** — `/mcp`, `/resume`, `/rewind` open modal pickers.
- **Approval prompts** — inline approval cards for writer tools.
- **Ask cards** — structured multiple-choice questions.
- **Todo panel** — pinned task list showing plan progress.

### HTTP/SSE Server (`internal/serve/`)

A second frontend that exposes the controller over HTTP:
- **GET /events** — SSE stream of all events.
- **POST /submit** — send user input.
- **POST /cancel**, **POST /approve**, **POST /plan**, **POST /compact**, etc.
- **GET /history** — message log with ETag caching.
- **GET /context** — prompt-vs-window gauge.
- **GET /checkpoints**, **GET /branches**, **GET /status**, **GET /sessions**.
- **POST /model** — runtime model switching.
- **POST /effort** — reasoning effort switching.
- CSRF protection via Content-Type enforcement.

### Desktop App (`desktop/`)

A Wails-based desktop application with a React/TypeScript frontend:
- Drives the same controller over Wails bindings.
- React components for chat, settings, workspace panel, memory panel, skill panel.
- Tab-based multi-session support.
- System tray integration.
- Auto-updater with signed manifest verification.

## Boot Sequence

The `internal/boot/` package wires everything together:

1. Load config (TOML resolution hierarchy).
2. Resolve the model provider.
3. Discover memory files.
4. Discover skills.
5. Start MCP plugins (Phase A for tools, Phase B async for prompts/resources).
6. Build the tool registry (built-ins + plugin tools).
7. Build the agent (or coordinator if planner_model is set).
8. Build the controller with all assembled pieces.
9. Enable interactive approval (for chat/desktop).

## See Also

- [Architecture Overview](architecture-overview.md)
- [Agent Loop & Coordinator](agent-loop-coordinator.md)
- [Event System](event-system.md)
- [HTTP/SSE Server & ACP](http-server-acp.md)
- [Desktop App](desktop-app.md)
