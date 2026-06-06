# Builtin Tools Reference

Reasonix ships with 24+ builtin tools that provide the agent's interface to the host environment. Built-in tools self-register at compile time via `init()` functions; the agent sees only a `*tool.Registry` and never distinguishes them from MCP plugin tools. This document provides a comprehensive reference for every builtin tool, its arguments, behavior, and constraints.

## Tool Classification

Every tool implements the `tool.Tool` interface:

```go
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    ReadOnly() bool
    Execute(ctx context.Context, args json.RawMessage) (string, error)
}
```

The `ReadOnly()` flag is critical for three behaviors:

- **Plan mode**: Writer tools are blocked when plan mode is active
- **Parallel dispatch**: Contiguous read-only tools fan out across goroutines
- **Permission gating**: Writer tools typically require approval

Additionally, some tools implement `tool.Previewer`:

```go
type Previewer interface {
    Preview(args json.RawMessage) (diff.Change, bool)
}
```

Previewers compute a before/after diff without touching disk, enabling the approval card and checkpoint snapshot before the call runs.

---

## File Operations

### `read_file`

Reads a single file from disk. Returns the full content (truncated if it exceeds the output byte limit).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | Yes | File path to read |
| `offset` | integer | No | Line number to start reading from (1-based) |
| `limit` | integer | No | Maximum number of lines to read |

- **Read-only**: Yes
- **Previewer**: No
- **Output truncation**: Files exceeding `maxToolOutputBytes` (~32KB) are head+tailed with a truncation notice

### `write_file`

Writes content to a file, creating it if it doesn't exist or replacing it entirely.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | Yes | File path to write |
| `content` | string | Yes | Content to write |

- **Read-only**: No
- **Previewer**: Yes — shows the full new content as a diff against the existing file (or as a "new file" if creating)
- **Checkpoint**: Pre-edit content is captured by the `onPreEdit` hook

### `edit_file`

Applies a search-and-replace edit to an existing file.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | Yes | File path to edit |
| `old_string` | string | Yes | Exact text to find |
| `new_string` | string | Yes | Replacement text |
| `replace_all` | boolean | No | Replace all occurrences (default: first only) |

- **Read-only**: No
- **Previewer**: Yes — shows the unified diff of the edit
- **Constraints**: `old_string` must match exactly (including whitespace/indentation). If the match is not unique and `replace_all` is not set, the edit fails with a message indicating multiple matches.

### `multi_edit`

Applies multiple edits to a single file in one operation. Edits are applied sequentially.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | Yes | File path to edit |
| `edits` | array | Yes | List of edit operations |
| `edits[].old` | string | Yes | Text to find |
| `edits[].new` | string | Yes | Replacement text |

- **Read-only**: No
- **Previewer**: Yes — shows the cumulative diff of all edits
- **Constraints**: Each edit's `old` must match exactly. Edits are applied in order, so earlier edits may shift line positions for later ones.

### `delete_range`

Deletes a range of lines from a file.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | Yes | File path |
| `start_line` | integer | Yes | First line to delete (1-based, inclusive) |
| `end_line` | integer | Yes | Last line to delete (1-based, inclusive) |

- **Read-only**: No
- **Previewer**: Yes — shows the removed lines as a diff

### `delete_symbol`

Deletes a named symbol (function, class, method, etc.) from a file using tree-sitter-based symbol detection.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | Yes | File path |
| `symbol` | string | Yes | Name of the symbol to delete |
| `scope` | string | No | Parent symbol name for disambiguation |

- **Read-only**: No
- **Previewer**: Yes — shows the full symbol body being removed

### `notebook_edit`

Edits Jupyter notebook (`.ipynb`) cells. Handles the JSON structure of notebooks natively.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `notebook_path` | string | Yes | Path to the `.ipynb` file |
| `cell_number` | integer | Yes | Cell index to edit (0-based) |
| `new_source` | string | Yes | New cell source content |
| `cell_type` | string | No | "code" or "markdown" |

- **Read-only**: No
- **Previewer**: Yes — shows the cell content diff

---

## Search & Discovery

### `grep`

Searches file contents using regular expressions.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `pattern` | string | Yes | Regular expression pattern |
| `path` | string | No | Directory or file to search in |
| `include` | string | No | Glob pattern for file inclusion |
| `exclude` | string | No | Glob pattern for file exclusion |

- **Read-only**: Yes
- **Previewer**: No

### `glob`

Finds files matching a glob pattern.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `pattern` | string | Yes | Glob pattern (e.g., `**/*.go`) |
| `path` | string | No | Base directory |

- **Read-only**: Yes
- **Previewer**: No

### `ls`

Lists directory contents.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | Yes | Directory path |
| `all` | boolean | No | Show hidden files |

- **Read-only**: Yes
- **Previewer**: No

---

## Shell Execution

### `bash`

