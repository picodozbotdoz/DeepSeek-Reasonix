# Phase 0: "Clarify" Prompt Refinement — Design Document

## Executive Summary

Add an AI-assisted prompt refinement feature (Phase 0 / "Clarify" mode) to the Reasonix CLI chat TUI. When activated, the user's raw draft prompt is sent to an LLM with a system prompt asking for 2–3 refined versions. The refinements are presented as a selectable list; the user picks one (or Esc to keep the original), and the chosen text enters the normal turn pipeline.

## 1. Trigger Mechanism

### Primary: Slash Command `/clarify`

- The user types `/clarify <optional focus hint>` in the TUI input and presses Enter
- `<optional focus hint>` is a brief instruction appended to the refinement system prompt (e.g., `/clarify make it more concise`)
- A bare `/clarify` (no args) refines the full current input value as it stands

### Secondary: Hotkey Ctrl+K

- Ctrl+K is currently unbound in the TUI (verified by scanning `chat_tui.go` key handlers)
- "K" as in "Klarity" — mnemonic, easy to reach
- Pressing Ctrl+K while the input has text triggers the same flow as `/clarify`
- Pressing Ctrl+K on an empty input shows a notice: "nothing to clarify — type a prompt first"
- Bound in the `chatTUI.update()` key switch, before the Enter handler

### Rationale

- `/clarify` is discoverable via `/help` and the completion menu
- Ctrl+K is fast for power users who already know the feature
- Ctrl+J is already bound to InsertNewline (Alt+Enter/Shift+Enter), so it cannot be reused
- Alt+Enter could be re-targeted, but that breaks existing muscle memory for multi-line input

## 2. Provider Strategy

### Design: Reuse the same provider, suppress tools

The refinement call uses the **same provider/model** as the active session, but sends a `provider.Request` with an **empty Tools slice** (no tool schemas). This produces a pure text-generation response with no tool calls — exactly what we need.

Rationale:
- The user's API key, base URL, and model capabilities are already correct
- No tool-related tokens consumed, no tool-call overhead
- The system prompt ("You are a prompt refinement assistant…") is short
- The user's raw text plus the system prompt is well under any context window

### Config: Optional `clarify_model` override

Add to `config.AgentConfig`:

```toml
[agent]
clarify_model = "deepseek/deepseek-chat"    # optional, falls back to default_model
```

When set, `boot.Build` constructs a **second, lightweight provider** from that model ref and passes it as `ClarifyProvider` to the Controller. When empty, the Controller reuses its executor's provider for the call.

This is a Phase 0 concession to power users who want a cheaper/faster model for refinement (e.g., a local model or a cheaper tier). The field is entirely optional.

## 3. Architecture & Implementation Layer

### Interception Point: TUI Layer, Before Controller.Submit

The clarify flow is entirely in the TUI layer:

1. User triggers via `/clarify` or Ctrl+K
2. TUI calls a new `Controller.ClarifyPrompt(ctx, input string) ([]string, error)` method
3. Controller makes a **synchronous** provider call (blocking the Update loop briefly)
4. TUI gets back the refined versions, shows a chooser overlay
5. User picks one → TUI calls `Controller.Submit(selected)` 
6. User presses Esc → TUI calls `Controller.Submit(original)`

### New Files

| File | Purpose |
|------|---------|
| `internal/clarify/clarify.go` | Core refinement logic: system prompt, option parsing, provider call |
| `internal/clarify/clarify_test.go` | Unit tests for option parsing |

