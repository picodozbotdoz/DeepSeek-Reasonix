# Agent Loop & Coordinator

The agent loop is the heart of Reasonix — the engine that drives a single coding task to completion. It wires a `Provider` (model backend), a `Tool.Registry` (available tools), and a `Session` (conversation history) into a streaming harness loop. The `Coordinator` extends this to two-model collaboration while preserving cache stability.

## The Agent

The `Agent` struct (`internal/agent/agent.go`) is a self-contained unit that owns everything needed for one model-driven task:

```go
type Agent struct {
    prov        provider.Provider    // model backend
    tools       *tool.Registry       // available tools
    session     *Session             // conversation history
    maxSteps    int                  // optional step limit
    temperature float64              // sampling temperature
    sink        event.Sink           // event stream (never nil)
    gate        Gate                 // per-call permission gate (nil = no gating)
    hooks       ToolHooks            // PreToolUse / PostToolUse shell hooks
    asker       Asker                // interactive question interface for the ask tool
    evidence    *evidence.Ledger     // per-turn tool receipt ledger
    jobs        *jobs.Manager        // background job manager
    planMode    atomic.Bool          // read-only gate for plan mode
    // ... context management fields, storm detection, etc.
}
```

### Key Interfaces

The agent stays independent of concrete implementations through four interface seams:

- **`Gate`** — decides per tool call whether it may run. The agent consults it at execute time after the plan-mode gate. A nil gate means no gating (every call runs).
- **`Asker`** — puts structured multiple-choice questions to the user. The `ask` tool uses it. A nil asker means headless mode (no interactive user).
- **`ToolHooks`** — fires user-configured shell hooks around each tool call. PreToolUse may block, PostToolUse only surfaces output.
- **`Renderer`** — redraws the assistant's final-answer text as styled output after a turn's text stream completes.

### The Run Loop

`Agent.Run(ctx, input)` is the main entry point. It appends the user input and drives the tool loop:

1. **Reset per-turn state** — clear the evidence ledger, repeat-success counters.
2. **Emit TurnStarted** — signal the frontend that a new turn has begun.
3. **Append user message** to the session.
4. **Enter the loop** (bounded by `maxSteps`, or unbounded if 0):
   - Capture the current prefix shape (for cache diagnostics).
   - Call `stream()` — one streaming completion from the provider.
   - Emit usage events with cache diagnostics.
   - Append the assistant message (text + reasoning + tool calls) to the session.
   - **If no tool calls** → run final-answer readiness checks → return.
   - **If tool calls** → execute them via `executeBatch()`.
   - After the batch, run `maybeCompact()` if context is getting full.
5. **Return** on final answer, context cancellation, provider error, or maxSteps exhaustion.

### Streaming: `stream()`

The `stream()` method runs one completion against the provider and processes the response chunks:

- **ChunkReasoning** — thinking-mode chain-of-thought. If a `PostLLMCall` hook is wired up, reasoning is buffered and transformed after the stream (to support e.g. translation hooks); otherwise, it streams live chunk-by-chunk.
- **ChunkText** — visible answer text, emitted as `event.Text` for live rendering.
- **ChunkToolCallStart** — emitted immediately so the frontend shows a tool card before its (possibly large) arguments finish streaming.
- **ChunkToolCall** — a complete tool call is accumulated.
- **ChunkUsage** — token accounting, cached for the status line and emitted as an event.
- **ChunkError** — terminates the stream with an error.

Anthropic extended thinking produces a `Signature` that must be round-tripped verbatim on subsequent tool-call turns; the agent stores the signed block but never re-uploads transformed reasoning.

### Tool Execution: `executeBatch()`

One model turn may produce multiple tool calls. `executeBatch()` dispatches them with a critical optimization: **contiguous read-only calls fan out across goroutines**, while unknown and writer calls run serially to preserve write/read ordering.

```
partitionToolCalls → [batch{parallel: true, read-only tools}] → runParallel
                   → [batch{parallel: false, writer tools}]   → run serial
                   → [batch{parallel: true, read-only tools}] → runParallel
```

`complete_step` and `todo_write` are never parallelized even though they're read-only — they read the turn's evidence ledger, so every prior call's receipt must be recorded first.

### Storm Detection & Loop Guards

The agent includes two death-spiral detectors:

1. **Storm breaker** (`applyStormBreaker`) — tracks a per-turn fixation signature keyed on each call's `(tool, error)`, not args. A stuck model reworks arguments cosmetically while failing identically, so keying on args would miss the loop. After 3 consecutive identical failures, the agent rewrites the result to nudge the model to change approach.

2. **Repeat-success guard** (`repeatedSuccessBlock`) — catches the complementary loop: a model keeps doing the same successful write (no error for the storm breaker to see). After 2 identical write-like successes, the next repeat is blocked.

