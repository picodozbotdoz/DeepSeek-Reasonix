# Prompt Refinement (Clarify)

The **Clarify** feature lets you refine your draft prompts with AI assistance before
submitting them as a normal turn. Instead of sending raw text to the agent, you can
ask the model to produce 2–3 improved versions, pick the one you like, and then
submit it — optionally editing further before sending.

## How to Use

### Ctrl+K (Primary Trigger)

Type a prompt in the chat input and press **Ctrl+K**:

```
> Write a function to parse CSV files
                                                ← press Ctrl+K
```

A loading indicator appears briefly, then a picker overlay shows the original
prompt plus 2–3 refined versions:

```
┌──────────────────────────────────────────────────┐
│ ◆ Prompt Refinement                              │
│ ↑/↓ navigate · Enter select · Esc keep original  │
├──────────────────────────────────────────────────┤
│ ● Original                                        │
│   Write a function to parse CSV files             │
│                                                   │
│ ○ Refined 1                                       │
│   Create a CSV parser in internal/parse/csv.go     │
│   that handles headers, quoted fields, and         │
│   returns typed rows with error reporting          │
│                                                   │
│ ○ Refined 2                                       │
│   Implement a ReadCSV function in Go that          │
│   accepts a file path with header detection        │
│   and returns structured data with errors          │
└──────────────────────────────────────────────────┘
```

- **↑/↓** or **Tab/Shift+Tab** — navigate between options
- **Enter** — select the highlighted option (replaces input box)
- **Esc** — keep the original prompt unchanged

After selecting, the chosen text replaces the input box. You can edit it further
before pressing **Enter** to submit it as a normal turn.

### /clarify Command

You can also refine text inline with the `/clarify` slash command:

```
/clarify Write a function to parse CSV files with error handling
```

This refines the text after `/clarify` and shows the same picker overlay.
The text is used directly as the draft — without needing anything in the input box.

> **Note:** `/clarify` without text shows a usage hint.

### When to Use Clarify

- **Vague prompts** — "fix the code" → "Fix the nil pointer dereference in
  `internal/auth/handler.go` by adding a nil check before calling `.Write()`"
- **Multi-part asks** — make each step explicit
- **Missing context** — add file paths, constraints, or error conditions the
  refiner infers from your intent
- **Uncertain phrasing** — see how the model rephrases your goal before
  committing tokens to a full turn

## How It Works

### Architecture

```
User types prompt → Ctrl+K triggers clarify
                         │
                         ▼
              TUI calls Controller.ClarifyPrompt()
                         │
                         ▼
              clarify.Refine() builds a provider.Request
              with Tools: nil — no tool schemas, no tool calls
                         │
                         ▼
              Provider.Stream() — lightweight text generation
              System: "You are a prompt refinement assistant…"
              User:   "Draft: <user's original prompt>"
                         │
                         ▼
              Response parsed for VERSION:-prefixed lines
              Fallback: full response as one suggestion
                         │
                         ▼
              Original + 2–3 refinements returned to TUI
                         │
                         ▼
              Picker overlay → user selects → input replaced
                         │
                         ▼
              User presses Enter → normal turn via
              Controller.Submit(selected)
```

### Key Design Points

- **No tool calls**: The refinement request passes `Tools: nil`, producing pure
  text generation. The model cannot call bash, edit files, or do any complex
  processing — it only generates text. This keeps the refinement fast and cheap.
- **Original always preserved**: The first option in the picker is always the
  unmodified original text.
- **Output format**: The model is instructed to produce `VERSION:`-prefixed
  lines. The parser extracts 2–3 versions. If no `VERSION:` lines are found
  (e.g., the model didn't follow instructions), the full response text is used
  as a single fallback refinement.
- **30-second timeout**: Refinement calls time out after 30 seconds. On timeout,
  the input box is unchanged and a notice is shown.
- **Non-blocking**: The refinement runs asynchronously — the TUI shows a
  loading spinner while the model generates options.

### Provider

By default, clarification uses the **same provider/model** as the active
session, but without tool schemas. You can optionally configure a dedicated
model for refinement:

```toml
[agent]
clarify_model = "deepseek/deepseek-chat"   # optional, falls back to default_model
```

Use a cheaper or faster model for refinement when your main model is expensive
or slow. When `clarify_model` is set but invalid, a warning is logged and the
session provider is used instead.

### Configuration Reference

```toml
[agent]
# Optional: use a different model for prompt refinement.
# When empty (default), the active session model is used.
clarify_model = "deepseek/deepseek-chat"
```

## Non-Interactive Mode (`reasonix run --clarify`)

In non-interactive mode (`reasonix run`), add the `--clarify` (or `-C`) flag to
refine the prompt before submitting it:

```bash
reasonix run --clarify "Write a function to parse CSV files"
```

Or with stdin:

```bash
echo "Write a function to parse CSV files" | reasonix run --clarify
```

The refinement runs automatically:
1. The prompt is sent to the model with `Tools: nil` — same as the interactive flow
2. The **first refined version** (not the original) is selected automatically
3. The before/after diff is printed to stderr
4. If refinement fails (network error, timeout), a warning is printed and the
   original prompt is used

Example output:

```
◇ prompt refined
  before: Write a function to parse CSV files
  after:  Create a CSV parser in internal/parse/csv.go that handles headers, quoted fields, and returns typed rows with error reporting
```

The `--clarify` flag works with `--model`, `--max-steps`, `--continue`, and
other `reasonix run` options.

## Related

- [Design document](design-clarify-phase0.md) — detailed implementation design