### Modified Files

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `ClarifyModel` field to `AgentConfig` |
| `internal/config/config.go` | Add `ClarifyModel()` accessor to `Config` |
| `internal/control/controller.go` | Add `ClarifyProvider` to `Options`; add `ClarifyPrompt()` method |
| `internal/boot/boot.go` | Resolve `clarify_model` from config, build `ClarifyProvider` if set |
| `internal/cli/chat_tui.go` | Add `/clarify` slash command handler, Ctrl+K binding, chooser overlay |
| `internal/cli/chat_tui.go` | Add `clarifyChooser` type (model + view) for the option picker |
| `internal/i18n/messages_en.go` | Add clarify-related translatable strings |
| `internal/i18n/messages_zh.go` | Add Chinese translations |
| `internal/i18n/messages_zh_tw.go` | Add Traditional Chinese translations |
| `internal/i18n/i18n.go` | No change needed (struct fields auto-detected by test) |

### No New Dependencies

- Uses the existing `provider.Provider` interface and `Stream()` method
- Uses the existing bubbletea TUI framework
- No third-party dependencies

## 4. Detailed Implementation

### 4a. `internal/clarify/clarify.go` — Core Logic

```go
package clarify

import (
    "context"
    "fmt"
    "strings"

    "reasonix/internal/provider"
)

// SystemPrompt for the refinement model call. Short, instructive, yields structured output.
const SystemPrompt = `You are a prompt refinement assistant for a coding agent called Reasonix.
The user has written a draft prompt for the agent. Your job is to produce 2–3 improved versions.
Each version should be a clear, specific, actionable instruction that helps the agent understand
what the user wants. Consider:
- Making vague requests concrete
- Breaking multi-part asks into clear steps
- Adding relevant context (files, paths, constraints) when inferable
- Keeping the user's original intent and voice

Output exactly 2–3 versions, each on a line starting with "VERSION:" followed by the text.
Do not include numbering, markdown, or extra commentary — just the VERSION: lines.
Example:
VERSION: Refactor the authentication handler in internal/auth/ to use the new JWT library. Update the signing key rotation logic and add tests.
VERSION: Update the auth package to use the new JWT library: rewrite handler.go, update key rotation, and add unit tests covering the new flow.
`

// Refine calls the provider to generate 2–3 prompt refinements.
// input is the user's raw prompt text.
// focusHint is an optional instruction from /clarify <hint>.
// Returns the original + refined versions (at least 2, at most 3).
func Refine(ctx context.Context, prov provider.Provider, input, focusHint string) ([]string, error) {
    if strings.TrimSpace(input) == "" {
        return nil, fmt.Errorf("cannot clarify empty input")
    }

    userMsg := input
    if focusHint != "" {
        userMsg = fmt.Sprintf("Focus: %s\n\n---\n\nDraft:\n%s", focusHint, input)
    }

    req := provider.Request{
        Messages: []provider.Message{
            {Role: provider.RoleSystem, Content: SystemPrompt},
            {Role: provider.RoleUser, Content: userMsg},
        },
        Tools:       nil, // NO tools — pure text generation
        Temperature: 0.7, // slight creativity for diverse versions
        MaxTokens:   1024,
    }

    ch, err := prov.Stream(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("clarify: stream: %w", err)
    }

    var sb strings.Builder
    for chunk := range ch {
        switch chunk.Type {
        case provider.ChunkText:
            sb.WriteString(chunk.Text)
        case provider.ChunkError:
            return nil, fmt.Errorf("clarify: %w", chunk.Err)
        }
        // ChunkReasoning, ChunkToolCall*, ChunkUsage, ChunkDone are ignored
    }

    versions := parseVersions(sb.String())
    if len(versions) == 0 {
        // Fallback: return the whole response as one suggestion
        trimmed := strings.TrimSpace(sb.String())
        if trimmed != "" {
            return []string{input, trimmed}, nil
        }
        return []string{input}, nil
    }

    // Prepend the original as the first option
    return append([]string{input}, versions...), nil
}

// parseVersions extracts VERSION:-prefixed lines from the model output.
func parseVersions(text string) []string {
    var versions []string
    for _, line := range strings.Split(text, "\n") {
        trimmed := strings.TrimSpace(line)
        if after, ok := strings.CutPrefix(trimmed, "VERSION:"); ok {
            v := strings.TrimSpace(after)
            if v != "" {
                versions = append(versions, v)
            }
        }
    }
    if len(versions) > 3 {
        versions = versions[:3]
    }
    return versions
}
```

