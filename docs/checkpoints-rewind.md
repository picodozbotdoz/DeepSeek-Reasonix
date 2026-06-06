# Checkpoint & Rewind

Checkpoints provide a snapshot-based edit safety net. Before a writer tool changes a file, the agent records the file's pre-edit content, keyed to the current user turn. A frontend can then rewind the workspace (and the conversation) to an earlier point — without touching the user's git history.

## Design Goals

- **Zero git pollution** — never commits, stages, or touches `.git/`. Works in a non-git directory.
- **Tracks only edit-tool changes** — `write_file` / `edit_file` / `multi_edit`. `bash` side effects are not tracked (no way to know what a shell command touched).
- **Aligned with Claude Code** — same rewind model (Esc-Esc / `/rewind`), same scope options.

## Data Model

```go
type FileSnap struct {
    Path     string        `json:"path"`
    Content  *string       `json:"content"`   // nil → file did not exist (restore deletes)
    Encoding *fileenc.Kind `json:"encoding,omitempty"`
}

type Checkpoint struct {
    Turn     int        `json:"turn"`
    Time     time.Time  `json:"time"`
    Prompt   string     `json:"prompt"`
    MsgIndex int        `json:"msgIndex"`    // conversation-rewind boundary
    Files    []FileSnap `json:"files"`
}

type Meta struct {
    Turn   int
    Time   time.Time
    Prompt string
    Paths  []string
}
```

- `Content == nil` means the file did not exist at the turn's start — restoring it deletes the file.
- `MsgIndex` is `len(Session.Messages)` at the turn's start — the conversation-rewind boundary.

## The Store

```go
type Store struct {
    dir  string           // <session>.ckpt/, or "" for in-memory only
    root string           // workspace root, for restore path-escape guards
    mu   sync.Mutex
    done []*Checkpoint    // finalized turns
    cur  *Checkpoint      // the active turn's checkpoint
    seen map[string]bool  // paths already snapshotted this turn (dedup)
}
```

### Persistence

- One JSON file per turn under `<session>.ckpt/` — cheap delete, corruption-isolated.
- Separate from the message JSONL so the session format is unchanged.
- Checkpoints persist across sessions — resuming a session re-loads them.

### Capture Seam

The agent snapshots file content via a pre-edit hook:

1. In `agent.(*Agent).executeOne`, before running a non-`ReadOnly()` tool that implements `tool.Previewer`:
   - Call `Preview(args)` → `diff.Change{Path, Kind, OldText}`
   - Invoke `onPreEdit(change)` — the controller wires this to `checkpoint.Store.Snapshot()`
2. `Snapshot()` records the file's pre-edit content (only the first touch per path per turn).
3. `Kind == create` (file didn't exist) → `Content = nil` so a restore *deletes* it.

`bash` has no `Previewer`, so it is naturally excluded — matching the "edit-tools only" contract.

## Controller API

```go
type RewindScope int
const (
    RewindCode         RewindScope = iota  // files only
    RewindConversation                     // message log only
    RewindBoth                             // both
)

func (c *Controller) Checkpoints() []Meta
func (c *Controller) Rewind(turn int, scope RewindScope) error
func (c *Controller) Fork(turn int) (string, error)
func (c *Controller) ForkNamed(turn int, name string) (string, error)
```

### Rewind

- **Code** — for every checkpoint from `turn` to the latest, take the earliest `FileSnap` per path and restore each file to that content (or delete if `nil`). Path-escape re-checked against the live workspace root.
- **Conversation** — truncate `Session.Messages` to just before `turn`'s user message, re-save.
- **Both** — code + conversation.

Refused while a turn is running. Conversation rewind requires the live `MsgIndex` boundary — unavailable for turns inherited from a resumed session (code rewind still works).

### Fork / Branch

`Fork()` copies the conversation at the start of `turn` into a new session file, preserving the current one as the branch point, and switches to the branch. Code is untouched (it's a conversation operation).

## Encoding Preservation

When restoring files, the store preserves the original encoding:
- `FileSnap.Encoding` records the detected encoding at snapshot time.
- `RestoreCode()` writes back using the same encoding.
- If encoding is unknown, it falls back to UTF-8.

## Path Safety

`safePath()` resolves paths against the workspace root and rejects anything escaping it:

```go
func safePath(root, p string) (string, error) {
    abs := filepath.Clean(filepath.Join(root, p))
    if !strings.HasPrefix(abs, filepath.Clean(root)+string(os.PathSeparator)) {
        return "", fmt.Errorf("checkpoint path %q escapes workspace %q", p, root)
    }
    return abs, nil
}
```

Restore must never write outside the workspace, even if a snapshot path is hostile or the project moved since it was taken.

## Turn Boundaries

`cpBound[turn]` records `len(Session.Messages)` at each turn's start. These boundaries are persisted in each checkpoint and rebuilt from the store on resume, so a reopened session can still rewind conversation / fork. They are dropped after a summarize restructures the log (those operations report "unavailable" rather than mis-truncating).

## Phasing

1. **Phase 1** (implemented): snapshot store + capture seam + `Controller.Rewind` (code/conversation/both) + CLI picker (Esc-Esc + `/rewind`).
2. **Phase 2** (implemented): desktop hover-rewind UI; "fork from here"; "summarize from/up to here".

## See Also

- [Architecture Overview](architecture-overview.md)
- [Controller & Frontends](controller-frontends.md)
- [Tool System](tool-system.md)