### Final-Answer Readiness

Before accepting a final answer (no tool calls), the agent runs readiness checks via the evidence ledger:

- If there are incomplete todos from the latest `todo_write`, the model must address them.
- If a writer tool succeeded, the model must run project-specified verification commands afterward.
- If a `todo_write` succeeded, the model must call `complete_step` afterward.
- Up to 3 readiness retries are allowed before the agent errors out.

### Plan Mode

Plan mode is a read-only gate: when `planMode` is true, `executeOne()` refuses any non-`ReadOnly()` tool and returns a "blocked" result. The system prompt and tool list never change with the toggle, so the prompt-cache prefix stays valid. The gating happens at execute time, and the model sees a "blocked" result it can adapt to.

## The Session

`Session` (`internal/agent/session.go`) holds the conversation history as `[]provider.Message`:

```go
type Session struct {
    mu             sync.RWMutex
    Messages       []provider.Message
    rewriteVersion int  // bumped each time the log is rewritten (compact/fold)
}
```

- **Add** — appends a message (used by the run loop, one turn at a time).
- **Replace** — swaps the whole message log (used by compaction).
- **Snapshot** — returns a copy for frontends reading from another goroutine.
- **RewriteVersion** — tracks how many times the log was restructured; used by cache-shape diagnostics.

The session is the fundamental data structure shared between the agent, the controller, and the frontends. It is serialized as JSONL for persistence and loaded on resume.

## The Coordinator

The `Coordinator` (`internal/agent/coordinator.go`) enables two-model collaboration while keeping each model's prompt prefix cache-stable:

```go
type Coordinator struct {
    planner        provider.Provider
    plannerSess    *Session
    executor       *Agent
    shouldPlan     func(string) bool  // nil = plan every turn
    // ...
}
```

Both `Agent` and `Coordinator` satisfy the `Runner` interface:

```go
type Runner interface {
    Run(ctx context.Context, input string) error
}
```

The CLI stays agnostic to which is in use — it just calls `runner.Run(ctx, input)`.

### Two-Model Flow

1. **Should we plan?** If `shouldPlan` is set and returns false for simple inputs (greetings, questions), skip straight to the executor.
2. **Plan phase** — the planner (no tools) produces a concise plan in its own session. The session grows prepend-only and stays cache-friendly.
3. **Handoff** — the plan is formatted as structured text and handed to the executor as a new input.
4. **Execute phase** — the executor (a full tool-using `Agent`) carries out the plan in its own separate session.

The two sessions **never mix** — neither model's prefix is disturbed by the other's turns. This is the key architectural insight: switching models inside one shared conversation would break the prefix and tank cache hits, so instead we use separate, cache-stable sessions.

### Default Planner Prompt

```
You are the planner in a two-model coding agent.
Given a task, produce a concise, ordered plan for the executor model to carry out.
Do not write full implementations or call tools — outline the steps, which files
to touch, and the key decisions. Keep it short and actionable.
```

### The `shouldPlan` Gate

A `nil` `shouldPlan` means every turn goes through the planner. When wired (the controller does this when `auto_plan_classifier` is configured), borderline tasks are classified — trivial ones bypass the planner and go straight to the executor, saving a planner round-trip.

## Subagents: The `task` Tool

The `task` tool spawns a child agent loop (a new `Agent` with its own session and tool registry). It can run in the foreground (blocking, returning the final answer) or in the background (managed by `jobs.Manager`).

The parent agent stamps each tool call's context with `callContext` (parent ID + event sink + asker), which the `task` tool reads to nest sub-agent events under the parent call. This ensures the frontend renders subagent activity as a collapsible subtree under the parent tool card.

## Context Management: Compaction

Long tasks eventually fill the model's context window. The agent manages this with low-frequency compaction that respects the cache-first design:

- **Soft ratio** (`softCompactRatio`, default 0.5) — emits a one-shot notice.
- **Compact ratio** (`compactRatio`, default 0.8) — tries compacting.
- **Force ratio** (`compactForceRatio`, default 0.9) — forces compaction.
- Compaction summarizes the older middle of the session into a single briefing (using the executor's own provider, no tools) and replaces it in place.
- The dropped originals are archived under `~/.config/reasonix/archive/`.
- This is the **only** point where the prompt prefix changes — a deliberate, rare cache-reset point.

## Cache Shape Diagnostics

The agent tracks the prefix shape across turns (`PrefixShape` / `CaptureShape`) and compares it with usage data to diagnose cache churn. When the prefix shape changes between turns (e.g. tool schemas were reordered), the cache miss is explained to the user via a diagnostic notice.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Provider System](provider-system.md)
- [Tool System](tool-system.md)
- [Context Management](context-management.md)
- [Permission & Sandbox](permission-sandbox.md)