### 4b. `internal/config/config.go` — New Config Fields

Add to `AgentConfig`:

```go
// ClarifyModel optionally names a provider/model for prompt refinement.
// When empty, refinement uses the active session's model.
ClarifyModel string `toml:"clarify_model"`
```

Add to `Config`:

```go
// ClarifyModel returns the configured clarify model ref, or "" for default.
func (c *Config) ClarifyModel() string {
    if c == nil {
        return ""
    }
    return strings.TrimSpace(c.Agent.ClarifyModel)
}
```

### 4c. `internal/boot/boot.go` — Build ClarifyProvider

In `Build()`, after resolving the main provider (`execProv`):

```go
// Resolve the clarify provider (optional — when clarify_model is set).
var clarifyProv provider.Provider
if cm := cfg.ClarifyModel(); cm != "" {
    ce, ok := cfg.ResolveModel(cm)
    if !ok {
        // Non-fatal: warn and fall back to the main provider
        sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
            Text: fmt.Sprintf("clarify_model %q not found — using default model for prompt refinement", cm)})
    } else {
        cp, err := NewProviderWithProxy(ce, proxySpec)
        if err != nil {
            sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn,
                Text: fmt.Sprintf("clarify_model %q failed: %s — using default model", cm, err)})
        } else {
            clarifyProv = cp
        }
    }
}
```

Add `ClarifyProvider` to `control.Options`:

In `control.Options`:
```go
ClarifyProvider provider.Provider // optional — for prompt refinement; nil = use executor's
```

Pass it in the controller construction:
```go
ctrlOpts := control.Options{
    // ... existing fields ...
    ClarifyProvider: clarifyProv,
}
```

### 4d. `internal/control/controller.go` — ClarifyPrompt Method

First, resolve the provider to use for clarity:

```go
// clarifyProvider returns the provider to use for prompt refinement.
// Prefers the dedicated ClarifyProvider; falls back to the executor's provider.
func (c *Controller) clarifyProvider() (provider.Provider, error) {
    if c.clarifyProv != nil {
        return c.clarifyProv, nil
    }
    // The executor's provider is not exported, so we use the boot-time
    // --runner or --executor's provider. For Phase 0, this is populated
    // at construction time.
    if c.executor == nil {
        return nil, fmt.Errorf("no provider available for clarification")
    }
    // The Agent doesn't expose its provider, but we store it separately.
    // If c.clarifyProv is nil at this point, and no separate provider was
    // set, the controller's Options should store a reference.
    // See the ClarifyProvider field in Options.
    return nil, fmt.Errorf("clarify: no provider available")
}
```

Add `ClarifyProvider` to Options and to the Controller struct.

New method:

```go
// ClarifyPrompt refines the input text via an LLM call, returning options.
// Returns the original text as the first option, plus 2–3 refinements.
func (c *Controller) ClarifyPrompt(ctx context.Context, input string) ([]string, error) {
    if strings.TrimSpace(input) == "" {
        return nil, fmt.Errorf("nothing to clarify")
    }
    
    prov := c.clarifyProv
    if prov == nil {
        // Last resort: try to extract from executor
        // (Phase 0: this is handled at Options time)
        return []string{input}, nil // no-op passthrough
    }
    
    return clarify.Refine(ctx, prov, input, "")
}
```

### 4e. `internal/cli/chat_tui.go` — TUI Integration

**New type:**

```go
// clarifyOption is a refined prompt candidate shown in the picker.
type clarifyOption struct {
    index int
    text  string
    label string // "Original" or "Refined 1", "Refined 2", etc.
}

// clarifyPicker is the modal overlay for choosing a refinement.
type clarifyPicker struct {
    options []clarifyOption
    sel     int
    loading bool
    err     string
    pending string    // the original text being refined
    done    bool      // true when the user has made a choice
    chosen  string    // the selected text
    cancel  bool      // true when user pressed Esc
}
```

