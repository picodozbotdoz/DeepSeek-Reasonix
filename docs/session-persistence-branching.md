# Session Persistence & Branching

Reasonix sessions are long-lived conversations that survive restarts, model switches, and configuration changes. The persistence layer stores the full message history in a compact, append-friendly format, while the branching system turns flat session files into a navigable conversation tree. Together they enable `--resume`, `/resume`, model switching, conversation forking, and the rewind system.

## Session Format

Sessions are stored as **JSONL** — one `provider.Message` per line — under the session directory (configured via `config.SessionDir()`, defaulting to `~/.reasonix/sessions/`). Each file is named with a timestamp and the model name for easy identification:

```
20260102-150405.000000000-deepseek-chat.jsonl
```

### Why JSONL?

The JSONL format was chosen over alternatives for several practical reasons:

- **Stream-friendly**: Messages can be appended or decoded one at a time without parsing the entire file. The `json.Decoder` reads a stream of values, so a single multi-MiB bash output message (which can exceed any line-buffer cap) loads correctly — a line-scanning `Scanner` would fail on such messages.
- **Crash-safe writes**: Sessions are rewritten atomically using a temp-file-then-rename strategy. A crash mid-write cannot leave a partial JSONL that won't reload, because the old file remains intact until the new one is fully written and renamed into place.
- **Compaction-friendly**: Compaction mutates the middle of `session.Messages`, so the file is rewritten in full on every save. For chat sessions (typically kilobytes), this is negligible. Append-only would require reconciling with compaction, adding complexity for no real gain.

### Session Structure

Each line in the JSONL file is a `provider.Message` with one of four roles:

| Role | Description |
|------|-------------|
| `system` | System prompt(s) — always at the start, never compacted |
| `user` | User input, compaction summaries, and readiness retry messages |
| `assistant` | Model output with optional `tool_calls`, `reasoning_content`, and `reasoning_signature` |
| `tool` | Tool results keyed by `tool_call_id` and `name` |

### Save & Load

```go
// Save writes the session atomically
func (s *Session) Save(path string) error

// LoadSession reads a JSONL file into a fresh Session
func LoadSession(path string) (*Session, error)
```

The `Save` method takes a `Snapshot()` of the messages (a lock-protected copy) so a concurrent turn can still be appending while the save proceeds. The write goes to a sibling `.tmp` file first, then `fileutil.ReplaceFile` atomically renames it into place.

`LoadSession` uses `json.NewDecoder` (not a line scanner) to handle arbitrarily large messages. Missing files surface as `os.IsNotExist` so callers can fall through to a new session.

### Session Thread Safety

The `Session` struct uses a `sync.RWMutex` to guard `Messages`:

- **Direct reads on the run-loop goroutine** stay lock-free (serial with its own writes)
- **Cross-goroutine access** goes through `Snapshot()`, which returns a full copy under `RLock`
- **Pointer swaps** (e.g., `SetSession` for `--resume`) are serialized by `sessMu`

The `rewriteVersion` counter is bumped each time the message log is rewritten (compaction or folding). This is used by the cache diagnostics system to detect when prefix stability has been broken.

## Session Listing & Preview

```go
type SessionInfo struct {
    Path           string
    CreatedAt      time.Time
    LastActivityAt time.Time
    Preview        string    // first user message, truncated to 80 chars
    Turns          int       // count of user-role messages
    Scope          string    // "project" or "global"
    WorkspaceRoot  string
    TopicID        string
    TopicTitle     string
}

func ListSessions(dir string) ([]SessionInfo, error)
```

Sessions are listed most-recently-active first. The preview extracts the first user message (truncated to 80 characters) and the turn count, so the resume picker shows "5 turns · 'help me debug the…'". Sessions with zero turns (never had user interaction) are filtered out.

## Session Path Continuation

When a controller is rebuilt (model switch, config change), the session should keep auto-saving to its existing file rather than creating a duplicate:

```go
func ContinueSessionPath(prevPath, dir, model string) string
```

If `prevPath` is non-empty, it is returned as-is — the continued session stays a single file. Otherwise, a fresh path is generated with `NewSessionPath`.

## Branching System

Sessions are flat files, but branching metadata turns them into a **conversation tree**. This enables forking conversations, resuming from earlier points, and tracking workspace scope.

### Branch Metadata

Each session file can have a companion `.meta` sidecar file:

```
20260102-150405-deepseek-chat.jsonl      ← conversation
20260102-150405-deepseek-chat.jsonl.meta  ← branch metadata
```

