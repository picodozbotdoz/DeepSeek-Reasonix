# Context Window Intelligence — Design Document

**Feature**: Context Analysis Hooks (cahooks)
**Status**: Design
**Date**: 2026-06-13

---

## Problem

Reasonix has a 1M context window with near-100% prefix caching. The cached prefix contains the agent's full conversation history, tool results, file contents, and reasoning. This is a rich source of experience — but it's currently unused. The agent doesn't learn from its own patterns, errors, or successes.

Extracting insights from this context is nearly free: a background LLM call that sends the exact same prefix as the next normal turn gets 100% cache hit. The only cost is the short analysis prompt. The result goes to a file, not back into the conversation. The user never sees it happen.

## Goal

A user-configurable hook system that runs LLM analysis on the context window at various lifecycle points, writes insights to persistent files, and does so transparently — no conversation pollution, no recompilation, near-zero cost.

## Use Cases

| Use Case | Trigger | What It Produces |
|---|---|---|
| Tool pattern extraction | Every N turns | Common tool chains, failure patterns, unused tools |
| Error taxonomy | After turns with errors | Error categories, contexts, resolutions |
| Task decomposition learning | Pre-compaction | How tasks were broken down, success patterns |
| Session summary | Before `/new` | What was accomplished, files changed, patterns emerged |
| Context pressure analysis | Context > 70% | What to preserve, what can be pruned |
| User preference detection | Post-turn | Style corrections, implicit preferences |
| Verification gap detection | After complete_step | What "done" looks like per task type |

---

## Design

### Core Mechanism

The LLM is the analysis engine. The prefix is the data source. Shell scripts are the glue. Output files are the store.

At each hook point, Reasonix:

1. Checks conditions (turn count, context usage, error presence)
2. Runs a `beforeLLMCall` shell script to prepare the analysis prompt
3. Calls the LLM with the **exact same prefix** as the next normal turn (100% cache hit)
4. Writes the LLM response to a temp file
5. Runs an `afterLLMCall` shell script to process and persist the result

The analysis result **never enters the conversation history**. It's written to a JSONL file in `.reasonix/meta/`. The user sees nothing. The prefix stays stable. The cost is negligible.

### Why This Works

```
Normal turn:     [system prompt] [tools] [message history] → [user input] → LLM → response
                 ├────────────── prefix (100% cached) ──────────────────┤

Analysis call:   [system prompt] [tools] [message history] → [analysis prompt] → LLM → insight
                 ├────────────── prefix (100% cached) ──────────────────────────────┤
                 SAME PREFIX                                              SHORT PROMPT
                                                                          (small miss)
```

The prefix is byte-identical. DeepSeek's prefix cache returns 100% hit. The only uncached tokens are the analysis prompt (~200 tokens). On a 1M prefix, this costs essentially nothing.

### Flow Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│  Trigger Point                                                  │
│  (post_turn / pre_compact / every_n_turns / context_threshold) │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Check Conditions                                               │
│  (has_errors / has_tool_calls / context_usage > threshold)      │
└──────────────────────────┬──────────────────────────────────────┘
                           │ (condition met)
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  beforeLLMCall shell script                                     │
│  Input:  payload (session context + metrics) + config           │
│  Output: effective_prompt + effective_output_structure          │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  LLM Analysis Call                                              │
│  Prefix: exact same as next normal turn (100% cache hit)        │
│  Prompt: effective_user_prompt (short, ~200 tokens)             │
│  Output: JSON matching effective_output_structure               │
│  Cost: ~0 (only analysis prompt is uncached)                    │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Write LLM response to tmp file                                 │
│  (/tmp/reasonix-analysis-{session}-{timestamp}.json)            │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  afterLLMCall shell script                                      │
│  Input:  payload + tmp file path                                │
│  Action: read tmp → transform → append to output JSONL          │
└──────────────────────────┬──────────────────────────────────────┘
                           │
                           ▼
