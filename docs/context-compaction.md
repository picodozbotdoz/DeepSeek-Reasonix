# Context Compaction

**Source:** `internal/agent/compact.go`

## Overview

DeepSeek-Reasonix's conversation history grows append-only — each turn adds user messages, assistant responses, and tool call/result pairs to the session. This growth is intentional: a stable prefix enables provider-side prompt caching, which dramatically reduces latency and cost on subsequent turns. However, every context window has a finite token budget. When the prompt grows too large, the agent must compact it: summarize the older middle of the conversation, replace it with a summary, and keep only the system prompt and a recent verbatim tail.

The compaction engine is designed around several key principles:

1. **Cache-first**: Compaction is deferred as long as possible. Between 50% and 80% of the context window, the agent reports growing context but does not compact, because rewriting the prefix would destroy the cache.
2. **Budget-based, not fraction-based**: The verbatim tail is kept within a fixed token budget (default 16,384 tokens), not a fraction of the window. This ensures that a huge window still compacts rarely while a small one still lands below the trigger.
3. **Full traceability**: Dropped messages are archived as timestamped JSONL files before summarization, so the full history is never truly lost.
4. **Stuck detection**: If compaction fires on every consecutive turn (meaning the kept tail alone exceeds the trigger), the system auto-pauses with a warning rather than burning tokens in an infinite summarization loop.

---

## Constants

```go
const (
    defaultSoftCompactRatio  = 0.5   // soft notice threshold
    defaultCompactRatio      = 0.8   // compaction trigger
    defaultCompactForceRatio = 0.9   // force compaction even for small regions
    defaultCompactTarget     = 0.5   // maximum tail as fraction of window
    defaultTailTokens        = 16384 // verbatim tail budget in tokens
    minRecentKeep            = 2     // never keep fewer recent messages
    minCompactMessages       = 2     // skip compaction below this many messages
    fallbackTokPerChar       = 0.25  // ~4 chars/token fallback ratio
)
```

### Ratio Semantics

- **`defaultSoftCompactRatio` (0.5)**: When the prompt reaches 50% of the context window, the agent emits a one-time notice that context is growing. No compaction occurs — the cache-stable prefix is preserved. This gives the user visibility into approaching limits without the cost of summarization.

- **`defaultCompactRatio` (0.8)**: The compaction trigger. When the prompt reaches 80% of the window, the agent compacts. This threshold is high enough to maximize cache utility while leaving enough headroom for the next turn's tool results.

- **`defaultCompactForceRatio` (0.9)**: The force threshold. At 90% of the window, compaction is forced even if the region is small (bypassing the `foldEconomics` check). This is the high-water mark that prevents the session from exceeding the context window entirely.

- **`defaultCompactTarget` (0.5)**: The safety cap on the verbatim tail. The tail never exceeds 50% of the window, even if `defaultTailTokens` would allow more. This ensures that compaction actually frees significant space rather than keeping half the window verbatim.

---

## maybeCompact

```go
func (a *Agent) maybeCompact(ctx context.Context, u *provider.Usage)
```

Called after every LLM turn with the latest token usage. It is a no-op when compaction is disabled (no window configured) or usage is unavailable.

### Soft Notice (50%–80%)

Between the soft ratio and the trigger, `maybeCompact` reports growing context once without rewriting the prefix:

```go
if u.PromptTokens >= soft && u.PromptTokens < high && !a.softCompactNoticed {
    a.softCompactNoticed = true
    a.sink.Emit(event.Event{Kind: event.Notice, ...})
    return
}
```

The `softCompactNoticed` flag ensures the notice fires only once per "rising edge" — if the prompt dips below the soft ratio and rises again, the notice fires again. This is a deliberate UX choice: a single notice at 50% is informational; repeated notices would be noise.

### Healthy Turn (Below Trigger)

When the prompt is below the trigger, the agent clears the stuck latch and consecutive counter:

```go
if u.PromptTokens < high {
    a.consecutiveCompacts = 0
    a.compactStuck = false
    a.softCompactNoticed = false
    return
}
```

This is the normal post-compaction state: a successful compaction drops the prompt below the trigger, and the next turn clears the counters. The agent is "healthy" again.

### Stuck Detection

If compaction fires on consecutive turns, it means the kept tail alone exceeds the trigger — the system prompt plus one verbatim turn is bigger than the window allows. After two consecutive compactions:

```go
a.consecutiveCompacts++
if a.consecutiveCompacts >= 2 {
    a.compactStuck = true
    a.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, ...})
}
```

The agent pauses auto-compaction and emits a warning:

```
context_window=32768 is too small for compaction to help (the system prompt
plus one turn already exceeds 80% of it); raise context_window or shrink
tool output. Auto-compaction paused until the prompt drops.
```

This prevents an infinite summarization loop where every turn triggers compaction, which summarizes, which still exceeds the trigger, which triggers compaction again — burning tokens and latency for no benefit.

---

## compact

```go
func (a *Agent) compact(ctx context.Context, trigger, instructions string, force bool) error
```

The core compaction function. It summarizes the older middle of the session and replaces it in place.

### Step 1: Plan the Compaction Region