```go
type BranchMeta struct {
    ID               string    `json:"id"`
    Name             string    `json:"name,omitempty"`
    ParentID         string    `json:"parent_id,omitempty"`
    ForkTurn         int       `json:"fork_turn,omitempty"`
    ForkMessageIndex int       `json:"fork_message_index,omitempty"`
    CreatedAt        time.Time `json:"created_at"`
    UpdatedAt        time.Time `json:"updated_at"`
    Scope            string    `json:"scope,omitempty"`          // "project" or "global"
    WorkspaceRoot    string    `json:"workspace_root,omitempty"`
    TopicID          string    `json:"topic_id,omitempty"`
    TopicTitle       string    `json:"topic_title,omitempty"`
}
```

### Branch Operations

- **`EnsureBranchMeta`**: Creates metadata for a session that doesn't have it yet, using the file's modification time for timestamps.
- **`TouchBranchMeta`**: Updates `UpdatedAt` without changing other fields — called when a session is active.
- **`SaveBranchMeta` / `SaveBranchMetaPreserveUpdated`**: Writes metadata atomically. The "preserve" variant doesn't bump `UpdatedAt`, useful for batch operations.
- **`ListBranches`**: Returns all branches in a directory with their metadata, preview text, and turn count, sorted by creation time.

### Fork Topology

The `ParentID` and `ForkTurn`/`ForkMessageIndex` fields trace the conversation tree. When a user rewinds and forks from an earlier point:

1. The original session's `BranchMeta` records the fork point
2. The new session gets a `ParentID` pointing to the original
3. `ForkTurn` and `ForkMessageIndex` record exactly where the fork happened
4. `Scope` and `WorkspaceRoot` propagate from the parent

This allows frontends to render a conversation tree and navigate between branches.

### Scope & Workspace

Branches are scoped:

- **`global`**: Available across all workspaces
- **`project`**: Tied to a specific `WorkspaceRoot`

The `DefaultScope()` method normalizes scope — only "project" is special; everything else is "global". This scope is used by the resume picker and session listing to filter sessions relevant to the current workspace.

### Topic System

`TopicID` and `TopicTitle` allow grouping sessions by topic. This is used by the desktop app's tab system and the session panel to organize conversations beyond just chronological order.

## Version Migration (v0.x → v1+)

Reasonix v1.0 was a ground-up rewrite in Go. The v0.x releases (TypeScript) stored sessions in a different format: `<name>.events.jsonl` with typed event records rather than provider messages.

### Migration Process

```go
func MigrateLegacySessions(srcDir, destDir string) (int, error)
```

The migration runs **once**, guarded by marker files:

1. **`.legacy-imported`** — the original v0.x marker
2. **`.legacy-imported.v0-events-home`** — marker for sessions in the home directory

The process:

1. Check if the marker already exists in `destDir` — if so, skip
2. Scan `srcDir` for `*.events.jsonl` files
3. For each file not already present as a `*.jsonl` in `destDir`:
   - Reconstruct the session from the event stream into `[]provider.Message`
   - Save the reconstructed session in v1 format
   - Preserve the original file's modification time for resume ordering
4. Write the marker files to prevent re-import on next launch

### Event-to-Message Reconstruction

The v0.x event stream used different types:

| v0.x Event Type | v1+ Message Role |
|-----------------|------------------|
| `user.message` | `RoleUser` with `Text` → `Content` |
| `model.final` | `RoleAssistant` with `Content`, `ReasoningContent`, and `ToolCalls` |
| `tool.result` | `RoleTool` with `CallID` → `ToolCallID`, `Output` → `Content` |

Tool results in v0.x carried only the call ID (not the tool name), so the reconstruction pass builds a `toolName` map from the assistant turn's tool calls to resolve names for tool results.

### Safety Properties

- **Never modifies legacy files** — migration is read-only on the source
- **Idempotent** — already-imported sessions are skipped by checking for the destination file
- **Incremental** — a session deleted after import doesn't reappear on the next launch
- **Tolerant of malformed tails** — `reconstructSession` returns what parsed cleanly rather than failing on a truncated last line

## Key Implementation Details

### Atomic File Operations

Both session saves and branch metadata writes use the same pattern:

1. Write to a temporary file in the same directory
2. Close the temp file
3. Rename (atomic on most filesystems) via `fileutil.ReplaceFile`

This ensures that a crash at any point leaves either the old or the new file intact — never a partial write.

### Preview Generation

The preview for session listings is generated lazily by scanning the JSONL file for the first `RoleUser` message:

```go
func previewSession(path string) (string, int)
```

This is deliberately lightweight — it stops after finding the first user message and counting turns, rather than loading the full session into memory. For large session directories, this keeps listing fast.

### Encoding Resilience

`LoadSession` uses `json.NewDecoder` rather than scanning lines, because a single bash output message can be multi-megabyte. The decoder has no line-buffer limit, so sessions that saved fine never fail to reload due to message size.
