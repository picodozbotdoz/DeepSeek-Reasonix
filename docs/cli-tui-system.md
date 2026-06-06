# CLI TUI System

The CLI is Reasonix's largest subsystem (78 Go source files, 20K+ lines) and the primary interactive frontend. It implements a rich terminal user interface with markdown rendering, streaming text, interactive approval flows, and a full set of slash commands. This document covers the TUI's architecture, rendering pipeline, and interactive components.

## Architecture Overview

The CLI TUI is built on a layered architecture:

1. **`cli.go`** — Entry point and command dispatch (`reasonix chat`, `reasonix run`, `reasonix init`, etc.)
2. **`chat_tui.go`** — The main chat loop: input handling, event processing, rendering
3. **`style.go` / `theme.go`** — ANSI styling and color themes
4. **`md.go`** — Markdown rendering to ANSI terminal output
5. **Component files** — Individual UI components (toolcard, statusline, diffview, etc.)

### Event-Driven Rendering

The chat TUI is an event consumer — it subscribes to the same `event.Sink` that the agent emits into. Every state change (streaming text, tool dispatch, usage, compaction) arrives as a typed event, and the TUI re-renders only what changed. This architecture keeps the TUI decoupled from the agent core and ensures that any frontend (TUI, HTTP, desktop) renders the same event stream identically.

## The Chat TUI

### Input Handling

The chat TUI processes user input through several paths:

- **Regular text**: Sent as a user turn to the controller
- **Slash commands** (`/compact`, `/resume`, etc.): Dispatched to the command handler
- **Shell escape** (`!command`): Executed directly as a shell command
- **File attachment** (`@path`): Reads the file and attaches it to the message
- **Image paste**: Binary image data attached to the message

### Cancel Behavior

- **First Ctrl+C during a turn**: Cancels the in-flight turn but keeps the chat running
- **Second Ctrl+C while idle**: Exits the chat

### Auto-Plan Mode

The autoplan system (`autoplan.go`) automatically enables plan mode for the first turn of a new conversation, then disables it for subsequent turns. This gives the model a chance to explore the codebase before making changes.

## Markdown Rendering

### The `md.go` Renderer

The markdown renderer converts CommonMark-style markdown to styled ANSI terminal output:

- **Headings**: Bold with color
- **Code blocks**: Syntax-highlighted (using Chroma or built-in highlighters)
- **Inline code**: Distinct background/foreground colors
- **Bold/italic**: ANSI SGR sequences
- **Lists**: Indented with proper bullet characters
- **Links**: Underlined with URL shown
- **Tables**: Aligned columns with borders

### CJK Support

The renderer handles CJK (Chinese, Japanese, Korean) text correctly:

- Fullwidth characters are measured at 2 columns
- Line breaking respects CJK word boundaries
- `md_cjk_test.go` verifies correct rendering of mixed CJK/Latin text

### LaTeX/Math Rendering

The `latex.go` and `mathnode.go` files provide terminal-friendly math rendering:

- Inline math (`$...$`) and display math (`$$...$$`)
- Unicode approximation of common math symbols
- Subscripts/superscripts using Unicode characters where available
- Fallback to plain text for unsupported constructs

## Streaming & Redraw

### Text Streaming

The agent streams text chunk-by-chunk as the model generates it. The TUI renders this as raw markdown in real-time. When the text stream completes (a `Message` event), the TUI:

1. Calculates how many rows the raw stream occupied (using `streamedRows`)
2. Moves the cursor back to where the stream started
3. Clears to end of screen
4. Re-emits the styled markdown

This "raw → styled" redraw gives users immediate feedback while ensuring the final output is properly formatted.

### Row Counting

```go
func streamedRows(s string, width int) int
```

Accurate row counting is critical for the redraw — if it's wrong, the cursor moves to the wrong position and the display breaks. The function:

- Strips ANSI SGR codes before measuring (they don't occupy columns)
- Uses `go-runewidth` for correct emoji, fullwidth, and ZWJ sequence measurement
- Handles line wrapping: a line exactly the terminal width does not wrap (terminals "lazy-wrap" only when the next character lands)

### Safety Limit

The redraw is skipped if `streamedRows` exceeds 200 — the cursor movement would be unreliable for very long outputs, and the raw text is still readable.

## Interactive Components

### Tool Cards (`toolcard.go`)

Tool cards display a tool call's lifecycle:

1. **Dispatch**: Shows the tool name, arguments (compact form), and read-only badge
2. **Preview**: For writer tools, shows the diff before the call runs
3. **Approval**: When gating is active, shows an approval prompt with allow/deny options
4. **Result**: Shows the outcome (success/failure, truncated output)

The compact argument rendering (`CompactArgs`) trims and caps tool arguments at 120 runes for the dispatch line.

### Status Line (`run_metrics.go`)

The status line shows real-time information during a turn:

- **Thinking**: Spinner + elapsed time + cancel hint
- **Tool working**: Spinner + tool name + elapsed time
- **Retrying**: Spinner + attempt/max
- **Idle**: Shortcut hints (keyboard commands)
- **Cache status**: Latest-turn or session-average cache hit rate

### Resume Picker (`resume_picker.go`)

An interactive picker that shows saved sessions with:

- Preview text (first user message)
- Turn count
- Last activity time
- Scope (project/global)

Uses the same selection UI as other pickers (up/down/enter/q).

### Skill Picker (`skill_picker.go`)

A full-screen interactive panel for managing skills:

- List all skills with scope badges (builtin/global/project/custom)
- Search/filter by name
- Toggle enable/disable
- Delete custom skills
- Show skill details (description, token count, allowed tools)

### Model Switcher (`model.go`)

An interactive model switcher that:

- Lists configured providers and their models
- Shows the current model
- Allows switching models mid-session (the controller is rebuilt with the new provider)

### Diff Viewer (`diffview.go`)

Displays unified diffs for writer tool previews and approval cards. Supports:

- Syntax highlighting
- Line numbers
- Folded sections for long diffs
- Added/removed line counts

### Todo Panel (`complete.go`)

A pinned panel that shows the current todo list:

- Task status (pending/in_progress/completed)
- Active form (present-tense description)
- Nesting levels for sub-tasks

### Chooser (`chooser.go`)

A generic interactive selection component used by:

- Model switcher
- Resume picker
- Init wizard (provider selection, model selection)

Supports single-select and multi-select modes with keyboard navigation.

## Slash Commands

The chat TUI supports a rich set of slash commands:

| Command | Description |
|---------|-------------|
| `/new` | Start a fresh conversation |
| `/compact [focus]` | Manually trigger compaction |
| `/rewind` | Open the rewind picker |
| `/tree` | Show the conversation branch tree |
| `/branch` | Fork the current conversation |
| `/switch` | Switch to a different branch |
| `/resume` | Resume a previous session |
| `/model` | Switch models |
| `/memory` | Show loaded memory files |
| `/remember <text>` | Quick-save to memory |
| `/forget <file>` | Remove a memory file |
| `/mcp` | Manage MCP servers |
| `/hooks` | View and manage hooks |
| `/paste-image` | Paste an image from clipboard |
| `/output-style` | Set output rendering style |
| `/theme` | Set color theme |
| `/language` | Set display language |
| `/skills` | Manage skills |
| `/verbose` | Toggle reasoning display |
| `/effort` | Set effort level |
| `/auto-plan` | Toggle auto-plan mode |
| `/help` | Show available commands |
| `/todo` | Show/hide the todo panel |
| `/quit` | Exit the chat |

## Theme System

### Color Themes (`theme.go`)

The TUI supports multiple color themes:

- **Dark** (default): Optimized for dark terminal backgrounds
- **Light**: Optimized for light terminal backgrounds
- **Custom**: User-defined via configuration

Themes affect:
- Text colors (primary, secondary, accent)
- Tool card colors (success, failure, pending)
- Diff colors (added, removed)
- Status line colors

### OSC Detection (`theme_osc_unix.go`)

On Unix terminals, the TUI detects the terminal's color scheme via OSC 11 queries. This allows automatic theme selection based on the terminal's current appearance. On Windows, this fallback is not available.

### Theme Switching

The `/theme` command lists available themes and applies the selection immediately. The theme is persisted in configuration so it survives restarts.

## Output Styles

### Style Options

The `/output-style` command controls how the agent's output is rendered:

- **Default**: Full markdown rendering with syntax highlighting
- **Plain**: Raw text without formatting
- **Minimal**: Reduced formatting (no syntax highlighting)

Styles are persisted in configuration.

## Doctor (`doctor.go`)

The `reasonix doctor` command collects redacted diagnostics for issue reports, including:

- Version, OS, architecture
- Configuration status (config paths, default model)
- Provider status (name, kind, base URL, key presence)
- Plugin status (name, transport, auto-start)
- CodeGraph status (enabled, resolved, version)
- LSP status (enabled, server count)
- Session statistics (directory, count, total bytes)
- Sandbox status (mode, network, write roots, availability)
- Network configuration (proxy mode, proxy, no_proxy)
- Permission rules (mode, allow/ask/deny counts)

Home directory paths are redacted to `~` for privacy.

## Git Status (`gitstatus.go`)

The git status component shows the current repository state in the status line, including branch name, dirty state, and uncommitted changes. This helps users understand the context of the agent's operations.

## Encoding Helpers

The CLI handles various text encoding edge cases:

- **CRLF normalization**: Edit operations handle Windows-style line endings
- **UTF-8 validation**: All text operations assume valid UTF-8
- **ANSI escape stripping**: Row counting and width measurement strip ANSI codes

## Help View (`help_view.go`)

The help view renders a formatted help page listing all available commands, keyboard shortcuts, and usage hints. It's displayed by `/help` and on first launch.