**Slash command handler in `runSlashCommand`:**

```go
case "/clarify":
    if m.state == tuiRunning {
        m.notice("wait for the current turn to finish before clarifying")
        break
    }
    line := strings.TrimSpace(m.input.Value())
    if line == "" {
        // /clarify with no current text — maybe the hint follows the command
        // Actually, /clarify would have been intercepted before reaching here.
        // The command itself means we're in the "draft mode" where the input
        // box holds the prompt.
        // But at this point the input was already read and cleared by the
        // Enter handler. 
        // REVISIT: /clarify must be handled in the Enter handler, not here.
        break
    }
    m.startClarify(line, "")
```

Actually, `/clarify` needs special handling because it must read the current input value BEFORE it's cleared. Let me redesign the Enter handler:

In the Enter handler, before clearing the input:
```go
// New check: /clarify (inline refine of current input)
if line == "/clarify" || strings.HasPrefix(line, "/clarify ") {
    m.input.Reset()
    m.pastedBlocks = nil
    hint := strings.TrimSpace(strings.TrimPrefix(line, "/clarify"))
    m.startClarify(m.captureCurrentInput(), hint) // capture what the user already typed
    return m, finalize(m, cmds)
}
```

Wait, the issue is that `/clarify` without args means "clarify my current input". But if the user types `/clarify` and presses Enter, the input will ONLY contain `/clarify` — the original text was already submitted by Enter. That's wrong.

**Better approach**: Don't require a separate submit. Instead:
1. User types their prompt in the input box
2. User presses Ctrl+K (or types `/clarify` and presses Enter — but this clears the input)
3. For Ctrl+K: we take the current input value directly, no clearing
4. For `/clarify`: handle it like a command prefix that reads the remaining input

Actually, the cleanest approach is:
- **Ctrl+K**: triggers clarify on the current input text (value stays in input until option is picked)
- **`/clarify <hint>`**: the user types a hint along with the command. But to get the current input, they'd need to have already typed it. This is awkward.

**Revised approach**: 
- **Ctrl+K**: Primary trigger. Takes the current textarea value, shows the picker.
- **`/clarify`** (standalone slash command): Triggers the same flow using whatever was in the textarea before Enter was pressed. We need to save the input BEFORE clearing it in the Enter handler.

Actually, let me look at this more carefully. The user flow would be:

**Flow A: Ctrl+K**
1. User types "refactor the auth module" into the input box
2. Presses Ctrl+K
3. The current input text is captured (without clearing)
4. A spinner shows "Refining prompt…"
5. Refined options appear in a chooser overlay
6. User picks one → input is replaced with the chosen text
7. User can then press Enter to submit, or edit further

**Flow B: `/clarify`**
1. User types "refactor the auth module" into the input box
2. User then types `/clarify make it more specific` at the END of the text
   OR types `/clarify` as a prefix before the text
   OR realizes they need to first write the text, then the command is separate

Actually, slash commands work as: user types `/clarify` on a line by itself and presses Enter. This means the original text is lost. So `/clarify` as a standalone command doesn't work for refining existing text in the input box.

**Better design**: 
- **`/clarify <text>`** — the text to refine is provided inline after the command
  Example: `/clarify Write a function to parse CSV files with error handling`
- **Ctrl+K** — refine the text currently in the input box
- These are two different modes

Actually, the simplest Phase 0 is:
- **Ctrl+K** is the primary trigger — refines whatever is in the input box
- When no text is in the box, show a notice
- `/clarify` as a slash command could work if the text is in the `display` field — but that's complex

Let me simplify: **Phase 0 only implements Ctrl+K**. The `/clarify` command is added to the completion menu for discoverability but it just functions as a "how to use" hint, OR it works as `/clarify <text>` to refine inline text.

Actually, the simplest and most discoverable approach:

**Primary: Ctrl+K** — takes the current textarea content
**Secondary: `/clarify <optional focus>`** — user types `/clarify focus on error handling` and presses Enter. But this only refines what's after `/clarify`, not the whole input box.

For Phase 0, let's go with:
- **Ctrl+K** as the only trigger. It's simple, fast, and doesn't conflict with input flow.
- The `/help` screen documents it.
- A `/clarify` command is added that works like `/clarify <text>` — you type the text inline after the command.

But wait — re-reading the design constraints: "User types a prompt in the CLI chat input → User activates Clarify (via a hotkey like Ctrl+J or a special prefix like ?prompt or a new slash command /clarify)"

So the task specifically asks for multiple trigger options. Let me design both:

**Ctrl+K** — Takes the current textarea content (0-kEystroke cost)
**`/clarify <focus hint>`** — Refines the current textarea content with an optional focus hint (the focus hint follows `/clarify` ; the current textarea content is combined with it)
**`?` prefix** — Alternative: if input starts with `? ` the system treats it as a clarify request for the remaining text

Wait, the `?` prefix idea is interesting but complicates the input flow (what if you want to ask a question about `?`?).

**Final Trigger Design (Phase 0):**

1. **Primary: Ctrl+K** — refines whatever is in the input box. Focus hint can be typed in the box before pressing Ctrl+K.
2. **Secondary: `/clarify <optional hint>`** — If the user has text in the box and types `/clarify make it more specific` and presses Enter, the system refines the CURRENT BOX TEXT + the hint. This means the Enter handler must:
   a. Check if the input starts with `/clarify`
   b. If so, save the textarea's current value BEFORE the user typed `/clarify`
   
   Actually this is impossible to do retroactively. Instead, `/clarify` works like: you type `/clarify make it more specific` as the entire input, and the refinement system takes just `make it more specific` as the hint and... what's the prompt? There's no prompt.

   So `/clarify` works best as: the text AFTER `/clarify` IS the prompt to refine. Example: `/clarify Write a Python script to parse CSV files` — the `Write a Python script...` part is the text to refine.

This makes `/clarify` a complete replacement for the normal input flow — you compose your prompt and refine in one step. This is complementary to Ctrl+K (where you already have text in the box).

OK, for Phase 0, let me keep it simple:

**Trigger: Ctrl+K** — refines the current input text. This is THE primary trigger.
**Trigger: `/clarify <text>`** — type text inline after the command to refine it.
**No `?` prefix** — avoided to prevent ambiguity.

The `/clarify` command is handled in the Enter handler:

```go
// In the Enter handler, before clearing input:
if strings.HasPrefix(line, "/clarify") {
    m.input.Reset()
    m.pastedBlocks = nil
    target := strings.TrimSpace(strings.TrimPrefix(line, "/clarify"))
    if target == "" {
        m.notice("usage: /clarify <text to refine>")
        return m, finalize(m, cmds)
    }
    return m.startClarify(target, "")
}
```

And Ctrl+K is handled in the key switch:

```go
case "ctrl+k":
    if m.state == tuiRunning {
        return m, nil // ignore while running
    }
    text := m.input.Value()
    if strings.TrimSpace(text) == "" {
        m.notice("nothing to clarify — type a prompt first")
        return m, nil
    }
    return m.startClarify(text, "")
```

**startClarify method:**

```go
func (m *chatTUI) startClarify(text, hint string) tea.Cmd {
    m.clarifyPicker = &clarifyPicker{
        options: []clarifyOption{
            {index: 0, text: text, label: "Original"},
        },
        sel:     0,
        loading: true,
        pending: text,
    }
    // Run the refinement in a goroutine, send result as tea.Msg
    return func() tea.Msg {
        versions, err := m.ctrl.ClarifyPrompt(context.Background(), text)
        if err != nil {
            return clarifyResultMsg{err: err}
        }
        return clarifyResultMsg{versions: versions}
    }
}
```

