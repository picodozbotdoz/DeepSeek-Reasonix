# Tool System

The tool system defines how the agent interacts with the host environment — reading files, writing code, running commands, searching codebases, and more. Tools are Go interfaces registered in a per-run registry. Built-in tools self-register at compile time; plugin tools are adapted at runtime from MCP servers. The agent sees only a `*tool.Registry` and never distinguishes between the two.

## Core Interface

```go
// internal/tool/tool.go

type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage       // JSON Schema for parameters
    Execute(ctx context.Context, args json.RawMessage) (string, error)
    ReadOnly() bool
}
```

- `Name()` — the model-visible identifier (e.g. "read_file", "mcp__stripe__create_charge").
- `Description()` — one-liner shown in the tool schema to the model.
- `Schema()` — JSON Schema for the tool's parameters. Canonicalized once at registration.
- `Execute()` — parses raw JSON args and returns result text. Errors are fed back to the model for self-correction.
- `ReadOnly()` — reports whether the tool has no observable side effects. The agent parallelizes a batch of tool calls only when every call is `ReadOnly()`. Writers run serially to preserve ordering.

## Previewer Interface (Optional)

```go
type Previewer interface {
    Preview(args json.RawMessage) (diff.Change, error)
}
```

Writer tools may implement `Previewer` to compute the file change a call *would* make — without touching disk. This is used by:
- The **approval card** — frontends show a diff preview before the user approves.
- The **checkpoint store** — the agent snapshots pre-edit content before executing the tool.
- The **tool dispatch event** — `ToolDispatch` events carry the file diff so frontends can render immediately.

`bash` and MCP tools do not implement `Previewer` — their targets are unknowable.

## Registry

```go
type Registry struct {
    mu    sync.RWMutex
    tools map[string]Tool
    order []string
    canon map[string]json.RawMessage  // canonicalized schemas
}
```

The registry is a per-run set of tools: enabled built-ins plus plugin tools. Key methods:

- `Add(t Tool)` — inserts or replaces a tool, canonicalizing its schema once.
- `Get(name string) (Tool, bool)` — looks up a tool by name.
- `Schemas()` — exports tool definitions in stable name order for the provider.
- `RemovePrefix(prefix string) int` — unregisters every tool whose name starts with prefix (used when an MCP server disconnects).

### Schema Stability

Schemas are canonicalized once at `Add()` time and cached. `Schemas()` is called every turn to build the provider request, and reusing the cached result prevents JSON re-serialization from changing whitespace or property order — critical for DeepSeek's prefix cache.

## Built-in Tools

Built-in tools live in `internal/tool/builtin/` and self-register via `init()`:

```go
func init() {
    tool.RegisterBuiltin(bashTool{})
}
```

`main.go` blank-imports `tool/builtin` to trigger all registrations.

### File Operations

| Tool | ReadOnly | Description |
|------|----------|-------------|
| `read_file` | Yes | Read file contents with encoding detection (UTF-8, UTF-16, GB18030). Supports line ranges, streaming for large files. |
| `write_file` | No | Create or overwrite a file. Always writes UTF-8. Confined to sandbox roots. Implements `Previewer`. |
| `edit_file` | No | Apply a search-and-replace edit to a file. Preserves original encoding. Implements `Previewer`. |
| `multi_edit` | No | Apply multiple edits to one or more files in a single call. Implements `Previewer`. |
| `notebookedit` | No | Edit Jupyter notebook cells. |

### Search & Navigation

| Tool | ReadOnly | Description |
|------|----------|-------------|
| `ls` | Yes | List directory contents. |
| `glob` | Yes | Find files by glob pattern, respecting `.gitignore`. |
| `grep` | Yes | Search file contents with regex. Decodes non-UTF-8 before matching. |

### Execution

| Tool | ReadOnly | Description |
|------|----------|-------------|
| `bash` | No | Run a shell command. Supports timeout, background execution (`run_in_background`), output polling (`bash_output`), kill (`kill_shell`). Sandboxed on macOS via Seatbelt. |
| `web_fetch` | No* | Fetch a URL and return its content. SSRF-protected. |

### Agent Coordination

| Tool | ReadOnly | Description |
|------|----------|-------------|
| `task` | No | Spawn a sub-agent (explore, research, review, security_review, or custom). Can run foreground or background. |
| `ask` | Yes | Put a structured multiple-choice question to the user. Uses the `Asker` interface. |
| `todo_write` | Yes* | Create/update a todo list. Not parallelized (reads evidence ledger). |
| `complete_step` | Yes* | Sign off a plan step with evidence. Not parallelized. |

### Workspace

| Tool | ReadOnly | Description |
|------|----------|-------------|
| `workspace` | Yes | Report the current workspace root and layout. |

### Symbol Operations

| Tool | ReadOnly | Description |
|------|----------|-------------|
| `delete_range` | No | Delete a range of lines from a file. |
| `delete_symbol` | No | Delete a named symbol (function, class, etc.) using tree-sitter. |

### Background Jobs

The `bgjobs` tool set manages long-running shell commands:
- `bash` with `run_in_background: true` → spawns into `jobs.Manager`
- `bash_output` → reads recent output from a background job
- `kill_shell` → terminates a background job
- `wait` → blocks until a background job completes

## Path Confinement

File-writing tools (`write_file`, `edit_file`, `multi_edit`) enforce sandbox confinement (`internal/tool/builtin/confine.go`):
- All write targets must resolve to an absolute path within `workspace_root` (default: cwd) plus any `allow_write` directories.
- Symlinks and `..` are resolved before checking, so a symlink cannot tunnel out of the workspace.
- Reads are unrestricted.

## Encoding Support

The `encoding_helpers.go` file provides encoding detection and conversion:
- **Read** — auto-detects UTF-8, UTF-8 BOM, UTF-16 LE/BE, and GB18030 (superset of GBK). Decodes to UTF-8 for the model.
- **Edit** — preserves the original file encoding. If you edit a GB18030 file, it stays GB18030 on disk.
- **Write** — always writes UTF-8 (the model's output encoding).
- **Grep** — decodes before matching, so regex patterns work on non-UTF-8 files.

## Gitignore Integration

`glob` and `grep` respect `.gitignore` patterns via the `go-gitignore` library. The `gitignore.go` file in builtin handles loading ignore files from the workspace root and walking the directory tree accordingly.

## MCP Tool Namespace

Plugin tools are namespaced `mcp__<server>__<tool>` (matching Claude Code's convention). The `tool.SplitMCPName` function parses these back into server + tool parts. When an MCP server disconnects, `Registry.RemovePrefix("mcp__server__")` drops all its tools.

## Tool Output Capping

A single tool result is capped at ~32KB (`maxToolOutputBytes`) — roughly 8K tokens. This prevents one accidental "read this 5MB log" from blowing the context window before the next compaction. Capped output is head+tailed with a truncation notice.

## Adding a New Built-in Tool

1. Create `internal/tool/builtin/mytool.go`.
2. Implement the `tool.Tool` interface: `Name()`, `Description()`, `Schema()`, `ReadOnly()`, `Execute()`.
3. Optionally implement `tool.Previewer` for writer tools.
4. Register via `func init() { tool.RegisterBuiltin(myTool{}) }`.
5. Add tests in `mytool_test.go`.
6. The tool is automatically available — `main` blank-imports `builtin`.

## See Also

- [Architecture Overview](architecture-overview.md)
- [MCP Plugin System](mcp-plugin-system.md)
- [Permission & Sandbox](permission-sandbox.md)
- [Agent Loop & Coordinator](agent-loop-coordinator.md)