```go
head, start, ok := a.planCompaction(msgs, minCompactMessages)
```

`head` is the count of leading messages preserved verbatim (the system prompt, if any). `start` is where the preserved recent tail begins. The region to summarize is `msgs[head:start]`.

If the region is too small (fewer than `minCompactMessages`), the agent tries again with `min = 1` to handle single huge messages. If still too small, it returns nil — the recent tail already covers everything worth keeping.

### Step 2: Fold Economics

```go
if !force && !foldEconomics(region) {
    return nil
}
```

The `foldEconomics` function estimates whether the region contains enough tokens (≥400) to justify the summarization API call. A very small region might cost more in API round-trips and latency than it saves in token budget. The `force` flag (set by the high-water mark or manual `/compact`) bypasses this check.

### Step 3: Emit CompactionStarted

```go
a.sink.Emit(event.Event{Kind: event.CompactionStarted, Compaction: event.Compaction{Trigger: trigger}})
```

This tells the UI to show a "compacting…" placeholder while the network summarization is in progress. The `trigger` field is `"auto"` or `"manual"`, so the UI can label the card appropriately.

### Step 4: PreCompact Hook

```go
if a.hooks != nil {
    if hookInstr := a.hooks.PreCompact(ctx, trigger); hookInstr != "" {
        instructions += "\n" + hookInstr
    }
}
```

A `PreCompact` hook can contribute extra summary guidance. Its stdout is appended to any explicit `/compact <focus>` text, giving users and integrations a way to steer what the summary preserves.

### Step 5: Archive Dropped Messages

```go
if a.archiveDir != "" {
    path, err := archiveMessages(a.archiveDir, region)
    ...
}
```

Before summarizing, the dropped messages are written to a timestamped JSONL file for full traceability. If archiving fails, compaction is aborted (the `CompactionDone` event resolves the "compacting…" placeholder).

### Step 6: Summarize

```go
summary, err := a.summarize(ctx, region, instructions)
```

The region is rendered as a readable transcript and sent to the executor's own provider (no tools) with a structured system prompt. The summary is returned as a single string.

### Step 7: Replace in Place

```go
compacted := make([]provider.Message, 0, head+1+len(msgs)-start)
compacted = append(compacted, msgs[:head]...)
compacted = append(compacted, provider.Message{
    Role: provider.RoleUser,
    Content: summaryTagOpen + "\n" +
        "Summary of earlier conversation (older messages were compacted to save context):\n" +
        summary + "\n" +
        summaryTagClose,
})
compacted = append(compacted, msgs[start:]...)
a.session.Replace(compacted)
a.session.IncrementRewrite()
```

The summary is wrapped in `<compaction-summary>` tags so the model can distinguish it from live user input and later strip or skip it when reasoning about the current turn. The session is replaced atomically, and the rewrite counter is incremented (which resets the provider-side cache).

### Step 8: Emit CompactionDone

```go
a.sink.Emit(event.Event{Kind: event.CompactionDone, Compaction: event.Compaction{
    Trigger: trigger, Messages: len(region), Summary: summary, Archive: archived,
}})
```

This tells the UI to replace the "compacting…" placeholder with the actual summary card. The `Archive` field is the path to the JSONL file, so the UI can offer a "show original messages" action.

---

## Summary Prompt Structure

The `summarySystemPrompt` is a carefully designed prompt that steers the summarizer to produce a briefing the agent can resume work from:

```
## Goal
The user's request and intent, kept close to their own words.

## Decisions & rationale
Key choices made so far and why — so they are not re-litigated or reversed.

## Files & code
Files read or modified, with specific facts: signatures, line locations,
data shapes, and exact edits applied.

## Commands & outcomes
Commands run and their relevant results.

## Errors & fixes
Problems hit and how they were resolved (or not).

## Pending & next step
What is still in progress or unstarted, and the single most concrete next action.
```

Each section is designed to preserve exactly what a coding agent needs to resume mid-task:

- **Goal**: The verbatim user request, so the agent doesn't drift from the original intent
- **Decisions & rationale**: Prevents the agent from re-litigating choices already made (a common failure mode after compaction)
- **Files & code**: Concrete facts that let the agent act without re-reading everything — signatures, line locations, data shapes, exact edits
- **Commands & outcomes**: What passed, what failed, and the error text that matters
- **Errors & fixes**: Prevents the agent from repeating the same dead ends
- **Pending & next step**: The most concrete next action, so the agent can pick up where it left off

The prompt ends with strict rules: "be terse — bullet points and fragments, not prose. Preserve identifiers, paths, and numbers exactly. Do NOT invent anything not present in the messages."

---

## planCompaction

```go
func (a *Agent) planCompaction(msgs []provider.Message, min int) (head, start int, ok bool)
```

Locates the region to summarize. `head` is the count of leading messages preserved verbatim (the system prompt, if present, is always `head = 1`). `start` is where the preserved recent tail begins.

### Tail Budget

When a context window is configured, the tail budget is:

```go
budget := defaultTailTokens  // 16384
if maxByWin := int(float64(a.contextWindow) * defaultCompactTarget); maxByWin < budget {
    budget = maxByWin
}
```

