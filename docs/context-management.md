# Context Management & Compaction

Reasonix is designed for long-running sessions where context windows fill up. The context management system keeps the prompt within the model's token budget while respecting the cache-first design: the system prompt prefix must stay byte-stable across turns to keep DeepSeek's automatic prefix cache warm. Compaction is the only point where the prefix is deliberately rewritten — a rare, controlled cache-reset.

## The Problem

Long coding tasks produce many turns, each adding messages to the session. Eventually the total prompt tokens approach or exceed the model's context window. Without intervention, the provider returns a `finish_reason: "length"` error and the agent cannot continue.

## The Solution: Low-Frequency Compaction

Compaction summarizes the older middle of the conversation into a single briefing message, keeping a verbatim recent tail. The session becomes:

```
[System Prompt (cache-stable prefix)] → [Summary] → [Recent N messages (verbatim)]
```

### Key Parameters

| Parameter | Default | Description |
|-----------|---------|-------------|
| `context_window` | Per-provider | Token limit for the model (0 disables compaction) |
| `soft_compact_ratio` | 0.5 | Emit a one-shot notice when prompt reaches this fraction |
| `compact_ratio` | 0.8 | Try compacting when prompt reaches this fraction |
| `compact_force_ratio` | 0.9 | Force compaction at this high-water mark |
| `recent_keep` | 8 | Minimum number of verbatim messages in the recent tail |

### When Compaction Fires

`maybeCompact()` is called after each tool batch:

1. **No usage data** → skip (can't measure).
2. **Ratio < compact_ratio** → skip (plenty of headroom).
3. **Ratio < compactForceRatio and not first attempt** → skip (give it another turn).
4. **Otherwise** → run `compact()`.

Additionally, `CompactNow()` is available for manual `/compact` commands.

### The Compaction Process

1. **Identify the boundary** — find a message index that splits the session into old (to summarize) and recent (to keep verbatim). The boundary is aligned backward off any tool result so the recent tail never begins with an orphan tool message whose `tool_calls` were summarized away.

2. **Build the summary prompt** — include:
   - The system prompt (for context)
   - The old messages
   - Instructions to produce a concise briefing
   - Any `PreCompact` hook output (extra guidance)
   - Focus instructions from `/compact <focus>` if manual

3. **Stream the summary** — using the executor's own provider, no tools. The summary replaces the old messages.

4. **Replace the session** — `session.Replace()` swaps the entire message log. The `rewriteVersion` is bumped for cache-shape diagnostics.

5. **Archive originals** — dropped messages are saved to `~/.config/reasonix/archive/<timestamp>.jsonl` so the full history stays traceable.

### Stuck Detection

If compaction can't get the prompt under the window (consecutive compactions without progress), `compactStuck` latches and auto-compaction pauses instead of looping. The user is notified and can manually compact or adjust settings.

## Cache-First Design

The cache-first design governs everything about how the prompt is constructed:

### System Prompt Prefix

The system prompt prefix is built from:
1. Base system prompt (from config or `system_prompt_file`)
2. Memory block (from `memory.Set.Block()`)
3. Tool schemas (from `tool.Registry.Schemas()`)

This prefix must stay **byte-for-byte identical** across turns. Any change (even whitespace) causes the entire prefix to miss DeepSeek's cache.

### What Changes the Prefix

| Change | When | Impact |
|--------|------|--------|
| Memory edit | On disk, applied next session | Prefix changes on next session |
| Model switch | Rebuild controller | Full prefix rebuild |
| Tool enable/disable | Config change | Schema order may change |
| Plan mode toggle | Runtime | No prefix change (gating is execute-time) |
| Compaction | When context is full | Summary replaces old messages |

### Memory Update Without Cache Break

When a user adds memory mid-session (via `#<note>`, `/remember`, or the memory panel), the change is applied through the **turn tail** — composed onto the next outgoing message rather than mutating the prefix. This way, the new memory takes effect this session without busting the cache. It joins the prefix naturally on the next session.

### Plan Mode Is Cache-Friendly

Toggling plan mode does not change the system prompt, tool schemas, or message history. The gating happens at execute time — the model sees a "blocked" result and adapts. This means toggling plan mode costs nothing in cache hits.

## Prefix Shape Diagnostics

The agent tracks the prefix shape across turns (`PrefixShape` / `CaptureShape`) and compares it with usage data. When the prefix shape changes between turns (e.g. tool schemas were reordered, or a tool was added/removed), the cache miss is explained to the user:

```
cache miss: prefix shape changed (tools schema differs)
```

This helps users understand why cache hit rate dropped and take corrective action.

## Aggregate Cache Statistics

The agent accumulates cache hit/miss tokens across every API call in the session (via `sessCacheHit` / `sessCacheMiss` atomic counters). These are NOT reset on compaction — compaction only rewrites session messages, not the aggregate counters. The status line shows the aggregate hit rate (Σhit / Σ(hit+miss)), which is a steadier, cost-oriented number than the single-turn rate.

## Configuration

```toml
[agent]
soft_compact_ratio  = 0.5   # notice only
compact_ratio       = 0.8   # try compacting
compact_force_ratio = 0.9   # force compacting
```

Set `context_window = 0` on a provider to disable compaction for that provider.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Agent Loop & Coordinator](agent-loop-coordinator.md)
- [Memory & Skills](memory-skills.md)
- [Hooks System](hooks-system.md)
