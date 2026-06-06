# Internationalization & Localization

Reasonix supports multiple display languages through a structured i18n system. The system currently provides English and Chinese catalogs, with a detection mechanism that automatically selects the appropriate language based on environment variables or user configuration. This document covers the i18n architecture, how to add new languages, and the scope of translatable strings.

## Architecture

### The Messages Struct

All translatable strings are declared as fields of a single `Messages` struct in `internal/i18n/i18n.go`:

```go
type Messages struct {
    // welcome / status screen
    Subtitle        string
    WelcomeTitleFmt string
    NoConfigYet     string
    StartingChatFmt string
    // ... 200+ more fields
}
```

Key design decisions:

- **Plain fields** are printed verbatim (e.g., `Subtitle`)
- **`*Fmt` fields** are `fmt.Sprintf` format strings (e.g., `WelcomeTitleFmt` receives `%s` arguments)
- **No trailing newlines** — call sites add framing whitespace, so the same field works wherever it appears
- **English is the default** — any code path that runs before detection still has text

### Language Catalogs

Each language declares one `Messages` value in its own file:

| File | Language |
|------|----------|
| `messages_en.go` | English |
| `messages_zh.go` | Chinese |

The active catalog is stored in the package-level variable `M`:

```go
var M = English  // default
```

### Detection

```go
func DetectLanguage(override string) string
```

Priority order:

1. **Explicit override** (e.g., `cfg.Language` or `/language zh`)
2. **`REASONIX_LANG`** environment variable
3. **`LC_ALL`** environment variable
4. **`LC_MESSAGES`** environment variable
5. **`LANG`** environment variable
6. **Fallback**: `"en"` (English)

The function returns the resolved tag (`"en"`, `"zh"`) so callers can log or expose it.

### Normalization

Locale strings are normalized before matching:

- `zh_CN.UTF-8` → `"zh"`
- `zh-Hans-CN` → `"zh"`
- `Chinese (China)` → `"zh"`
- `en_US.UTF-8` → `"en"`
- `English` → `"en"`

The normalization handles common locale formats from different operating systems.

## Scope of Translations

### What's Translated

The i18n system covers the **CLI surface only**:

- Welcome and status screen text
- Init wizard prompts and labels
- Chat REPL banner and tips
- In-chat notices and messages
- Slash command descriptions
- Approval prompt text
- Status line labels (thinking, working, retrying, idle)
- Skill picker labels
- Provider error explanations (400, 401, 402, 422, 429, 500, 503)
- Model/memory/rewind notices
- Selection menu hints

### What's NOT Translated

- **System prompts**: Sent to the model in English for consistent behavior
- **Internal error wrappers**: Developer-facing messages stay English
- **Agent runtime telemetry**: Logs and diagnostics stay English
- **Tool names and schemas**: These are part of the model API contract

This boundary ensures that model behavior and developer logs are language-stable regardless of the display language.

## Provider Error Messages

The `Messages` struct includes localized explanations for common HTTP status codes:

```go
ProviderErrBadRequest          string // 400
ProviderErrAuth                string // 401
ProviderErrInsufficientBalance string // 402
ProviderErrUnprocessable       string // 422
ProviderErrRateLimited         string // 429
ProviderErrServer              string // 500
ProviderErrServerBusy          string // 503
```

The `ProviderStatusMessage` method maps a status code to its localized explanation:

```go
func (m Messages) ProviderStatusMessage(status int) string
```

This gives users actionable, localized guidance when a provider returns an error — far more helpful than raw HTTP status codes.

## Runtime Language Switching

### `/language` Command

The `/language` command allows switching languages during a session:

- `/language auto` — Use environment detection
- `/language en` — Force English
- `/language zh` — Force Chinese

The selection is persisted in configuration so it survives restarts.

### Language Header

The `/language` listing shows:

- Available languages with their tags
- Current language
- How to select one

All labels are drawn from the current catalog, so switching language immediately affects the display.

## Adding a New Language

### Step 1: Create the Catalog File

Create `internal/i18n/messages_<tag>.go` with a complete `Messages` value:

```go
package i18n

var French = Messages{
    Subtitle:        "Agent de codage alimenté par IA",
    WelcomeTitleFmt: "Bienvenue dans %s",
    // ... all fields
}
```

### Step 2: Register the Language

In `i18n.go`, add the language to the `setLanguage` function:

```go
func setLanguage(tag string) string {
    switch tag {
    case "zh":
        M = Chinese
        return "zh"
    case "fr":
        M = French
        return "fr"
    default:
        M = English
        return "en"
    }
}
```

Also update `normalize` to recognize the new locale:

```go
if strings.HasPrefix(s, "fr") || strings.Contains(s, "french") {
    return "fr"
}
```

### Step 3: Update Tests

The test `TestCatalogsComplete` uses reflection to verify that every field in `Messages` has a non-empty value in every catalog. Missing translations fail CI instead of surfacing as blank lines at runtime. This prevents drift between languages.

### Completeness Enforcement

The test iterates over every exported field of `Messages` and checks that each catalog provides a non-empty value. If a new field is added to `Messages` but not to a language catalog, the test fails with the field name and the missing language. This ensures that adding a field requires updating every `messages_*.go` file.

## Desktop App Integration

The desktop app shares the same `i18n.M` catalog as the CLI, so both frontends localize identically. Slash command descriptions, status labels, and approval prompts all draw from the same source. The desktop app's `i18n.tsx` bridge file provides TypeScript bindings for the catalog entries used in the React frontend.

## Design Rationale

### Why Not Go's `golang.org/x/text`?

The i18n system deliberately avoids `golang.org/x/text/message` and other standard i18n libraries:

- **Simplicity**: A flat struct of string fields is easy to understand, grep, and auto-complete
- **Compile-time safety**: Missing fields fail CI, not at runtime
- **Zero dependencies**: No external i18n framework to learn or update
- **Directness**: Call sites simply read `i18n.M.SomeField` — no lookup keys, no plural rules, no template engines

### Why Not Translation Files?

- **No runtime file I/O**: All strings are compiled into the binary
- **No parsing errors**: The Go compiler validates syntax
- **No missing key lookups**: The struct is the contract — if it compiles, all keys exist

The trade-off is that adding a field requires updating every language file, but the test suite catches this immediately, and the Go compiler provides a stronger guarantee than any runtime key-lookup system.
