# Permission & Sandbox Security

Reasonix runs shell commands and edits files autonomously. Two layers protect the host: **permissions** (policy — which calls to allow, prompt, or deny) and **sandbox** (enforcement — what the OS permits even when a call is allowed). Together they form a defense-in-depth model where the permission layer is the user-facing decision point and the sandbox is the hard boundary the model cannot escape.

## Permission Layer

The permission system (`internal/permission/`) decides, per tool call, whether to allow it, deny it, or ask the user first. It is pure logic (no I/O), making it trivially testable and keeping the agent independent of how "ask" is resolved.

### Policy

```go
type Policy struct {
    Mode  Decision  // fallback for writer tools when no rule matches
    Allow []Rule
    Ask   []Rule
    Deny  []Rule
}

type Decision int
const (Allow Decision = iota; Ask; Deny)
```

**Precedence**: `deny > ask > allow > fallback`. Fallback is `Allow` for read-only tools and `Mode` (default `Ask`) for writers. This means a broad `allow = ["bash"]` can still be carved by `deny = ["bash(rm -rf*)"]`, and `ask` overrides a broad `allow` to force a prompt on a risky subset.

### Rules

```go
type Rule struct {
    Tool    string
    Subject string
    Literal bool
}
```

A rule is parsed from config strings:
- `"bash"` — matches any call to the bash tool.
- `"bash(rm -rf*)"` — matches bash calls whose subject (the command) matches the glob.
- `"bash=go build ./..."` — matches bash calls whose subject is exactly this string (no globbing, for remembered approvals).

The **subject** is extracted generically from the call's JSON args by known keys:
- `command` (bash)
- `path` / `file_path` (file tools)
- `pattern` (grep/glob)

This means tools need not implement a permission-specific method — the permission layer just reads their args.

### Glob Matching

`matchGlob` implements `*` (any run of characters, including `/`) and `?` (exactly one character). Unlike `path.Match`, `*` is not stopped by `/`, which is what command-line and path prefixes intuitively expect: `"rm -rf*"` matches `rm -rf / --no-preserve-root`.

### Gate (Interactive + Non-Interactive)

```go
type Gate struct {
    Policy   Policy
    Approver Approver  // nil for non-interactive runs
    OnRemember func(rule string)
}
```

The `Gate` wraps a `Policy` with an optional interactive `Approver`:
- **Interactive** (chat TUI, desktop) — `Ask` decisions prompt the user: allow once / always allow / deny. "Always allow" persists a new `Rule` via `OnRemember`.
- **Non-interactive** (`reasonix run`, sub-agents) — `Ask` resolves to `Allow`, preserving autonomous behavior.
- **`Deny`** is a hard block in every mode — the tool never executes and the model receives a "blocked" result.

### Bash Read-Only Detection

Even though `bash` is technically a writer tool, many commands are read-only (e.g. `ls`, `cat`, `git status`). The `bash_readonly.go` module detects common read-only command prefixes and reclassifies those calls as read-only for permission purposes.

### Relationship to Plan Mode

Plan mode is an orthogonal, coarser gate that refuses *all* writers regardless of policy. It is checked first. The permission layer is the fine-grained, always-on gate underneath it.

### Auto-Approve After Plan Approval

When the user approves a plan in plan mode, the controller sets `autoApprove = true` for the execution turn. This means writer tools are auto-approved (no re-prompting) for the approved work. `Deny` rules still bite — they are resolved before the approver.

### Bypass (YOLO) Mode

The `bypass` flag (`--dangerously-skip-permissions` or runtime toggle) auto-allows every approval prompt for the rest of the session. It is never persisted. `Deny` rules are unaffected — they are resolved before the approver.

## Sandbox Layer

The sandbox (`internal/sandbox/`) is the *enforcement* layer beneath the permission rules (which are *policy*). A permitted command still cannot escape the box.

### File-Writer Confinement

The built-in file-writing tools (`write_file`, `edit_file`, `multi_edit`) enforce confinement:
- All write targets must resolve to an absolute path within `workspace_root` (default: cwd) plus any `allow_write` directories.
- Symlinks and `..` are resolved before checking, so a symlinked directory or `..` cannot tunnel out of the workspace.
- Reads are unrestricted.
- A write that falls outside every root is refused, and the error is fed back to the model.

### macOS Seatbelt

On macOS, bash commands can be jailed via `sandbox-exec` (Seatbelt):
- **Write confinement** — commands may write only to `workspace_root` + `allow_write` + temp dirs + common toolchain caches (`~/.cache/go-build`, `~/.npm`, etc.).
- **Network control** — when `[sandbox] network = true`, network egress is allowed; otherwise blocked.
- **Mode** — `enforce` activates confinement; any other value runs commands unwrapped.

```go
type Spec struct {
    Mode       string     // "enforce" to wrap, anything else to run unwrapped
    WriteRoots []string   // directories the command may write to
    Network    bool       // whether network egress is allowed
}
```

### Other Platforms

On Linux and Windows, bash commands currently run unconfined (sandbox enforcement not yet implemented). The file-writer built-ins still enforce path confinement on all platforms.

### Sandbox Spec Resolution

```go
func Command(spec Spec, shell string, command string) ([]string, bool)
```

`Command` wraps a shell command in the platform's sandbox tooling. On macOS with `spec.enforce()`, it prepends `sandbox-exec -p <profile>` with the Seatbelt profile. On other platforms, it returns the raw command.

## Configuration

```toml
[permissions]
mode  = "ask"                                # writer fallback: ask|allow|deny
deny  = ["bash(rm -rf*)", "bash(git push*)"] # hard-blocked in every mode
allow = ["bash(go test*)"]                   # never prompted

[sandbox]
# workspace_root = ""          # file-writers confined here; empty = cwd
# allow_write    = ["/tmp"]    # extra dirs write_file/edit_file/multi_edit may touch
```

### Default Behavior

With the default configuration (`mode = "ask"`, no rules):
- `reasonix run` — writers resolve `Ask → Allow` (no TTY, no approver), behaving autonomously.
- `reasonix chat` — prompts before each writer/bash call.
- `deny` rules harden both modes.

## Security Model Summary

```
┌──────────────────────────────────────────────────────────┐
│                     Tool Call Flow                        │
│                                                          │
│  Model produces tool call                                │
│        │                                                 │
│        ▼                                                 │
│  Plan Mode Gate (if active)                              │
│  Writer? → Block (return "blocked" result)               │
│        │ (passed)                                        │
│        ▼                                                 │
│  Permission Gate                                         │
│  Deny rule? → Block                                      │
│  Ask rule? → Interactive prompt / auto-allow             │
│  Allow rule? → Pass                                      │
│  No match? → Fallback (Allow for readers, Mode for      │
│              writers)                                    │
│        │ (allowed)                                       │
│        ▼                                                 │
│  Sandbox Enforcement                                     │
│  File writer outside workspace? → Refuse                 │
│  Bash command (macOS)? → Seatbelt profile                │
│        │ (within bounds)                                 │
│        ▼                                                 │
│  Tool Executes                                           │
└──────────────────────────────────────────────────────────┘
```

## See Also

- [Architecture Overview](architecture-overview.md)
- [Tool System](tool-system.md)
- [Controller & Frontends](controller-frontends.md)
