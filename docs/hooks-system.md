# Hooks System

Hooks are user-configured shell commands that fire at specific points in the agent loop. They provide a lightweight, scriptable extension mechanism for guardrails, formatting, notifications, and reasoning post-processing — without modifying Reasonix's code.

## Hook Events

| Event | Timing | Can Block? | Description |
|-------|--------|-----------|-------------|
| `PreToolUse` | Before a tool call | Yes | Gate tool execution; exit 2 blocks |
| `PostToolUse` | After a tool call | No | React to results; e.g. auto-format |
| `UserPromptSubmit` | Before a turn starts | Yes | Gate user input; exit 2 blocks |
| `Stop` | After a turn ends | No | Notifications, cleanup |
| `PostLLMCall` | After model streaming finishes | No | Transform reasoning text |
| `SessionStart` | When a session becomes active | No | Setup, notifications |
| `SessionEnd` | When a session is closed | No | Teardown |
| `SubagentStop` | When a task sub-agent finishes | No | Post-processing |
| `Notification` | Agent needs user attention | No | Push notifications |
| `PreCompact` | Just before compaction | No | Contribute summary guidance |

## Hook Configuration

Hooks are declared in `settings.json` files, **not** in `reasonix.toml`:

- **Global** — `~/.reasonix/settings.json` (always active)
- **Project** — `<project>/.reasonix/settings.json` (only after `/hooks trust`)

```json
{
  "hooks": {
    "PreToolUse": [
      { "match": "bash", "command": "my-guard.sh" }
    ],
    "PostToolUse": [
      { "match": ".*file", "command": "gofmt -w ." }
    ],
    "Stop": [
      { "command": "notify-send 'turn done'" }
    ],
    "PostLLMCall": [
      { "command": "translate-reasoning.sh" }
    ]
  }
}
```

### Hook Config Fields

| Field | Description |
|-------|-------------|
| `match` | Anchored regex selecting tools (Pre/PostToolUse only); `""` or `"*"` = every tool |
| `command` | Shell command to run (spawned through the platform shell) |
| `description` | Optional human label surfaced in `/hooks` |
| `timeout` | Override per-event timeout in milliseconds |
| `cwd` | Override working directory (defaults to payload's cwd) |

### Match Semantics

The `match` field is compiled as an anchored regex: `"file"` won't match `"read_file"` — use `".*file"`. A malformed regex never fires (safer than firing on everything).

### Trust Model

Project hooks are **not trusted by default** — they could be committed by anyone. The user must explicitly trust a project's hooks via `/hooks trust`. Global hooks are always active.

`ProjectDefinesHooks()` reports whether a project's settings.json declares at least one hook, so frontends can prompt the user to trust it.

## Hook Execution

```go
func Run(ctx context.Context, payload Payload, hooks []ResolvedHook, spawner Spawner) Report
```

### Payload

Each hook receives a JSON payload on stdin:

```go
type Payload struct {
    Event         Event           `json:"event"`
    Cwd           string          `json:"cwd"`
    ToolName      string          `json:"toolName,omitempty"`
    ToolArgs      json.RawMessage `json:"toolArgs,omitempty"`
    ToolResult    string          `json:"toolResult,omitempty"`
    Prompt        string          `json:"prompt,omitempty"`
    LastAssistant string          `json:"lastAssistantText,omitempty"`
    Turn          int             `json:"turn,omitempty"`
    Reasoning     string          `json:"reasoning,omitempty"`
    Trigger       string          `json:"trigger,omitempty"`
    Message       string          `json:"message,omitempty"`
}
```

### Exit Codes

| Exit Code | Meaning |
|-----------|---------|
| 0 | Pass (allow) |
| 2 | Block (only on `PreToolUse` / `UserPromptSubmit`) |
| Other | Warn |

### Report & Outcome

```go
type Outcome struct {
    Hook      ResolvedHook
    Decision  Decision  // pass / block / warn / error
    ExitCode  int
    Stdout    string
    Stderr    string
    TimedOut  bool
    Duration  time.Duration
}

type Report struct {
    Event    Event
    Outcomes []Outcome
    Blocked  bool
}
```

Execution stops at the first block on gating events so a blocking hook prevents later hooks from running against a phantom success.

## PostLLMCall: Reasoning Transformation

The `PostLLMCall` event is special — it can **transform the reasoning text** that the user sees. When a hook is configured:
- The agent buffers reasoning silently (no live streaming).
- After the stream completes, the hook receives the full reasoning text on stdin.
- If the hook exits 0 with non-empty stdout, that output replaces the displayed reasoning.
- This enables use cases like translation, summarization, or redaction of chain-of-thought.

## PreCompact: Summary Guidance

`PreCompact` hooks contribute extra guidance to the compaction summary prompt. The hook's stdout is injected as additional instructions, steering what the summarizer keeps.

## Timeouts

| Event | Default Timeout |
|-------|----------------|
| PreToolUse, UserPromptSubmit | 5 seconds |
| All others | 30 seconds |

Hooks are spawned through the platform shell (`sh -c` on Unix, `cmd /c` on Windows) with the payload on stdin. Output is capped at 256KB per stream to prevent runaway children from exhausting memory.

## The Runner

`hook.Runner` (`internal/hook/runner.go`) wraps the low-level `Run()` function with a session-scoped set of resolved hooks and convenience methods:

- `PreToolUse()` / `PostToolUse()` — fire around each tool call.
- `PromptSubmit()` — fire before a turn.
- `Stop()` — fire after a turn.
- `PostLLMCall()` — transform reasoning.
- `SessionStart()` / `SessionEnd()` — lifecycle events.
- `PreCompact()` — contribute compaction guidance.

The runner is nil-safe — constructing one with no hooks produces a no-op runner.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Agent Loop & Coordinator](agent-loop-coordinator.md)
- [Context Management](context-management.md)
