# Evidence Ledger & Ask Tool

DeepSeek-Reasonix enforces agent integrity through two complementary subsystems that operate at the boundary between the LLM's decisions and the host runtime. The **Evidence Ledger** records every tool call as an immutable receipt so that completion claims are backed by observable proof, while the **Ask Tool** gives the model a structured, side-effect-free channel for soliciting genuine user decisions mid-task. Together they ensure the agent cannot declare work finished without evidence and cannot guess at decisions that rightfully belong to the user.

---

## Evidence Ledger System

**Source:** `internal/evidence/evidence.go`

### Overview

The Evidence Ledger is a turn-scoped, in-memory data structure that records every tool invocation as a structured `Receipt`. It is the backbone of the `complete_step` tool's verification logic: the model can only mark a todo step as "completed" when a successful tool call backs it up. The ledger is not serialized into prompts or session state — it exists solely in host memory for the duration of a single agent turn, and is reset between user turns.

This design prevents a subtle failure mode: without evidence tracking, an LLM can claim a step is done based on its own reasoning alone, even if no tool call actually performed the work. The ledger makes every such claim auditable and falsifiable.

### Receipt Struct

Every tool call produces a `Receipt` that captures the essential facts about what happened:

```go
type Receipt struct {
    ToolName string          // the tool that was invoked (e.g. "bash", "write_file")
    Args     json.RawMessage // the raw JSON arguments as sent by the model
    Success  bool            // whether the tool call completed without error
    Command  string          // extracted bash command (only for tool "bash")
    Step     string          // the step identifier (only for tool "complete_step")
    TodoStep *TodoStepMatch  // matched todo item (auto-populated for complete_step)
    Paths    []string        // file paths touched by the call
    Read     bool            // whether the call was classified as a read operation
    Write    bool            // whether the call was classified as a write operation
    Todos    []TodoItem      // the full todo list (only for tool "todo_write")
}
```

Key design choices:

- **Args is a deep copy.** The raw arguments are copied on `Record` so that later mutations of the original JSON do not retroactively alter the receipt. This ensures the ledger is a faithful snapshot of what the model actually sent.
- **Command and Step are trimmed.** Whitespace is normalized on ingress so that `"  go test  "` and `"go test"` are treated as the same command in matching queries.
- **Paths are normalized.** All file paths undergo normalization (see below) before storage.
- **Failed receipts are retained.** Unsuccessful tool calls are still recorded for auditability — `HasSuccessful*` matchers ignore them, but the full history is available for debugging and the `UnverifiedCompletedTodos` check.

### TodoStepMatch

When a `complete_step` receipt is recorded, the ledger automatically attempts to match the step argument against the most recent successful `todo_write` receipt:

```go
type TodoStepMatch struct {
    Found      bool   // whether a matching todo item was found
    Index      int    // 1-based position in the todo list
    Content    string // the todo item's content text
    Status     string // the todo item's status at the time of the last todo_write
    ActiveForm string // the active-form label, if any
}
```

This matching happens inside `Ledger.Record` — when a successful `complete_step` is recorded and its `TodoStep` field is nil, the ledger walks backwards through existing receipts to find the latest `todo_write` and resolves the step text against that todo list. The match is stored directly on the receipt so that subsequent queries (especially `UnverifiedCompletedTodos`) can use it without re-computing.

### Tool Classification

The ledger classifies every tool as a **writer**, a **reader**, or neither, based on its name:

**Writer tools** (set `Write = true`):
- `write_file`
- `edit_file`
- `multi_edit`
- `notebook_edit`
- `delete_range`
- `delete_symbol`

**Reader tools** (set `Read = true`):
- `read_file`
- `ls`
- `grep`

Additionally, if the `readOnly` flag is passed to `ReceiptFromToolCall` and the call has paths but is neither a known writer nor reader, it is classified as a read operation. This handles edge cases where a custom or MCP tool reports itself as read-only but operates on files.

### Path Normalization

File paths are normalized on ingestion to ensure consistent matching regardless of platform conventions:

```go
func normalizePath(p string) string {
    p = strings.TrimSpace(p)
    p = strings.ReplaceAll(p, `\`, "/")       // backslashes → forward slashes
    p = filepath.Clean(filepath.FromSlash(p))  // canonicalize (., .., double slashes)
    if runtime.GOOS == "windows" {
        p = strings.ToLower(p)                 // case-insensitive on Windows
    }
    return p
}
```