┌─────────────────────────────────────────────────────────────────┐
│  Hook Complete → Agent Continues                                │
│  (async hooks don't block the agent loop)                       │
└─────────────────────────────────────────────────────────────────┘
```

---

## Configuration

### File Location

- **Project**: `.reasonix/cahooks.json`
- **Global**: `~/.reasonix/cahooks.json`

Project config overrides global on name collision.

### Schema

```json
{
  "hooks": [
    {
      "name": "string (required, unique)",
      "enabled": true,

      "trigger": "enum (required)",
      "every_n": 5,
      "context_threshold": 0.7,
      "idle_seconds": 300,
      "condition": "enum (optional)",

      "before_llm_call": "path to shell script (optional)",
      "llm": {
        "prompt": "analysis instruction (required or from beforeLLMCall)",
        "output_structure": { "schema": "..." },
        "model": "model override (optional, default: deepseek-flash)"
      },
      "inject_into_context": false,
      "after_llm_call": "path to shell script (optional)",

      "output": {
        "dir": ".reasonix/meta",
        "file": "insights.jsonl"
      },

      "async": true,
      "timeout_ms": 30000
    }
  ]
}
```

### Trigger Types

| Trigger | When | Config Fields |
|---|---|---|
| `post_turn` | After each turn completes | — |
| `pre_compact` | Before context summarization | — |
| `pre_new_session` | Before `/new` clears history | — |
| `every_n_turns` | Every N turns | `every_n` (int) |
| `context_threshold` | When context usage exceeds threshold | `context_threshold` (float, 0.0-1.0) |
| `session_idle` | After N seconds of inactivity | `idle_seconds` (int) |

### Conditions

| Condition | Fires When |
|---|---|
| `always` | Every trigger (default) |
| `has_errors` | Turn produced error results |
| `has_tool_calls` | Turn made tool calls |
| `has_writes` | Turn modified files |
| `has_file_changes` | Turn changed any file content |

### beforeLLMCall Script

**Purpose**: Modify the analysis prompt and output structure based on current session state.

**Input** (JSON on stdin):
```json
{
  "event": "post_turn",
  "cwd": "/path/to/project",
  "turn": 12,
  "session_id": "abc123",
  "context_usage": 0.65,
  "context_window": 1000000,
  "tokens_used": 650000,
  "messages": ["..."],
  "tool_calls": ["..."],
  "tool_results": ["..."],
  "reasoning": "...",
  "errors": ["..."],
  "config": {
    "prompt": "Analyze tool call sequences...",
    "output_structure": { "patterns": [] }
  }
}
```

**Output** (JSON on stdout):
```json
{
  "user_prompt": "modified analysis instruction",
  "output_structure": { "modified": "schema" }
}
```

If omitted, `llm.prompt` and `llm.output_structure` are used as-is.

### afterLLMCall Script

**Purpose**: Process the LLM output before persisting.

**Input** (JSON on stdin):
```json
{
  "event": "post_turn",
  "cwd": "/path/to/project",
  "turn": 12,
  "session_id": "abc123",
  "context_usage": 0.65,
  "tmp_file": "/tmp/reasonix-analysis-abc123.json",
  "llm_output": { "raw": "llm response" },
  "config": {
    "output": {
      "dir": ".reasonix/meta",
      "file": "tool-patterns.jsonl"
    }
  }
}
```

**Action**: Script reads tmp file, transforms content, appends to output JSONL.

If omitted, raw LLM output is appended as-is.

### LLM Payload

The LLM call sends:

```
┌─────────────────────────────────────────────────────────────┐
│  System Prompt (exact same as next normal turn)             │
├─────────────────────────────────────────────────────────────┤
│  Tool Schemas (exact same as next normal turn)              │
├─────────────────────────────────────────────────────────────┤
│  Message History (exact same as next normal turn)           │
├─────────────────────────────────────────────────────────────┤
│  Analysis Prompt (from effective_user_prompt, ~200 tokens)  │
│  "Analyze the tool call sequences in this session.         │
│   Extract common chains, failure patterns, unused tools.   │
│   Return JSON: {patterns: [...], failures: [...], ...}"    │
└─────────────────────────────────────────────────────────────┘
```

The prefix (system + tools + history) is 100% cached. Only the analysis prompt is uncached.

---

## Output Format

### JSONL (Default)

Each analysis produces one JSON line appended to the output file:

```jsonl
{"session":"abc123","turn":12,"created_at":"2026-06-13T10:30:00Z","last_used_at":null,"analysis":{"patterns":[...],"failures":[...]}}
{"session":"abc123","turn":17,"created_at":"2026-06-13T10:35:00Z","last_used_at":"2026-06-13T11:00:00Z","analysis":{"patterns":[...],"failures":[...]}}
```

**Why JSONL**:
- Append-only (no file corruption on crash)
- Streaming (process line by line)
- Human-readable (one JSON object per line)
- Easy to query (`jq`, `grep`)
- Atomic per-line writes

### Insight Injection (`inject_into_context`)

When `inject_into_context: true`, insights from `.reasonix/meta/` are loaded into the next session's standing context. This enables the self-learning loop:

```
Session N → cahooks analyzes → writes to .reasonix/meta/insights.jsonl
    ↓
Session N+1 → loads insights from .reasonix/meta/ → injects as standing context
    ↓
Agent has learned from Session N's experience
```

**Injection mechanism**:
- At session start, scan `.reasonix/meta/*.jsonl`
- Load recent insights (last 7 days, or last N turns)
- Inject as a system message appendix: `# Learned from previous sessions\n\n{insights}`
- Update `last_used_at` timestamp on each injection

**Scope**: Session-scoped for now. Cross-session aggregation will be designed later.

### Other Formats

| Format | Use Case | Notes |
|---|---|---|
| `json` | Small sessions, single report | Overwrites file (not append) |
| `markdown` | Human-readable reports | Good for session summaries |
| `csv` | Data analysis, metrics | Good for numeric patterns |

### File Placeholders

| Placeholder | Value |
|---|---|
| `{date}` | Current date (YYYY-MM-DD) |
| `{session}` | Session ID |
| `{turn}` | Current turn number |
| `{name}` | Hook name |

Example: `tool-patterns-{date}.jsonl` → `tool-patterns-2026-06-13.jsonl`

---

## Examples

### Example 1: Tool Pattern Extraction

```json
{
  "name": "tool-patterns",
  "enabled": true,
  "trigger": "every_n_turns",
  "every_n": 5,
  "condition": "has_tool_calls",
  "llm": {
    "prompt": "Analyze tool call sequences in this session. Extract: (1) common tool chains (A→B→C), (2) tools that always fail together, (3) tools that are never used. Return JSON.",
    "output_structure": {
      "patterns": [{"chain": ["read_file", "edit_file"], "frequency": 12}],
      "failures": [{"tools": ["bash"], "error": "timeout", "count": 3}],
      "unused": ["web_fetch"]
    }
  },
  "output": {
    "dir": ".reasonix/meta",
    "file": "tool-patterns.jsonl"
  },
  "async": true,
  "timeout_ms": 30000
}
```

### Example 2: Session Summary Before /new

```json
{
  "name": "session-summary",
  "enabled": true,
  "trigger": "pre_new_session",
  "llm": {
    "prompt": "Summarize this session: what was accomplished, what files were changed, what patterns emerged, what would be useful to remember for future sessions.",
    "output_structure": {
      "accomplished": ["..."],
      "files_changed": ["..."],
      "patterns": ["..."],
      "memory": ["..."]
    }
  },
  "output": {
    "dir": ".reasonix/meta",
    "file": "sessions.jsonl"
  },
  "async": false,
  "timeout_ms": 60000
}
```

### Example 3: Context Pressure Analysis

```json
{
  "name": "context-pressure",
  "enabled": true,
  "trigger": "context_threshold",
  "context_threshold": 0.7,
  "llm": {
    "prompt": "The context is at 70% capacity. Analyze which information is most likely needed for the next 5 turns and which can be safely pruned. Return JSON with preserve and prune lists.",
    "output_structure": {
      "preserve": [{"type": "file", "path": "...", "reason": "..."}],
      "prune": [{"type": "tool_result", "turn": 5, "reason": "..."}]
    }
  },
  "output": {
    "dir": ".reasonix/meta",
    "file": "context-pressure.jsonl"
  },
  "async": true,
  "timeout_ms": 20000
}
```

### Example 4: Error Taxonomy with beforeLLMCall Script

```json
{
  "name": "error-taxonomy",
  "enabled": true,
  "trigger": "post_turn",
  "condition": "has_errors",
  "before_llm_call": "scripts/prepare-error-analysis.sh",
  "after_llm_call": "scripts/post-process-errors.sh",
  "output": {
    "dir": ".reasonix/meta",
    "file": "errors.jsonl"
  },
  "async": true,
  "timeout_ms": 15000
}
```

`scripts/prepare-error-analysis.sh`:
```bash
#!/bin/bash
INPUT=$(cat)
ERRORS=$(echo "$INPUT" | jq '.errors')
PROMPT="Categorize these errors from the current session:\n$ERRORS\n\nReturn JSON: {errors: [{type, context, resolution, frequency}]}"
echo "$INPUT" | jq --arg prompt "$PROMPT" '{
  user_prompt: $prompt,
  output_structure: {errors: [{type: "string", context: "string", resolution: "string", frequency: "number"}]}
}'
```

`scripts/post-process-errors.sh`:
```bash
#!/bin/bash
INPUT=$(cat)
TMP_FILE=$(echo "$INPUT" | jq -r '.tmp_file')
OUTPUT_DIR=$(echo "$INPUT" | jq -r '.config.output.dir')
OUTPUT_FILE=$(echo "$INPUT" | jq -r '.config.output.file')
SESSION_ID=$(echo "$INPUT" | jq -r '.session_id')
TURN=$(echo "$INPUT" | jq -r '.turn')
ENRICHED=$(jq -n --arg session "$SESSION_ID" --argjson turn "$TURN" --slurpfile data "$TMP_FILE" \
  '{session: $session, turn: $turn, timestamp: now | todate, errors: $data[0].errors}')
echo "$ENRICHED" >> "$OUTPUT_DIR/$OUTPUT_FILE"
```

---

## Implementation Plan

### Phase 1: Core Infrastructure

**Files to create**:
- `internal/cahooks/config.go` — Config loading, validation, schema
- `internal/cahooks/hook.go` — Hook execution (beforeLLMCall → LLM → afterLLMCall)
- `internal/cahooks/payload.go` — Payload construction from session context
- `internal/cahooks/trigger.go` — Trigger point registration and evaluation

**Files to modify**:
- `internal/hook/hook.go` — Add `PostTurn`, `PreCompact`, `PreNewSession` event constants
- `internal/agent/agent.go` — Add cahooks call site after `Run()` returns
- `internal/control/controller.go` — Add cahooks call site before `/new`

### Phase 2: LLM Integration

**Files to create**:
- `internal/cahooks/llm.go` — Build LLM request with exact prefix, call provider, handle response

**Key implementation detail**: The LLM call must use the **exact same prefix** as the next normal turn. This means:
1. Reuse the same `Session.Messages()` for history
2. Reuse the same `tool.Registry.Schemas()` for tools
3. Reuse the same system prompt
4. Append only the analysis prompt as the user message

### Phase 3: Output Handling

**Files to create**:
- `internal/cahooks/output.go` — File writing (JSONL append, JSON overwrite, etc.)
- `internal/cahooks/tmp.go` — Temp file management

### Phase 4: Trigger Points

**Integration points**:
- `agent.go:Run()` — After turn completes, before returning
- `agent.go:maybeCompact()` — Before compaction, if pre_compact hooks exist
- `controller.go:Send()` — Before `/new`, if pre_new_session hooks exist
- `agent.go:Run()` step loop — After each step, check every_n_turns and context_threshold

### Phase 5: Async Execution

**Files to modify**:
- `internal/cahooks/hook.go` — Add goroutine execution with timeout
- `internal/jobs/` — Optionally integrate with existing job manager

---

## Testing

### Unit Tests

- Config loading and validation
- Trigger condition evaluation
- Payload construction from session context
- JSONL append correctness
- Temp file cleanup

### Integration Tests

- Full hook execution with mock LLM
- beforeLLMCall script execution
- afterLLMCall script execution
- Async hook doesn't block agent loop
- Multiple hooks on same trigger point

### E2E Tests

- Real LLM call with 100% prefix cache hit (verify cache metrics)
- Tool pattern extraction produces valid JSONL
- Session summary captures all turns
- Context pressure analysis fires at threshold

---

## Risks and Mitigations

| Risk | Mitigation |
|---|---|
| LLM call adds latency to agent loop | `async: true` runs in goroutine; agent continues immediately |
| Analysis prompt pollutes prefix | Analysis prompt is a user message, not system prompt; it's consumed by the analysis call, not the next normal turn |
| Output files grow unbounded | Users can set rotation policies in afterLLMCall scripts |
| beforeLLMCall script fails | Fall back to default prompt; log warning; don't block agent |
| afterLLMCall script fails | Write raw LLM output; log warning; don't block agent |
| LLM returns invalid JSON | afterLLMCall script can validate and filter; output file gets raw response |
| Multiple cahooks compete for LLM calls | Each hook is independent; no shared state; parallel execution safe |

---

## Open Questions

1. **Prefix stability**: Does the analysis user message break the prefix for the next normal turn? **Answer: Test after develop & implement.** Need to verify that the analysis call's user message doesn't persist in the session history.

2. **Model selection**: Should cahooks use a cheaper model by default? **Answer: Yes.** Use `deepseek-flash` as the default cahooks model. The analysis doesn't need the strongest model.

3. **Insight injection**: Should there be an `inject_into_context` option? **Answer: Yes.** Add `inject_into_context: true` to load insights from `.reasonix/meta/` into the next session's standing context. This enables the self-learning loop.

4. **Cross-session insights**: Should insights from previous sessions be available to new sessions? **Answer: Yes, investigate later.** For now, insights are session-scoped. Cross-session aggregation and loading will be designed in a future iteration.

5. **Insight staleness**: How to handle insights that become outdated? **Answer: Track create date and last usability assessment date.** Each insight record includes `created_at` and `last_used_at` timestamps. Improvement (TTL, relevance scoring) will be added later.
