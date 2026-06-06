# Sub-Agent & Task Tool

The `task` tool is Reasonix's delegation mechanism. It spawns a sub-agent in its own session with a filtered tool set, runs it to completion, and returns only the final answer to the parent model. This keeps noisy exploration sequences out of the parent's context budget and enables focused, self-contained work delegation. Background task execution extends this to asynchronous sub-tasks that survive across turns.

## Architecture

### The Task Tool

```go
type TaskTool struct {
    prov              provider.Provider
    pricing           *provider.Pricing
    parentReg         *tool.Registry
    maxSteps          int
    contextWindow     int
    softCompactRatio  float64
    compactRatio      float64
    compactForceRatio float64
    temperature       float64
    archiveDir        string
    sysPrompt         string
    gate              Gate
}
```

The `TaskTool` carries everything needed to build a fresh sub-agent: the provider, the parent's tool registry (for filtering), compaction settings, and a permission gate. It is classified as a **writer tool** (`ReadOnly() = false`) because a sub-agent can invoke any whitelisted tool, including writers. This conservative classification also prevents the parallel-dispatch path from running two sub-agents at once and letting their writes race.

### Sub-Agent Lifecycle

1. **Construction**: A new `Session` and `Agent` are created with the sub-agent's system prompt and filtered tool registry
2. **Execution**: The sub-agent runs the prompt to completion using `Agent.Run()` — the same harness loop as the parent
3. **Answer extraction**: The session is walked backwards for the last assistant message with non-empty content — that's the final answer
4. **Return**: Only the final answer text is returned to the parent model; intermediate tool calls and reasoning are not included

### System Prompt

```go
const DefaultTaskSystemPrompt = `You are a sub-agent invoked by a parent coding agent to carry out one focused task.
Use the provided tools to investigate or act. Return a single final answer that is concise
and self-contained — the parent will see only that answer, not your tool calls or reasoning.
If you need to ask for clarification, fail with a precise question instead of guessing.`
```

The system prompt steers the sub-agent toward focused, terse delivery. Since the sub-agent doesn't see the parent's conversation, it must self-contain all necessary context.

## Tool Filtering

### Meta-Tool Exclusion

Sub-agents are prevented from recursively spawning further sub-agents by excluding **meta-tools**:

```go
var subagentMetaTools = []string{
    "task",
    "run_skill",
    "install_skill",
    "explore",
    "research",
    "review",
    "security_review",
}
```

These tools can spawn or author more agent work, so excluding them preserves one layer of delegation without adding a spawn-count cap. A caller who deliberately wants deeper nesting can override this by providing an explicit tool whitelist that includes meta-tools.

### FilterRegistry

```go
func FilterRegistry(parent *tool.Registry, names []string, exclude ...string) *tool.Registry
```

`FilterRegistry` builds a sub-registry from the parent:

- If `names` is empty, every parent tool is included (minus exclusions)
- If `names` is specified, only those tools are included (minus exclusions)
- Excluded tools are always stripped, even if explicitly named

This is shared between the `task` tool and subagent skills — any code path that delegates to a sub-agent uses the same filtering logic.

### Step Budget

The sub-agent's step budget is derived from the parent:

- **Parent with finite `maxSteps`**: Sub-agent gets `maxSteps / 2` (minimum 5) — a delegated sub-task stays shorter than the whole turn
- **Parent with unbounded `maxSteps`**: Sub-agent is also unbounded — the same bounds (context cancellation, compaction) apply
- **Explicit `max_steps` override**: The caller can set any cap per invocation

## Event Nesting

### Nested Event Sink

Sub-agent events are forwarded to the parent's event stream, **nested under the task call**:

```go
func subSinkFor(parentID string, parent event.Sink) event.Sink {
    return event.FuncSink(func(e event.Event) {
        switch e.Kind {
        case event.ToolDispatch, event.ToolResult:
            e.Tool.ParentID = parentID
            e.Tool.ID = parentID + "/" + e.Tool.ID
            parent.Emit(e)
        }
    })
}
```

Only `ToolDispatch` and `ToolResult` events are forwarded — the sub-agent's own turn/usage/text/reasoning events are dropped. The forwarded call IDs are namespaced with the parent ID (`parentID/childID`) so a sub-agent call never collides with a parent call in the frontend's dispatch→result matching.