This means `C:\Users\Alice\code\main.go`, `c:/users/alice/code/main.go`, and `./code/main.go` (when CWD-appropriate) will all be treated as the same path on Windows. On Unix, paths remain case-sensitive. The normalization is applied to every path in every receipt at `Record` time, and also to the path arguments of query methods like `HasSuccessfulWrite`.

### ReceiptFromToolCall Factory

`ReceiptFromToolCall` is the primary factory function that constructs a `Receipt` from raw tool call data:

```go
func ReceiptFromToolCall(toolName string, args json.RawMessage, success bool, readOnly bool) Receipt
```

It parses the raw JSON arguments to extract structured fields:
- For `bash`: extracts the `command` field
- For `complete_step`: extracts the `step` field
- For `todo_write`: extracts the `todos` array
- For all tools: extracts paths from `path`, `file_path`, `notebook_path` (single) and `paths`, `file_paths` (array) fields

After extraction, it applies tool classification (writer/reader) and returns the fully populated receipt. This function is the single entry point that the agent loop uses to create receipts, ensuring consistent field extraction across all code paths.

### Query Methods

The ledger provides several query methods that power different parts of the verification system:

#### HasSuccessfulCommand

```go
func (l *Ledger) HasSuccessfulCommand(command string) bool
```

Returns true if any receipt in the current turn records a successful `bash` call with exactly the given command string. Used by the final readiness system to verify that required verification commands (e.g., `go build ./...`, `go test ./...`) have actually been executed.

#### HasSuccessfulCommandAfter

```go
func (l *Ledger) HasSuccessfulCommandAfter(command string, after int) bool
```

Same as `HasSuccessfulCommand`, but only searches receipts with index greater than `after`. This is used when the readiness system needs to verify that a command ran *after* the latest write operation — not just at any point in the turn.

#### HasSuccessfulWrite / HasSuccessfulReadOrWrite

```go
func (l *Ledger) HasSuccessfulWrite(paths []string) bool
func (l *Ledger) HasSuccessfulReadOrWrite(paths []string) bool
```

These check whether all specified paths have been touched by a successful writer (or reader/writer) tool call. The paths are normalized and deduplicated; every requested path must appear in at least one matching receipt. `HasSuccessfulWrite` is used to confirm that file modifications actually occurred, while `HasSuccessfulReadOrWrite` is a looser check that also counts read operations.

#### MatchLatestTodoStep

```go
func (l *Ledger) MatchLatestTodoStep(step string) (TodoStepMatch, bool)
```

Walks backwards through receipts to find the most recent successful `todo_write` and matches the step text against its todo list. Returns the match and whether a `todo_write` was found at all. The step can be either a 1-based numeric index (e.g., `"2"`, `"3."`) or a content/activeForm text match (case-insensitive).

#### IncompleteLatestTodos

```go
func (l *Ledger) IncompleteLatestTodos() ([]TodoStepMatch, bool)
```

Returns all non-completed items from the most recent successful `todo_write` receipt. This is used by the final readiness system to detect when the model is trying to finish while todo items remain pending or in-progress. The second return value indicates whether a baseline `todo_write` was found at all.

#### UnverifiedCompletedTodos

```go
func (l *Ledger) UnverifiedCompletedTodos(current []TodoItem) (missing []TodoStepMatch, hasBaseline bool)
```

This is the most sophisticated query. It detects "phantom completions" — todo items that are marked "completed" in the current list but have no corresponding successful `complete_step` receipt. It works by:

1. Finding the most recent successful `todo_write` receipt as a baseline
2. For each item in the current list that is "completed":
   - If it was already "completed" in the baseline, it's considered verified (it was completed in a prior turn)
   - If it has a matching successful `complete_step` receipt, it's verified
   - Otherwise, it's unverified — the model marked it completed without actually performing and confirming the work

The `hasBaseline` return value is important: when no prior `todo_write` exists (e.g., at the very start of a session), there's no baseline to compare against, and callers should fall back to a looser validation strategy rather than blocking all completions.

### Step Matching Logic

The `matchTodoStep` function resolves a step identifier against a todo list using two strategies:

1. **Numeric index**: If the step parses as an integer (with optional trailing period), it is used as a 1-based index into the todo list. `"2"` and `"2."` both match the second item.
2. **Text match**: If no numeric match is found, the step text is compared case-insensitively against each todo item's `Content` and `ActiveForm` fields. The first match wins.

Identity comparison (`sameTodoIdentity`) uses the same text matching: two todo items are considered the same if their `Content` or `ActiveForm` fields match case-insensitively.

### Ledger Lifecycle

```go
func NewLedger() *Ledger
func (l *Ledger) Reset()
```

