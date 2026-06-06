# Output Style & Persona System

**Source:** `internal/outputstyle/outputstyle.go`

## Overview

The output style system lets users shift how the agent communicates without rewriting the system prompt. An output style is a named block of persona and tone instructions that is either **appended** to the coding system prompt (augmenting the agent's technical capabilities with communication preferences) or **replaces** the entire system prompt (pure persona mode). The system mirrors the skill and command loaders: three baked-in styles are always available, and custom styles can be added as markdown files with frontmatter under project and home convention directories.

This design solves a real usability problem. Different tasks call for different communication styles: a learning session benefits from collaborative pauses and `TODO(human)` stubs, while a quick fix needs terse code-and-bullets output. Rather than forcing users to manually edit the system prompt for each context switch, the output style system provides a lightweight selector that modifies the prompt on the fly.

---

## OutputStyle Struct

```go
type OutputStyle struct {
    Name        string // the selector key (case-insensitive)
    Description string // one-line summary for listings
    Body        string // the prompt text to apply
    KeepCoding  bool   // true: append to coding prompt; false: replace it
    Builtin     bool   // true for the three baked-in styles
    Path        string // file the style loaded from ("" for built-ins)
}
```

### KeepCoding Semantics

The `KeepCoding` flag is the central design decision of the system. It controls how the style interacts with the base system prompt:

- **`KeepCoding = true`** (augment mode): The style body is appended to the end of the coding system prompt. The agent retains all its technical instructions (tool usage rules, safety constraints, code intelligence) while adopting the communication preferences specified in the style. This is the default for both built-in and custom styles.

- **`KeepCoding = false`** (pure persona mode): The style body **replaces** the entire system prompt. The agent becomes a pure persona with no coding instructions — useful for creative writing, conversation, or any scenario where the coding tools are irrelevant. The user takes full responsibility for the prompt content in this mode, as the agent loses its default guardrails.

This two-mode design prevents the "prompt sprawl" problem where appending more and more style instructions eventually dilutes the core coding capabilities. A style that truly wants to redefine the agent's behavior can use `KeepCoding: false` to start fresh, while most styles simply layer communication preferences on top of the existing prompt.

### Path Field

The `Path` field records the filesystem location a custom style was loaded from. It is empty for built-in styles. This is useful for debugging (which file defined this style?) and for the UI (showing a "custom" vs. "builtin" badge).

---

## Built-in Styles

Three styles are always available without any configuration:

### 1. Explanatory

```
Name:        "explanatory"
Description: "Explain non-obvious implementation choices as you go"
KeepCoding:  true
```

The explanatory style surfaces the reasoning behind non-obvious choices as the agent works. After a substantive change, it adds a short `## Insight` note covering the key trade-off or why an alternative was rejected. The goal is to teach the *why*, not just the *what* — the user sees not only what was done but why it was the right approach.

This style is ideal for users who are learning a codebase, unfamiliar with a technology, or who want to understand the agent's decision-making process. The `## Insight` notes are deliberately brief — a sentence or two, not an essay — so they don't bloat the output.

### 2. Learning

```
Name:        "learning"
Description: "Collaborate and leave TODO(human) stubs for the user to complete"
KeepCoding:  true
```

The learning style makes the agent a collaborator rather than a doer. When a meaningful implementation decision comes up, it pauses and asks the user to make the call (leveraging the Ask Tool). For the most instructive pieces of code, the agent writes the surrounding structure but leaves a clearly-marked `TODO(human)` stub with a one-line description for the user to implement themselves.

This style is designed for educational contexts: tutorials, pair programming with a learner, or situations where the user wants hands-on practice rather than a fully automated solution. The `TODO(human)` stubs are small and focused — just enough to require understanding, not enough to be overwhelming.

### 3. Concise

```
Name:        "concise"
Description: "Terse replies: minimal prose, code and bullets only"
KeepCoding:  true
```

The concise style strips the output to its minimum: no preamble, no postamble, no restating the request. Code and short bullet points are preferred over paragraphs; answers use the fewest words that are still clear. This is the style for experienced users who know what they want and just need the agent to do it without commentary.

---

## Custom Styles

### File Format

Custom styles are markdown files with optional YAML frontmatter, stored under convention directories:

```markdown
---
name: friendly
description: Warm, conversational tone with emoji
keep-coding-instructions: true
---

Communication style — Friendly: use a warm, conversational tone throughout.
Address the user by name when known. Use emoji sparingly for emphasis
(not as decoration). Prefer paragraphs over bullets for explanations.
```

The frontmatter fields are:

- **`name`** (optional): The style selector name. If omitted, the filename stem is used (e.g., `friendly.md` → name `"friendly"`).
- **`description`** (optional): One-line summary for the `/output-style` listing.
- **`keep-coding-instructions`** (optional, default `true`): Controls whether the style augments or replaces the system prompt. Accepts `"false"`, `"no"`, `"0"`, or `"off"` to disable (switching to pure persona mode).

The body (everything after the frontmatter) is the prompt text. If the body is empty after trimming whitespace, the file is treated as malformed and skipped.

### Discovery Directories

Custom styles are discovered under the same convention directories used for commands and skills:

```go
var conventionDirs = []string{".reasonix", ".agents", ".agent", ".claude"}
```

Within each convention directory, styles are looked up in an `output-styles/` subdirectory. The `Dirs` function returns the search paths in load order:

```go
func Dirs() []string
```

The order is: **home convention dirs** (loaded first), then **project convention dirs** (loaded second). Within each set, directories are processed in reverse order of `conventionDirs` (i.e., `.reasonix` is checked after `.claude`). The "later wins" rule means that a project-level `.reasonix/output-styles/concise.md` overrides the built-in "concise" style.

### parseFile

The `parseFile` function loads one markdown file and returns an `OutputStyle`:

```go
func parseFile(path string) (OutputStyle, bool)
```

It reads the file, splits frontmatter from body using `frontmatter.Split`, extracts the `name` (falling back to the filename stem), determines `KeepCoding` from the `keep-coding-instructions` frontmatter field, and returns the style. If the file cannot be read, has an empty body, or is otherwise malformed, `ok` is `false` and the file is silently skipped.

---

## List & Resolve

### List

```go
func List(dirs []string) []OutputStyle
```

Returns every available style — built-ins plus custom markdown files — deduplicated by lowercased name, with custom files overriding built-ins of the same name. The result is sorted alphabetically by name.

The deduplication works by building a map keyed by `strings.ToLower(st.Name)`. Built-ins are inserted first, then custom files are processed directory by directory. If a custom file has the same lowercased name as a built-in, it replaces the built-in entry in the map. This means a user can create `.reasonix/output-styles/concise.md` to redefine the "concise" style without modifying the binary.

### Resolve

```go
func Resolve(name string, dirs []string) (OutputStyle, bool)
```

Finds the style with the given name (case-insensitive lookup). An empty name or the string `"default"` returns `ok = false`, meaning no style should be applied — the system prompt remains unmodified. This is how the "reset to default" operation works: `/output-style default` resolves to no style.

The function calls `List` internally, so it benefits from the same deduplication and override logic. If a custom style shadows a built-in, `Resolve` returns the custom version.

---

## Apply

```go
func Apply(base string, st OutputStyle) string
```

Folds a style into a base system prompt according to the `KeepCoding` flag:

1. **Empty body**: If the style's body is empty (after trimming whitespace), the base prompt is returned unchanged. This prevents a misconfigured style from accidentally blanking the system prompt.

2. **`KeepCoding = false`**: The style body replaces the base prompt entirely. The agent becomes a pure persona.

3. **`KeepCoding = true`**: The style body is appended to the base prompt with a double-newline separator:
   ```
   <base system prompt>

   <style body>
   ```

4. **Empty base**: If the base prompt is empty but the style body is non-empty, the style body becomes the entire prompt regardless of `KeepCoding`. This handles the edge case where the system prompt hasn't been initialized yet.

The `Apply` function is pure — it takes two strings and returns a string, with no side effects or state mutations. This makes it easy to test and reason about.

---

## DescribeList

```go
func DescribeList(styles []OutputStyle, active string) string
```

Renders the available styles as a short listing for the `/output-style` slash command. The currently active style is marked with `*`:

```
  concise (builtin) — Terse replies: minimal prose, code and bullets only
* explanatory (builtin) — Explain non-obvious implementation choices as you go
  learning (builtin) — Collaborate and leave TODO(human) stubs for the user to complete
  friendly (custom) — Warm, conversational tone with emoji
```

Each line shows: the active marker (`* ` or `  `), the style name, the scope (`builtin` or `custom`), an em-dash, and the description. The `active` parameter is matched case-insensitively against each style's name.

The "builtin" vs. "custom" badge comes from the `Builtin` field, which is `true` for the three baked-in styles and `false` for any file-loaded style — even one that overrides a built-in by name.

---

## Integration with the Agent Loop

When the agent initializes a turn, it calls `Resolve` with the configured output style name. If a style is found, `Apply` folds it into the base system prompt before the prompt is sent to the LLM. The modified prompt is used for that turn only — the base prompt is never mutated. This means switching styles is instant and reversible: the next turn can use a different style without any cleanup.

The style selector is typically set via the `/output-style` slash command, which updates the session's configuration and takes effect on the next turn. The desktop app's settings panel may also expose a dropdown that calls `Resolve` and `Apply` directly.