### Context Propagation

The parent's call context carries three pieces of state into each tool call:

1. **`parentID`**: The executing call's ID — used to nest sub-agent events
2. **`sink`**: The agent's event sink — used to forward nested events
3. **`asker`**: The interactive user-prompting interface — the `ask` tool uses this

The `task` tool reads these via `CallContext(ctx)` to build the nested sink. In headless runs (no interactive user), the asker is nil and the `ask` tool returns a "decide for yourself" result.

## Background Execution

### Foreground vs Background

The `task` tool supports two execution modes:

| Mode | Behavior | Use Case |
|------|----------|----------|
| **Foreground** (default) | Synchronous — blocks until the sub-agent finishes | When the answer is needed immediately |
| **Background** (`run_in_background: true`) | Asynchronous — returns a job ID immediately, keeps running across turns | For long, independent sub-tasks |

### Background Flow

1. The task tool registers a job with the session's `jobs.Manager`
2. The nested sink is captured immediately (before the job context replaces the call context)
3. The job runs on a goroutine under the manager's session-scoped context
4. The tool returns immediately with a message like `Started background task "analyze-api" (task-3)`
5. The model can continue working while the sub-agent runs
6. `DrainCompletedNote()` feeds completion notices into the next turn so the model learns when jobs finish
7. The model can use `bash_output` or `wait` to retrieve the final answer

### Job Context

Background tasks run under the `jobs.Manager`'s root context, which has the session's lifetime — not a single turn. This means:

- A background task started in one turn keeps running across turns
- Cancellation happens only when the controller closes or `kill_shell` is called
- The sub-agent still compacts its own context independently

## RunSubAgent — Shared Core

```go
func RunSubAgent(ctx context.Context, prov provider.Provider, reg *tool.Registry,
    sysPrompt, prompt string, opts Options, sink event.Sink) (string, error)
```

`RunSubAgent` is the shared core behind both the `task` tool and subagent skills. A caller supplies:

- The system prompt (the task persona or the skill body)
- The tool registry (already filtered)
- Run options (model budget, gate)
- The event sink (nested under the parent call, or `event.Discard`)

This abstraction ensures that skill-based sub-agents and explicit `task` invocations behave identically — same step budget logic, same compaction, same event nesting.

## Permission Gate Inheritance

Sub-agents inherit the parent's `Gate` interface, but the controller wires the **headless variant**:

- **Deny rules still bite** — a sub-agent cannot bypass permission restrictions
- **Interactive prompts are never triggered** — there is no UI to answer an approval request
- **Blocked calls return immediately** with a "blocked" result the sub-agent can adapt to

This ensures that autonomous sub-agents respect the same security boundaries as the parent while never blocking on an interactive prompt that no one can answer.

## Schema

```json
{
  "type": "object",
  "properties": {
    "prompt": {
      "type": "string",
      "description": "What the sub-agent should accomplish. Be specific about the deliverable."
    },
    "description": {
      "type": "string",
      "description": "Short label for the sub-task (3-7 words)."
    },
    "tools": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Optional tool whitelist. Subagent/skill meta-tools are still excluded."
    },
    "max_steps": {
      "type": "integer",
      "minimum": 1,
      "description": "Optional cap on tool-call rounds. Defaults to half the parent's cap (min 5)."
    },
    "run_in_background": {
      "type": "boolean",
      "description": "Run asynchronously: returns a job id immediately and keeps working across turns."
    }
  },
  "required": ["prompt"]
}
```

## Usage Patterns

### Focused Exploration

Delegate multi-file exploration to keep the parent's context clean:

```
task(prompt="Find every place that calls authenticate() and summarize the call patterns", 
     description="analyze auth calls")
```

### Parallel Research

While not parallelizable directly (task is a writer tool), background tasks enable concurrent work:

```
task(prompt="Audit the payment module for SQL injection vulnerabilities", 
     description="security audit", 
     run_in_background=true)
```

### Scoped Tool Access

Restrict a sub-agent to read-only exploration:

```
task(prompt="Map the data flow from the API layer to the database", 
     tools=["read_file", "grep", "ls", "glob"])
```

### Budget Control

Limit a sub-agent's autonomy with a step cap:

```
task(prompt="Fix the flaky test in auth_test.go", 
     max_steps=10)
```