Executes a shell command. This is the most powerful and most dangerous builtin tool.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `command` | string | Yes | Shell command to execute |
| `timeout` | integer | No | Timeout in seconds |
| `run_in_background` | boolean | No | Run asynchronously, returning a job ID |
| `description` | string | No | Short label for the background job |

- **Read-only**: Depends on the command (classified as writer for safety)
- **Previewer**: No — bash targets are unknowable before execution
- **Checkpoint**: Not tracked (targets are unknowable)
- **Sandbox**: May be confined by the OS-level sandbox (Seatbelt on macOS)
- **Process management**: Supports cancellation via context; process trees are killed on timeout
- **Protected directories**: Certain system directories (e.g., `/System`, `/Library` on macOS) are blocked

### Background Bash

When `run_in_background` is true, the command runs as a job managed by `jobs.Manager`:

1. Returns immediately with a job ID (e.g., `bash-1`)
2. Output accumulates in a buffer, readable via `bash_output`
3. The job survives across turns (session-scoped context)
4. Completion notices are drained into the next turn

### `bash_output`

Reads incremental output from a background bash job.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | Job ID |

- **Read-only**: Yes
- **Behavior**: Returns only output since the last call (streaming). When the job is terminal with no buffered output, the final result is surfaced once.

### `kill_shell`

Kills a running background job.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `id` | string | Yes | Job ID |

- **Read-only**: No (classified as writer)
- **Behavior**: Cancels the job's context and flips its status to "killed" synchronously

### `wait`

Blocks until specified background jobs (or all running jobs) reach a terminal state.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `ids` | array | No | Job IDs to wait for (empty = all running) |
| `timeout` | integer | No | Maximum wait time in seconds |

- **Read-only**: Yes

---

## Workspace

### `workspace`

Returns the current workspace root path. No parameters.

- **Read-only**: Yes

---

## Task & Todo Management

### `todo_write`

Writes a structured todo list that the agent tracks across the turn.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `todos` | array | Yes | List of todo items |
| `todos[].content` | string | Yes | Task description |
| `todos[].status` | string | Yes | "pending", "in_progress", or "completed" |
| `todos[].activeForm` | string | No | Present-tense form ("Fixing the bug") |
| `todos[].level` | integer | No | Nesting level |

- **Read-only**: Yes (classifies as read-only because it doesn't modify files)
- **Evidence**: The evidence ledger records todo_write receipts, enabling `complete_step` validation

### `complete_step`

Marks a todo step as complete, with host-observable validation.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `step` | string | Yes | Step index or description to mark complete |

- **Read-only**: Yes
- **Evidence**: The evidence ledger validates that the step matches a known todo item and that required verification commands have been run

---

## Interactive

### `ask`

Asks the user one or more multiple-choice questions when the model hits a genuine decision fork.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `questions` | array | Yes | 1-4 questions |
| `questions[].header` | string | Yes | Short tab label |
| `questions[].question` | string | Yes | Full question text |
| `questions[].options` | array | Yes | 2-4 choices |
| `questions[].options[].label` | string | Yes | Choice text |
| `questions[].options[].description` | string | No | One-line explanation |
| `questions[].multiSelect` | boolean | No | Allow multiple selections |

- **Read-only**: Yes
- **Headless behavior**: With no interactive user (headless runs), returns "proceed with your best judgment"

---

## Sub-Agent

### `task`

Spawns a sub-agent for a focused sub-task. See [Sub-Agent & Task Tool](./sub-agent-task-tool.md) for full documentation.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `prompt` | string | Yes | What the sub-agent should accomplish |
| `description` | string | No | Short label (3-7 words) |
| `tools` | array | No | Tool whitelist |
| `max_steps` | integer | No | Step cap |
| `run_in_background` | boolean | No | Run asynchronously |

- **Read-only**: No

---

## Web

### `web_fetch`

Fetches content from a URL. Uses a separate HTTP client with SSRF protection (different security boundary from provider/update traffic).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `url` | string | Yes | URL to fetch |
| `method` | string | No | HTTP method (default: GET) |

- **Read-only**: Yes
- **Security**: Does not use the shared `netclient` — has its own SSRF-safe dialer

---

## Confinement

### `confine` (internal)

Restricts a tool call's scope — used internally by the workspace confinement system. Not directly invoked by the model.

---

## Protected Directories

On macOS, certain directories are protected from write operations:

- `/System`
- `/Library`
- `/usr`
- `/bin`, `/sbin`
- `/var`

Other platforms use a minimal protection list. The `protected_dirs_darwin.go` and `protected_dirs_other.go` files provide platform-specific lists.

## Gitignore Integration

The `gitignore.go` helper respects `.gitignore` patterns when listing or searching files. This prevents the agent from reading or modifying files the user has explicitly excluded from version control.

## Output Truncation

All tool results are capped at `maxToolOutputBytes` (~32KB ≈ 8K tokens). This prevents a single accidental "read this 5MB log" from blowing the context window before the next compaction runs. Truncated results include a notice like "output truncated; showing first 16KB and last 16KB".