A new ledger starts empty. `Reset` clears all receipts and is called between user turns — this ensures that evidence from one turn cannot be used to justify claims in the next. The ledger is guarded by a `sync.Mutex` so that concurrent tool calls (from parallel execution) can record receipts safely.

### Context Injection

The ledger follows the standard Go context-value injection pattern:

```go
func WithLedger(ctx context.Context, ledger *Ledger) context.Context
func FromContext(ctx context.Context) (*Ledger, bool)
```

`WithLedger` stamps a ledger onto a context; `FromContext` retrieves it. The agent loop injects the ledger into every tool call's context, allowing tools like `complete_step` to query the ledger for verification without needing a direct reference to the agent.

---

## Ask Tool

**Source:** `internal/agent/ask.go`

### Overview

The Ask Tool provides a structured multiple-choice question mechanism for genuine decision forks — situations where the model cannot resolve the correct path from the request, the code, or sensible defaults alone. Rather than guessing or asking in unstructured prose, the model presents the user with a clear set of options and receives their selections back as a tool result.

This is a deliberate design choice: unstructured prose questions are ambiguous, easy to miss in a long transcript, and provide no guarantee that the user actually answered. The structured approach produces a clean, machine-readable response that the model can reliably parse.

### Schema

The tool accepts 1–4 questions, each with 2–4 options:

```json
{
  "questions": [
    {
      "header": "Library",
      "question": "Which HTTP client library should we use?",
      "options": [
        { "label": "axios", "description": "Widely used, interceptors built in" },
        { "label": "fetch", "description": "No dependency, browser-native" },
        { "label": "got", "description": "Node.js, retry and streaming support" }
      ],
      "multiSelect": false
    }
  ]
}
```

- **header**: A very short label used as a tab title in the frontend UI (e.g., "Library", "Scope", "Approach"). This also serves as the key in the formatted answer summary.
- **question**: The full question text shown to the user.
- **options**: 2–4 choices, each with a required `label` and an optional `description`. The recommended option should be listed first.
- **multiSelect**: When true, the user can select more than one option.

### ReadOnly Guarantee

```go
func (*AskTool) ReadOnly() bool { return true }
```

The ask tool is marked read-only because it has no host-side effects — it never modifies files, runs commands, or changes state. This means it is always available, even in plan mode, where writer tools are blocked. Asking clarifying questions while planning is explicitly supported and encouraged.

### Headless Fallback

```go
_, _, asker, ok := CallContext(ctx)
if !ok || asker == nil {
    return "No interactive user is available to answer; proceed with your best judgment and state the assumption you made.", nil
}
```

When no interactive user is available (headless runs, CI pipelines, automated tests), the ask tool does not block. Instead, it returns a message telling the model to proceed with its best judgment and explicitly state the assumption it made. This ensures autonomous runs never deadlock waiting for a human who isn't there.

### Asker Interface & Frontend Rendering

The tool reaches the user through the `Asker` interface carried on the `CallContext`. The frontend renders the questions as a selectable card UI (the `AskCard` component in the desktop app), with each question as a tab and options as clickable choices. The user's selections are returned through the same `Asker` interface and formatted by `formatAnswers`.

### Validation

The tool performs several validation checks on its arguments:

1. At least one question is required
2. Every question must have non-empty text and at least two options
3. Every option must have a non-empty label
4. **Duplicate label detection**: Within a single question, no two options may share the same label. This is enforced by tracking seen labels and returning an error that identifies both the duplicate and the option it conflicts with:

```
question 1 option 3: duplicate label "fetch" also used by option 2
```

This prevents ambiguous answers where the model cannot tell which "fetch" the user selected.

### formatAnswers

The `formatAnswers` function renders the user's selections as a compact, model-facing summary:

```
The user answered:
- Library: axios
- Scope: full rewrite, tests only
```

Each line is keyed by the question's `header` (falling back to the full `Prompt` text if the header is empty). Multi-select answers are joined with commas. If a question received no answer, it shows "(no answer)". This format is designed to be unambiguous for the model — it can reliably parse which question each answer corresponds to without guessing.

### When to Use vs. Not Use

The tool's description includes explicit guidance:

**Use it for**: Genuine forks the model can't resolve — which library, which approach, scope decisions, architectural choices with no clear default.

**Don't use it for**: Decisions with an obvious default. If one option is clearly the right choice, the model should pick it and proceed rather than interrupting the user. The ask tool is for decisions that are *genuinely the user's to make*, not for decisions the model is unsure about out of laziness.
