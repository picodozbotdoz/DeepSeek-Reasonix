# Cache Diagnostics & Prefix Stability

Reasonix is built around a **cache-first** design: the system prompt and tool schemas form a stable prefix that should be byte-identical across turns so the provider's automatic prefix cache stays warm. When the prefix changes — even by one byte — the entire prompt becomes a cache miss, costing time and money. The cache diagnostics system tracks prefix stability, explains why misses happen, and helps users and developers optimize cache behavior.

## Why Cache Matters

DeepSeek's API (and most OpenAI-compatible providers) offers automatic prefix caching: if the beginning of a prompt matches a previously cached prefix, the cached portion is served from memory rather than recomputed. This means:

- **Lower latency**: Cached tokens don't need to be processed again
- **Lower cost**: Cached tokens are typically billed at a reduced rate
- **Higher throughput**: More of the context window is available for new content

The catch is that the prefix must be **byte-stable** — any change to the system prompt, tool schemas, or early message history invalidates the entire cache. This is why Reasonix goes to great lengths to keep the prefix stable across turns.

## Prefix Shape

The `PrefixShape` struct captures a snapshot of the current prefix state:

```go
type PrefixShape struct {
    SystemHash        string  // SHA-256 hash of the system prompt
    ToolsHash         string  // SHA-256 hash of the tool schemas JSON
    PrefixHash        string  // Combined hash of system + tools
    LogRewriteVersion int     // Bumped each time the message log is rewritten
    ToolSchemaTokens  int     // Estimated token count of tool schemas
}
```

### Shape Capture

`CaptureShape` takes a snapshot of the current prefix state at the start of each turn:

```go
func CaptureShape(systemPrompt string, schemas []provider.ToolSchema, rewriteVersion int) PrefixShape
```

Tool schemas are normalized (sorted by name, description, parameters) before hashing, so the order of tool registration doesn't affect the hash. This is important because `init()` registration order can vary between builds.

### Shape Comparison

`CompareShape` returns diagnostics describing what changed between two shapes:

```go
func CompareShape(prev, cur PrefixShape, usage *provider.Usage) CacheDiagnostics
```

The diagnostics identify which component(s) changed:

| Reason | Meaning |
|--------|---------|
| `system` | The system prompt text changed |
| `tools` | The tool schemas changed (tool added, removed, or modified) |
| `log_rewrite` | The message log was rewritten (compaction or folding) |

## Cache Diagnostics in the Event Stream

Every `Usage` event carries `CacheDiagnostics`:

```go
type CacheDiagnostics struct {
    PrefixHash          string
    PrefixChanged       bool
    PrefixChangeReasons []string  // "system", "tools", "log_rewrite"
    SystemHash          string
    ToolsHash           string
    LogRewriteVersion   int
    ToolSchemaTokens    int
    CacheMissTokens     int
    CacheHitTokens      int
}
```

### Usage Line

Both the TextSink and the chat TUI render the cache diagnostics as part of the per-turn usage line:

```
  · 12345 tok · in 8192 (6144 cached / 2048 new) · out 512 (128 reasoning) · $0.0012 · cache prefix changed: log_rewrite
```

Key formatting decisions:

- **Cache is reported as absolute** "(N cached / M new)" rather than as a percentage. A turn that adds a lot of fresh content shouldn't read as "cache broke" — the cached prefix is still hitting, the denominator just grew.
- **Reasoning tokens** are shown as a subset of completion tokens, highlighting the chain-of-thought cost.
- **Cost** is shown when pricing is configured.
- **Prefix churn** is appended when the prefix changed, naming the specific reason(s).

## Session-Level Cache Tracking

The agent accumulates cache statistics across every API call in the session:

```go
sessCacheHit  atomic.Int64  // cumulative cache hit tokens
sessCacheMiss atomic.Int64  // cumulative cache miss tokens
```

These are **NOT reset on compaction** — compaction only rewrites `session.Messages`, so the aggregate never craters when the prefix is summarized away. The status line shows the aggregate hit rate (`Σhit/Σ(hit+miss)`), which is a steadier, cost-oriented number than the single-turn rate.

### Cache Hit Rate Display

The chat TUI's status line shows two cache metrics:

- **Latest-turn hit rate**: From the most recent `Usage` event
- **Session-average hit rate**: From the cumulative `SessionCache()` counters

This lets users distinguish between "this turn was a miss" and "the session overall is cache-friendly."

## What Breaks the Cache

### System Prompt Changes

The system prompt is built from configuration, memory files, and project instructions. Changes to any of these between turns break the prefix:

- Adding/removing memory files via `/remember` or `/forget`
- Changing project instructions
- Switching models (which may change the system prompt format)
- Language changes (which affect i18n strings in the prompt)

### Tool Schema Changes

Adding or removing tools changes the tool schemas JSON, breaking the prefix:

- Starting/stopping MCP servers (which add/remove tools)
- Enabling/disabling skills
- Changes to tool descriptions

### Log Rewrites (Compaction)

Compaction is the only intentional cache-reset point. When compaction fires:

1. The `rewriteVersion` is bumped
2. The middle of the message history is replaced with a summary
3. The entire prefix changes because the message sequence is different

This is why compaction is a **low-frequency** operation — it only fires when the prompt nears `compactRatio` (default 80%) of the context window, and the soft-ratio notice (50%) gives advance warning.

### Plan Mode

`SetPlanMode` does **NOT** break the cache. The system prompt, tools schema, and message history are left untouched — the toggle only affects the permission gate at execute time. The model sees a "blocked" result it can adapt to, rather than a changed prompt.

## Tool Schema Token Costs

The `SchemaTokenCosts` function estimates the per-tool token cost:

```go
func SchemaTokenCosts(schemas []provider.ToolSchema) []ToolSchemaCost
```

This helps diagnose situations where tool schemas consume a significant portion of the context window. Each tool's JSON schema (name + description + parameters) is measured and reported.

## Optimization Strategies

### Keep the Prefix Stable

- Avoid changing memory files or project instructions between turns
- Don't add/remove MCP servers mid-session unless necessary
- Use `/compact` strategically — compaction is a deliberate cache reset

### Minimize Tool Schema Overhead

- Only enable MCP servers you need — each adds tools to the schema
- Disable unused skills to reduce the tool count
- Monitor the `ToolSchemaTokens` in diagnostics to understand the overhead

### Monitor Cache Hit Rate

- Watch the usage line for cache hit/miss ratios
- If the session-average rate is consistently low, investigate what's changing the prefix
- If compaction fires frequently, consider raising `context_window` or reducing tool output

### Compaction Timing

- The soft-ratio notice (50%) warns you before compaction fires
- Compaction at 80% gives you the most cache-stable turns before the reset
- Manual `/compact` lets you choose when to reset, rather than waiting for the auto trigger