The budget is capped at 50% of the window (`defaultCompactTarget`), so a 32K window gets at most 16K tokens of tail — never the full 16,384 if that would exceed half the window.

### No Window Fallback

When no window is configured (manual `/compact` on an unconfigured provider), the tail is a fixed count of recent messages, aligned off tool results so the tail never begins with an orphan whose assistant tool_calls were summarized away.

### Minimum Messages

If `start - head < min`, there are too few messages to compact and `ok` is `false`. The minimum is normally 2 (preventing trivial compaction of a single message) but drops to 1 for single huge messages.

---

## tailStart

```go
func tailStart(msgs []provider.Message, head, budgetTokens int, tokPerChar float64, minKeep int) int
```

Walks from newest → oldest, growing the verbatim tail until the next message would push its token estimate past `budgetTokens` (but never below `minKeep` messages). Then aligns the boundary back off any tool result:

```go
for start > head && start < len(msgs) && msgs[start].Role == provider.RoleTool {
    start--
}
```

This alignment is critical: if the tail begins with a tool result whose corresponding assistant tool_calls message was summarized away, the model sees an orphaned result with no context for why the call was made. Walking back to include the assistant message that issued the tool call ensures the tail is self-contained.

---

## tokPerChar

```go
func (a *Agent) tokPerChar() float64
```

Derives a tokens-per-character ratio from the last turn's real usage so per-message estimates track the provider's tokenizer without requiring a local one. Reasoning content is excluded from the char count because the provider strips it before counting.

The ratio is clamped to the range `[0.05, 2.0]` — ratios outside this range indicate a miscalculation (e.g., the prompt was cached and the usage report is inaccurate). Before any usage is available, the fallback is `0.25` (approximately 4 characters per token), which is a reasonable cross-language approximation for English-dominant text.

---

## SummarizeFrom & SummarizeUpTo

These two methods provide explicit boundary control for manual compaction:

### SummarizeFrom

```go
func (a *Agent) SummarizeFrom(ctx context.Context, fromIdx int) error
```

Replaces messages from `fromIdx` onward with a single summary, keeping everything before it verbatim ("summarize from here"). `fromIdx` must be a turn boundary (a user message), so the split never severs a tool_call/result pair.

### SummarizeUpTo

```go
func (a *Agent) SummarizeUpTo(ctx context.Context, toIdx int) error
```

Replaces messages before `toIdx` (after the system prompt) with a single summary, keeping `toIdx` onward verbatim ("summarize up to here"). Again, `toIdx` must be a turn boundary.

Both methods archive the dropped messages before summarizing (best-effort — archiving failure does not prevent compaction) and emit a `Notice` event with the count of messages summarized.

---

## archiveMessages

```go
func archiveMessages(dir string, msgs []provider.Message) (string, error)
```

Writes the dropped originals to a timestamped `.jsonl` file (one message per line) under the archive directory. The filename includes millisecond precision:

```
20240315-143022.456.jsonl
```

Each line is a complete JSON-serialized `provider.Message`, preserving all fields (role, content, tool calls, reasoning content, etc.). This provides full traceability — a user can reconstruct the exact conversation that was summarized, including tool arguments and results that the summary may have condensed.

The archive directory is created if it doesn't exist (`os.MkdirAll`). If writing fails, the error propagates and compaction is aborted (the original messages are preserved in the session).

---

## renderTranscript

```go
func renderTranscript(msgs []provider.Message) string
```

Flattens messages into a readable transcript for the summarizer:

```
[user]
Fix the login bug in auth.ts

[assistant calls read_file] {"path":"src/auth.ts"}

[tool read_file result]
<file contents>

[assistant]
The bug is on line 42 — the comparison uses === instead of ==.
```

System messages are rendered as `[system]`, user messages as `[user]`, assistant messages as `[assistant]` (with content and tool calls), and tool results as `[tool <name> result]`. This format preserves the conversational structure while stripping provider-specific formatting that would be meaningless to the summarizer.

---

## Summary Tags

```go
const (
    summaryTagOpen  = "<compaction-summary>"
    summaryTagClose = "</compaction-summary>"
)
```

The summary is wrapped in these tags when inserted into the session. This serves two purposes:

1. **Model visibility**: The model can identify which parts of the conversation are summaries (not live user input) and weight them appropriately — trusting the summary for context but not treating it as a direct user instruction.
2. **Programmatic extraction**: Downstream tooling can extract or strip summaries from the transcript for analysis, display, or re-compaction.

---

## Compaction Events

Two event kinds drive the UI's compaction experience:

- **`CompactionStarted`**: Emitted before the network summarization call. The UI shows a "compacting…" placeholder with the trigger label ("auto" or "manual").
- **`CompactionDone`**: Emitted after compaction completes. Carries the number of messages compacted, the summary text, and the archive path. The UI replaces the placeholder with a summary card that can be expanded to show the summary text.

If compaction fails after the `Started` event (e.g., the summarizer returns empty output, archiving fails), `emitCompactionAborted` emits a `CompactionDone` event with no summary, telling the UI to drop the placeholder.
