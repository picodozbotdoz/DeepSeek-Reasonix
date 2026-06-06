# Additional Subsystems

This document covers subsystems not detailed in the other architecture documents: the diff engine, evidence ledger, instruction system, frontmatter parser, file utilities, billing, i18n, boot sequence, jobs manager, and diagnostic tools.

## Diff Engine (`internal/diff/`)

The diff engine computes line-level differences for file edits, supporting the tool preview and checkpoint systems:

- **`Change` struct** — carries `Path`, `Kind` (create/modify/delete), `OldText`, `NewText`, and `Binary` flag.
- Used by `tool.Previewer` to show diffs before execution.
- Used by `checkpoint.Store.Snapshot()` to record pre-edit content.
- Supports large files with chunked diff computation.

## Evidence Ledger (`internal/evidence/`)

The evidence ledger tracks tool receipts per user turn, enabling the `complete_step` readiness check:

- **Per-turn tracking** — records every tool call's outcome (success, failure, blocked).
- **Writer tracking** — identifies the latest successful writer tool call.
- **Todo tracking** — checks the latest `todo_write` for incomplete items.
- **Command verification** — checks if a specific bash command ran after a writer.
- **`complete_step` validation** — verifies cited evidence before signing off a plan step.

When the agent produces a final answer (no tool calls), `finalReadinessFailure()` checks:
1. Are there incomplete todos?
2. Was a writer tool used but verification commands not run afterward?
3. Was `todo_write` used but `complete_step` not called?

If readiness fails, the agent is told to address the missing receipts before giving a final answer.

## Instruction System (`internal/instruction/`)

The instruction system parses project-specific instructions from `REASONIX.md` files:

- **`VerifyCheck`** — a structured check extracted during boot: a command that must run after a write, with a source path and line number.
- Example: a project memory might say "after editing Go files, run `go vet ./...`".
- The controller feeds these checks to the agent, which enforces them via the evidence ledger.

## Frontmatter Parser (`internal/frontmatter/`)

A lightweight frontmatter parser for skill and command Markdown files:

- Splits `---`-fenced blocks from the body.
- Parses simple `key: value` lines (no YAML dependency — Reasonix stays lean).
- Used by `skill.Store.parse()` and `command.Load()`.

## File Utilities

### Atomic Write (`internal/fileutil/atomicwrite.go`)

Writes files atomically using a temp file + rename pattern, preventing partial writes on crash.

### Encoding Detection (`internal/fileutil/encoding/`)

Detects file encoding (UTF-8, UTF-8 BOM, UTF-16 LE/BE, GB18030/GBK) and provides encode/decode functions:

- `Detect(data)` — returns the detected encoding kind.
- `Encode(text, kind)` — encodes UTF-8 text to the specified encoding.
- `Decode(data, kind)` — decodes data from the specified encoding to UTF-8.

This is what enables Reasonix to correctly read, edit, and write non-UTF-8 files (especially CJK Windows charsets like GBK/GB18030).

## Billing (`internal/billing/`)

Queries the active provider's wallet-balance endpoint (when available):

- `Balance()` — returns the current account balance.
- Uses the provider's optional `balance_url` and `bearer` key.
- Displayed in the status line and `/status` endpoint.

## Internationalization (`internal/i18n/`)

Reasonix supports English and Chinese UI strings:

- `Messages` struct — all translatable strings.
- `messages_en.go` — English strings.
- `messages_zh.go` — Chinese strings.
- `TestCatalogsComplete` — ensures no string is missing from any locale.
- Language auto-detects from `$LANG` / `$REASONIX_LANG`, or can be set via `[language]` in config.

## Boot Sequence (`internal/boot/`)

The boot package wires everything together for a session:

1. Load config (`config.Load()`) — TOML resolution hierarchy.
2. Resolve the model provider (`config.ResolveModel()`).
3. Discover memory (`memory.Load()`).
4. Discover skills (`skill.New().List()`).
5. Start MCP plugins (`plugin.StartAvailable()` or `StartAll()`).
6. Build the tool registry (`tool.NewRegistry()` + built-ins + plugin tools).
7. Build the agent (or coordinator if `planner_model` is set).
8. Build the controller with all assembled pieces.
9. Return the controller to the frontend.

`Build()` accepts `Options` including model name, sink, and stderr writer.

## Jobs Manager (`internal/jobs/`)

The jobs manager handles background tool execution:

- Spawns long-running shell commands into background goroutines.
- Tracks job state (running, completed, failed).
- Provides output polling and kill capabilities.
- Emits completion notes that the controller drains into the next turn.
- Cancelled on session close.

## Diagnostic Tools

### Doctor (`internal/doctor/`)

`reasonix doctor` runs a health check and produces a diagnostic report:

- Config resolution check.
- API key availability.
- MCP server connectivity.
- CodeGraph status.
- File encoding support.

### MCP Diagnostics (`internal/mcpdiag/`)

Diagnoses MCP server authentication issues:

- Checks auth configurations.
- Reports missing or invalid credentials.
- Provides actionable error messages.

## Nil Utilities (`internal/nilutil/`)

Helper functions for nil-safe interface checks:

- `IsNil(v)` — checks whether an interface value is nil, even when it holds a typed nil pointer. This prevents the common Go pitfall where `var g Gate = (*MyGate)(nil)` is non-nil as an interface.

## System Proxy (`internal/sysproxy/`)

Detects and uses system HTTP proxy settings:

- Platform-specific implementations (Windows vs Unix).
- Reads `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` environment variables.
- On Windows, reads the system registry proxy settings.

## File Reference Search (`internal/fileref/`)

Resolves `@<path>` references in chat messages:

- Searches for files relative to the workspace root.
- Returns file contents or directory listings.
- Handles MCP resource references (`@<server>:<uri>`).

## Command System (`internal/command/`)

Manages custom slash commands:

- Loads Markdown files from `.reasonix/commands/` and `~/.config/reasonix/commands/`.
- Supports frontmatter (description, argument-hint).
- Template substitution: `$ARGUMENTS`, `$1`…`$N`, `$$`.
- Subdirectory namespacing: `git/commit.md` → `/git:commit`.

## Process Utilities (`internal/proc/`)

Platform-specific process management:

- `kill_windows.go` — kill process trees on Windows.
- `kill_other.go` — send signals on Unix.
- `hide_windows.go` — hide console windows on Windows.
- `hide_other.go` — no-op on Unix.

## Output Style (`internal/outputstyle/`)

Manages the agent's output persona/tone:

- Built-in styles: `explanatory`, `learning`, `concise`.
- Custom styles: `.reasonix/output-styles/<name>.md`.
- Folded into the system prompt as additional instructions.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Agent Loop & Coordinator](agent-loop-coordinator.md)
- [Tool System](tool-system.md)
- [Memory & Skills](memory-skills.md)
