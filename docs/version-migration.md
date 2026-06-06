# Version Migration (v0.x → v1+)

Reasonix v1.0 was a ground-up rewrite in Go, replacing the v0.x TypeScript releases. This required a session migration system to import old conversations into the new format. This document covers the migration process, the format differences, and the safety guarantees that ensure users don't lose their conversation history.

## Why a Rewrite?

The v0.x codebase was TypeScript, which imposed several limitations:

- **Runtime overhead**: Node.js startup time and memory usage
- **Distribution complexity**: Required Node.js runtime or bundling
- **Single-binary goal**: Go produces a static binary with no runtime dependencies
- **Performance**: Go's concurrency model better serves the agent loop's parallel tool execution

The v1.0 rewrite preserved compatibility by migrating session data automatically on first launch.

## Format Differences

### v0.x Format: Event Log

v0.x stored sessions as **typed event logs** in `<name>.events.jsonl`:

```json
{"type": "user.message", "text": "Help me debug the auth module"}
{"type": "model.final", "content": "I'll look at the auth module...", "toolCalls": [...]}
{"type": "tool.result", "callId": "call_abc123", "output": "file contents..."}
```

Additional event types (UI, plan, checkpoint, etc.) carried presentation state but no message content.

### v1+ Format: Provider Messages

v1+ stores sessions as **provider messages** in `<name>.jsonl`:

```json
{"role": "user", "content": "Help me debug the auth module"}
{"role": "assistant", "content": "I'll look at the auth module...", "tool_calls": [...]}
{"role": "tool", "tool_call_id": "call_abc123", "name": "read_file", "content": "file contents..."}
```

The new format is simpler — only four roles (system, user, assistant, tool) instead of many event types. Presentation state is handled by the frontend, not persisted in the session.

## Migration Process

### Entry Point

```go
func MigrateLegacySessions(srcDir, destDir string) (int, error)
```

Called during boot (`internal/boot/boot.go`) to import any v0.x sessions that haven't been migrated yet. `srcDir` is typically the old sessions directory, and `destDir` is the new one.

### Step-by-Step

1. **Check markers**: If the destination directory already has a `.legacy-imported` or `.legacy-imported.v0-events-home` marker, skip entirely
2. **Scan source**: Read `srcDir` for `*.events.jsonl` files
3. **Check destination**: For each legacy file, check if a corresponding `*.jsonl` already exists in `destDir` — if so, skip (already imported or a v1+ session with the same name)
4. **Reconstruct**: Parse the event stream into `[]provider.Message`:
   - `user.message` → `RoleUser`
   - `model.final` → `RoleAssistant` (with tool calls and reasoning)
   - `tool.result` → `RoleTool`
5. **Save**: Write the reconstructed session in v1+ JSONL format
6. **Preserve timestamps**: Copy the original file's modification time to the new file so resume ordering is preserved
7. **Write markers**: Create marker files in `destDir` to prevent re-import

### Marker Files

| Marker | Purpose |
|--------|---------|
| `.legacy-imported` | Original v0.x marker — indicates the directory has been processed |
| `.legacy-imported.v0-events-home` | Additional marker for sessions in the home directory |

The migration checks both markers. If the original `.legacy-imported` marker exists but the home-directory marker doesn't, it writes the missing marker without re-importing.

## Event-to-Message Reconstruction

### The `legacyEvent` Struct

```go
type legacyEvent struct {
    Type             string           `json:"type"`
    Text             string           `json:"text"`              // user.message
    Content          string           `json:"content"`           // model.final
    ReasoningContent string           `json:"reasoningContent"`  // model.final
    ToolCalls        []legacyToolCall `json:"toolCalls"`         // model.final
    CallID           string           `json:"callId"`            // tool.result
    Output           string           `json:"output"`            // tool.result
}
```

### Mapping Rules

| v0.x Event | v1+ Message | Notes |
|------------|-------------|-------|
| `user.message` | `RoleUser` with `Content = Text` | Simple text mapping |
| `model.final` | `RoleAssistant` with `Content`, `ReasoningContent`, `ToolCalls` | Tool calls use `legacyToolCall.Function.Name/Arguments` |
| `tool.result` | `RoleTool` with `ToolCallID = CallID`, `Content = Output` | Tool name resolved from the assistant turn's tool call map |

### Tool Name Resolution

v0.x `tool.result` events carry only the call ID, not the tool name. The reconstruction builds a `toolName` map from each `model.final` event's tool calls:

```go
toolName := map[string]string{}
for _, tc := range e.ToolCalls {
    toolName[tc.ID] = tc.Function.Name
}
```

When a `tool.result` arrives, the tool name is looked up from this map. This ensures that v1+ sessions have complete tool metadata for display and search.

### Ignored Event Types

All other v0.x event types are dropped during migration:

- UI events (rendering state)
- Plan events (planning state)
- Checkpoint events (snapshot markers)
- Error events (transient errors)
- Status events (progress indicators)

These carry presentation state that the v1+ frontends handle independently, and none contains message content that needs to be preserved.

## Safety Guarantees

### Never Modify Source Files

The migration is **read-only** on the source directory. Legacy `.events.jsonl` files are never modified, renamed, or deleted. Users can always fall back to the original files if needed.

### Idempotent

Running the migration multiple times produces the same result:

- Marker files prevent re-importing
- Already-imported sessions (matching `*.jsonl` exists) are skipped
- No data is duplicated

### Incremental

A session that was imported and then deleted by the user does **not** reappear on the next launch. The marker file records that the import has run, not which specific sessions were imported.

### Tolerant of Malformed Data

The `reconstructSession` function handles errors gracefully:

- Malformed JSON lines are skipped
- A truncated last line (common after a crash) doesn't prevent the rest from loading
- Empty sessions return an empty message list (filtered out by `ListSessions`)

### Atomic Writes

Migrated sessions are written using the same atomic temp-file-then-rename strategy as normal session saves. A crash during migration cannot leave a partial file that would fail to load.

## Configuration Migration

Beyond sessions, the v0.x configuration format (JSON) was replaced by v1+'s TOML format. The boot sequence handles this through the `reasonix init` wizard, which generates a fresh TOML configuration. Old JSON configs are not automatically migrated — users run the init wizard to configure providers, API keys, and settings.

## Testing

The migration system has comprehensive test coverage:

- **Unit tests**: Verify event-to-message reconstruction for each event type
- **Integration tests**: End-to-end migration of sample session directories
- **Edge case tests**: Empty sessions, malformed events, missing tool names
- **Idempotency tests**: Running migration twice produces the same result
