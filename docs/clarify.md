# Prompt Refinement (Clarify)

The **Clarify** feature lets you refine your draft prompts with AI assistance before
submitting them as a normal turn. It has two modes:

- **Mode 2 (Fresh)** — prefix-stable request, optionally includes recent conversation
  history. Fast and cache-efficient. Activated via `Ctrl+K` or `/clarify`.
- **Mode 1 (Context)** — sends full session context so the model sees the entire
  conversation. No prefix caching, but richer refinements. Activated via
  `Ctrl+Shift+K` (or `Cmd+K` on Mac) or `/clarify --context`.

## Quick Reference

| Trigger | Mode | Use case |
|---------|------|----------|
| `Ctrl+K` | Fresh | Quick refine of your draft, best cache efficiency |
| `Ctrl+Shift+K` / `Cmd+K` | Context | Refine with full conversation context |
| `/clarify <text>` | Fresh | Inline refine, standalone text |
| `/clarify --context <text>` | Context | Inline refine with session context |
| `reasonix run --clarify` | Fresh | Non-interactive refine |
| `reasonix run --clarify-context` | Context | Non-interactive refine with session |

## How to Use

### Ctrl+K (Fresh Mode — Default)

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

### Ctrl+Shift+K (Context Mode)

Same as Ctrl+K, but the model sees the full conversation history for context-aware
refinements. Use this when your draft references earlier discussion in the session.

### /clarify Command

```
/clarify Write a function to parse CSV files with error handling
/clarify --context Refine the approach we discussed above
```

`/clarify` without `--context` uses fresh mode; with `--context` uses context mode.

### When to Use Clarify

- **Vague prompts** — "fix the code" → "Fix the nil pointer dereference in
  `internal/auth/handler.go` by adding a nil check before calling `.Write()`"
- **Multi-part asks** — make each step explicit
- **Missing context** — add file paths, constraints, or error conditions the
  refiner infers from your intent
- **Uncertain phrasing** — see how the model rephrases your goal before
  committing tokens to a full turn

## How It Works

### Architecture (Mode 2 — Fresh)

```
User types prompt → Ctrl+K triggers clarify (mode 2)
                         │
                         ▼
              TUI calls Controller.ClarifyPrompt()
                         │
                         ▼
              clarify.RefineFresh() builds a fresh provider.Request:
              [system prompt] + [instruction] + [history(N pairs)]
              + [Draft:\n + input]
              with Tools: nil
                         │
                         ▼
              Provider.Stream() — lightweight text generation
                         │
                         ▼
              Response parsed for VERSION:-prefixed lines
              Fallback: full response as one suggestion
                         │
                         ▼
              Original + 2–3 refinements returned → picker → submit
```

### Cache Boundary (Mode 2 — Fresh)

The fresh mode is designed for **DeepSeek prefix caching**:

```
[system prompt]      — fixed, cached across ALL clarify calls
[instruction]        — fixed, cached ("" when none configured)
[history(N pairs)]   — variable content but fixed size; 
                       N=0 means no history → full cache hits
[Draft:\n + input]   — "Draft:\n" prefix cached; user input varies
```

When `max_history_pairs = 0` (the default), every clarify call hits cache on the
entire prefix except the final user input. With `max_history_pairs > 0`, the
history content changes each turn but the system prompt + instruction prefix
stays cached.

### Architecture (Mode 1 — Context)

```
Ctrl+Shift+K triggers context mode
                         │
                         ▼
              Controller.ClarifyPromptContext()
              reads full session messages from executor
                         │
                         ▼
              clarify.RefineContextual() builds a request with:
              [system prompt] + [instruction] + [session messages]
              + [Draft:\n + input]
              with Tools: nil
                         │
                         ▼
              No prefix cache — session messages change each turn
              But refinements are aware of the full conversation
```

### Key Design Points

- **No tool calls**: `Tools: nil` — pure text generation. The model cannot call
  bash, edit files, or do any complex processing. It only generates text.
- **Original always preserved**: The first picker option is always the unmodified
  original text.
- **Output format**: The model produces `VERSION:`-prefixed lines. The parser
  extracts 2–3 versions. If no `VERSION:` lines are found, the full response
  text is used as a fallback.
- **30-second timeout**: Refinement calls time out after 30 seconds.
- **Non-blocking**: The refinement runs asynchronously in the TUI.

## Non-Interactive Mode (`reasonix run`)

```bash
# Fresh mode (prefix-stable, fastest)
reasonix run --clarify "Write a function to parse CSV files"
reasonix run -C "Write a function to parse CSV files"

# Context mode (uses resumed session for context)
reasonix run --clarify-context "Refine our approach"
```

Or with stdin:

```bash
echo "Write a function to parse CSV files" | reasonix run --clarify
```

The refinement runs automatically:
1. The prompt is sent to the model with `Tools: nil`
2. The **first refined version** (index 1) is selected automatically
3. The before/after diff is printed to stderr
4. If refinement fails, a warning is printed and the original prompt is used

Example output:

```
◇ prompt refined
  before: Write a function to parse CSV files
  after:  Create a CSV parser in internal/parse/csv.go that handles headers, quoted fields, and returns typed rows with error reporting
```

## Configuration Reference

```toml
[clarify]
# Optional: different model for clarification (default: session model)
model = "deepseek/deepseek-chat"

# Mode 2 — fresh, prefix-stable request
[clarify.fresh]
enabled = true                        # enable this mode (default: true)
system_prompt = ""                    # custom system prompt (empty = built-in)
instruction = ""                      # extra guidance before the draft
max_history_pairs = 2                 # 0 = no history, full cache hits

# Mode 1 — context-aware (full session context)
[clarify.context]
enabled = true                        # enable this mode (default: true)
system_prompt = ""                    # custom system prompt (empty = built-in)
instruction = "Consider the conversation above when refining."
```

Legacy `[agent] clarify_model` is still supported but `[clarify].model` takes
precedence when both are set.

## Related

- [Design document](design-clarify-phase0.md) — detailed implementation design