**clarifyResultMsg handler:**

```go
case clarifyResultMsg:
    if msg.err != nil {
        m.clarifyPicker = nil
        m.notice("clarify failed: " + msg.err.Error())
        break
    }
    if m.clarifyPicker == nil {
        break
    }
    m.clarifyPicker.loading = false
    for i, v := range msg.versions {
        label := fmt.Sprintf("Refined %d", i) // 0-indexed
        if i == 0 {
            label = "Original"
        }
        m.clarifyPicker.options = append(m.clarifyPicker.options, clarifyOption{
            index: i, text: v, label: label,
        })
    }
    m.clarifyPicker.sel = 0
```

**clarifyPicker key handling in update:**

```go
// In the key press switch, before the modal checks:
if m.clarifyPicker != nil {
    if m.clarifyPicker.loading {
        // Still loading — ignore keys except Esc
        if msg.String() == "esc" {
            m.clarifyPicker = nil
            return m, nil
        }
        return m, nil
    }
    switch msg.String() {
    case "up":
        if m.clarifyPicker.sel > 0 {
            m.clarifyPicker.sel--
        }
    case "down":
        if m.clarifyPicker.sel < len(m.clarifyPicker.options)-1 {
            m.clarifyPicker.sel++
        }
    case "enter":
        chosen := m.clarifyPicker.options[m.clarifyPicker.sel].text
        m.clarifyPicker = nil
        // Set the input to the chosen text
        m.input.SetValue(chosen)
        m.growInputToFit()
        // User can now edit and press Enter to submit
        return m, finalize(m, cmds)
    case "esc":
        m.clarifyPicker = nil
        // Restore the original text to the input
        m.notice("clarify cancelled — using original")
        return m, finalize(m, cmds)
    }
    return m, nil
}
```

## 5. Option Presentation (UI)

