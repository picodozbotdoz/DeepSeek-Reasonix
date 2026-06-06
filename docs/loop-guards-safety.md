# Loop Guards & Safety

Reasonix runs autonomous tool loops — the model decides which tools to call, calls them, and decides again. Without guards, a model can get stuck in infinite loops: calling the same failing command with slightly reworded arguments, repeating a successful write operation, or refusing to stop when a tool's output is truncated. The loop guard system detects these patterns and intervenes to redirect the model, while other safety mechanisms prevent the agent from producing unverified or incomplete work.

## Storm Breaker

### The Problem

When a tool call fails, the model often re-attempts with cosmetically different arguments — rewording an essay, reordering an object, or slightly modifying a command. These retries fail identically every time because the underlying issue (e.g., truncated arguments, wrong file path) hasn't changed. The model is "stuck in a storm" — a death spiral of repeated identical failures.

### Detection

The storm breaker keys on each call's **(tool, error)** pair, NOT on its arguments:

```go
stormSig   string  // signature of repeated failure
stormCount int     // consecutive turns with the same signature
```

This is deliberate — a stuck model reworks the arguments cosmetically while failing the same way. Keying on arguments would miss the loop entirely because the args differ on each retry. Errors that embed their subject (e.g., "file not found: /x") differ per target, so genuine varied probing does not collapse to one signature.

### Signature Construction

```go
func batchStormSignature(calls []provider.ToolCall, outcomes []toolOutcome) (string, bool)
```

The signature is built by concatenating each call's `(name, error)` pair. A turn is a fixation candidate **only** when every call errored and none was merely blocked by plan mode/permissions. Any success, any block, or a different batch shape resets the counter.

### Threshold & Intervention

```go
const stormBreakThreshold = 3
```

After 3 consecutive identical failure signatures, the storm breaker rewrites the model-facing result:

```
[loop guard] "bash" has now failed 3 times in a row with the same error. 
Re-sending it — even with the wording changed — will not help: the calls keep 
failing the same way. Change approach: if an argument is being truncated, write 
less in one call and split the work into several smaller calls; otherwise fix the 
arguments, use a different tool, or explain the blocker in your final answer.
```

The loop guard also emits a `Notice` event so the user sees what's happening:

```
loop guard: bash failed 3× the same way — nudging the model to change approach
```

### Reset Conditions

The storm counter resets whenever:
- A turn produces any success
- A call is blocked (plan mode / permission) — these carry clear messages the model can act on
- The failure signature changes — a different error shape means the model is making varied progress

## Repeat Success Guard

### The Problem

The storm breaker only catches failure loops. The complementary pattern is a model that keeps doing the same **successful** write operation — a no-op/write loop where there is no error for the failure-only storm breaker to see.

### Detection

```go
repeatSuccessCounts map[string]int  // tool name → consecutive repeat count
```

The agent tracks write-like tool calls that have already succeeded in the current user turn. Each successful writer tool call increments the counter for that tool name.

### Threshold & Intervention

```go
const repeatSuccessBreakThreshold = 2
```

After 2 repetitions of the same successful write tool, subsequent identical calls are blocked:

```
blocked by loop guard: "write_file" has already succeeded 2 times on the same 
content in this turn. If the write is genuinely different, use a different tool 
or explain why the repetition is needed.
```

The threshold of 2 gives the model room for a natural self-correction (write, verify, fix) while catching the third repeat, which is usually a no-op loop.

## Final Readiness System

### The Problem

Without enforcement, the model can give a final answer before completing its work — declaring a task done while todos remain incomplete, or answering before running verification commands after a write. This produces answers that look complete but aren't backed by host-observable evidence.

### Evidence Ledger

The evidence ledger (`internal/evidence/`) records every tool call's receipt for the current turn:

```go
type Receipt struct {
    ToolName string          // tool name
    Args     json.RawMessage // original arguments
    Success  bool            // whether the call succeeded
    Command  string          // bash command (if applicable)
    Step     string          // complete_step step (if applicable)
    TodoStep *TodoStepMatch  // matched todo item
    Paths    []string        // file paths touched
    Read     bool            // whether the call was a read operation
    Write    bool            // whether the call was a write operation
    Todos    []TodoItem      // full todo list (for todo_write)
}
```

### Readiness Checks

When the model produces a final answer (no tool calls), the agent runs readiness checks:

1. **Incomplete todos**: If the latest `todo_write` still has pending or in-progress items, the final answer is blocked
2. **Missing verification commands**: If project checks (from "Reasonix host checks" in memory) specify commands that should run after a write, and they haven't been run, the final answer is blocked
3. **Missing complete_step**: If a `todo_write` was followed by a write, but no `complete_step` was called, the final answer is blocked

### Blocking Behavior

When readiness fails:

```go
const maxFinalReadinessBlocks = 3
```

1. The model's final answer is intercepted
2. A retry message is injected: "Host final-answer readiness check failed. Before giving a final answer, address the missing host-observable receipts: ..."
3. The model continues with additional tool calls
4. After 3 consecutive readiness blocks, the turn errors out — the model is truly stuck

### Project Checks

Structured project instructions can define "Reasonix host checks" — bash commands that must run after a write to verify the change:

```markdown
## Reasonix host checks
- go build ./...
- go test ./...
```

These are extracted by `instruction.ExtractHostChecks()` and enforced by the readiness system. The agent checks that each command has been successfully run via `bash` after the latest write operation.

## Plan Mode

### The Mechanism

```go
planMode atomic.Bool
```

Plan mode is a read-only gate that refuses any tool call whose `ReadOnly()` is false. When active:

- Writer tools return: `blocked: "write_file" is a writer tool and plan mode is read-only. Keep exploring with read-only tools, then write your plan as your reply — the user will be asked to approve it before any changes are made.`
- The cache-stable bits — system prompt, tools schema, message history — are left untouched, so the toggle costs nothing in cache hits
- The model sees "blocked" results and can adapt its approach without the prefix changing

### Interaction with Other Guards

Plan mode interacts with the permission gate in sequence:

1. **Plan mode check**: Is the tool read-only? If not and plan mode is on → blocked
2. **Permission gate check**: Does the gate allow this call? If not → blocked (with a distinct reason)

Both types of blocks reset the storm breaker counter because they carry clear, distinct messages the model can already act on.

## Max Steps Guard

```go
maxSteps int  // positive = hard cap, 0/negative = unbounded
```

When `maxSteps` is positive, the run loop stops after that many tool-call rounds:

```
paused after 50 tool-call rounds (agent.max_steps) — the work so far is saved; 
send another message to continue, or set max_steps higher or to 0 for no limit
```

This is the **ultimate backstop** — the loop guards try to redirect the model before it burns the entire budget, but max_steps ensures the loop terminates regardless. The work is saved in the session, so the user can pick up where they left off.

## Tool Output Truncation

```go
const maxToolOutputBytes = 32 * 1024  // ~32KB ≈ 8K tokens
```

Tool results exceeding this limit are head+tailed (first 16KB + last 16KB) with a truncation notice. This prevents a single accidental "read this 5MB log" from blowing the context window before the next compaction runs. The truncation also helps the storm breaker — a model that keeps producing over-long arguments gets truncated the same way each time, creating a consistent failure signature the storm breaker can detect.

## Parallel Execution Safety

Tool calls within a batch are partitioned for parallel execution:

```go
func partitionToolCalls(r *tool.Registry, calls []provider.ToolCall) []toolCallBatch
```

- **Read-only tools**: Run in parallel (up to 8 concurrent)
- **Writer tools**: Run serially (one at a time, in provider order)
- **`complete_step` and `todo_write`**: Always serial — they read the turn's evidence ledger, so every prior call's receipt must be recorded before they run
- **Unknown tools**: Always serial — their side effects are unknowable

This ensures write/read ordering stays provider-ordered while maximizing throughput for independent read operations.