The clarified options are shown as a **modal chooser overlay** in the TUI, reusing the pattern from `chooser.go` (the `ask` tool's question card) and `rewindPicker`.

**Layout:**

```
┌──────────────────────────────────────────────────┐
│ ◆ Prompt Refinement                              │
│ ↑/↓ navigate · Enter select · Esc use original   │
├──────────────────────────────────────────────────┤
│ ○ Original                                        │
│   Write a function to parse CSV files             │
│                                                   │
│ ● Refined 1                                       │
│   Create a CSV parser in internal/parse/csv.go     │
│   that handles headers, quoted fields, and         │
│   returns typed rows with error reporting          │
│                                                   │
│ ○ Refined 2                                       │
│   Implement a ReadCSV function in Go that          │
│   accepts a file path, parses CSV with header      │
│   detection, supports custom delimiters, and       │
│   returns structured data with validation errors   │
└──────────────────────────────────────────────────┘
```

Implementation:
- Use the same lipgloss styling as the rest of the TUI
- Selected item uses `●` and accent color
- Non-selected uses `○` and dim
- The text is soft-wrapped to the viewport width
- The overlay is rendered in the `View()` method when `m.clarifyPicker != nil`
- The overlay replaces the normal input area rendering
- Height is computed from content length (capped at `maxModalHeight`)

## 6. Edge Cases & Error Handling

| Edge Case | Handling |
|-----------|----------|
| **Empty input** | Neither Ctrl+K nor `/clarify` activates if input is empty after trimming; a notice is shown |
| **Network error** | The Stream call returns error; `clarifyResultMsg` carries the error; the TUI shows a notice and leaves the input box unchanged |
| **Model returns no VERSION: lines** | `parseVersions` returns empty; fallback uses the full response text as a single refinement — always at least the original is returned |
| **Model returns >3 versions** | Capped at 3 |
| **Already-running turn** | Ctrl+K is ignored while `tuiRunning`; `/clarify` is caught in the slash handler and shows "wait for the current turn" notice |
| **Provider timeout** | The Stream call has a 30s context timeout; on timeout the TUI shows "clarify timed out" and returns to the original input |
| **Zero-length model output** | Fallback returns `[original]` — a single-element list means the picker shows just the original |
| **Model returns tool calls despite no tools** | The `parseVersions` function handles this by ignoring non-text chunks; if only tool calls come back, the fallback returns `[original]` |
| **ClarifyProvider configured but fails to build** | Non-fatal at boot: a warning notice is emitted, and the Controller falls back to nil clarifyProv. If both clarifyProv and executor provider are unavailable, ClarifyPrompt returns a passthrough `[input]` |

## 7. Translatable Strings

Add to `i18n.Messages`:

```go
ClarifyTitle          string // "Prompt Refinement"
ClarifyHint           string // "↑/↓ navigate · Enter select · Esc use original"
ClarifyOriginalLabel  string // "Original"
ClarifyRefinedFmt     string // "Refined %d"
ClarifyEmpty          string // "nothing to clarify — type a prompt first"
ClarifyRunning        string // "wait for the current turn to finish before clarifying"
ClarifyFailedFmt      string // "clarify failed: %s"
ClarifyCancelled      string // "clarify cancelled — using original"
ClarifyTimeout        string // "clarify timed out — using original"
ClarifyUsage          string // "usage: /clarify <text to refine>  or  press Ctrl+K with text in the input"
ClarifyWorking        string // "refining prompt…"
ClarifyHotkeyHelp     string // "ctrl+k  refine your prompt with AI"
```

## 8. Test Plan

| Test | Location | What |
|------|----------|------|
| `parseVersions` | `clarify/clarify_test.go` | Parses VERSION: lines correctly; handles 0, 1, 2, 3, 4+ versions; handles extra whitespace; handles empty lines |
| `Refine` with mock provider | `clarify/clarify_test.go` | Refine returns original + 2 versions; handles tool calls in response gracefully; handles error from provider |
| `ClarifyPrompt` | `control/controller_test.go` | Calls through to clarify.Refine; handles nil clarifyProv gracefully; passthrough fallback |
| TUI integration | `cli/chat_tui_test.go` | Ctrl+K triggers flow; `/clarify` triggers flow; Esc restores original; Enter selects option; loading state renders correctly |
| Config | `config/config_test.go` | `clarify_model` is parsed from TOML; empty field works; invalid ref resolved gracefully |
| Boot | `boot/boot_test.go` | clarify_model resolves to provider; invalid clarify_model warns but doesn't fail boot |

## 9. Implementation Order (Implementation-Ready Checklist)

1. **Config**: Add `ClarifyModel` to `AgentConfig` + accessor on `Config`
2. **Clarify package**: Create `internal/clarify/clarify.go` + `clarify_test.go`
3. **Control**: Add `ClarifyProvider` to `Options` + `Controller` struct; add `ClarifyPrompt` method
4. **Boot**: Resolve `clarify_model` in `Build()`; wire into controller options
5. **TUI (model)**: Add `clarifyPicker` struct + fields to `chatTUI`
6. **TUI (update)**: Add Ctrl+K handler, `/clarify` handler in Enter path, `clarifyResultMsg` handler, picker key navigation
7. **TUI (view)**: Add overlay rendering for the picker in `View()`
8. **i18n**: Add translation strings to all 3 message files
9. **Test**: Write unit tests for parsing, Refine with mock provider, TUI flow

Estimated implementation time: **4–6 hours** for a developer familiar with the codebase.

## 10. Future Phases (Post-Phase 0)

| Phase | Feature |
|-------|---------|
| Phase 1 | Clarify in `/run` mode (non-interactive) — refine and submit in one shot |
| Phase 2 | Clarify with context awareness — the system prompt includes recent conversation history, so refinements can reference earlier turns |
| Phase 3 | Clarify with tool-aware suggestions — the refiner knows which tools are available and can suggest prompts that use them effectively |
| Phase 4 | Multi-turn iterative refinement — the user can give feedback on a refinement and get another round |
