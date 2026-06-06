# Inspect & Capability Projection

**Source:** `internal/inspect/inspect.go`

## Overview

The inspect package is a read-only capability projection layer that transforms a running agent's live runtime state — its configured providers, available tools, connected MCP servers, exposed prompts and resources, and loaded slash commands — into flat, JSON-serializable structs that any front-end can render directly without understanding the internal architecture. Two consumers currently drive off this projection: the CLI's `/mcp` slash command (which lists server status and tool surfaces in the terminal) and the desktop app's settings panel (which populates provider cards, tool lists, and server status indicators).

The critical design invariant is **zero mutation**: every function in this package takes already-built runtime objects as input and returns a pure view. The only I/O performed is reading the environment to check whether an API key is present (for the `KeyReady` field) — and even that is a boolean check that never exposes the secret value itself. This makes the projection safe to call from any thread at any time without locks, side effects, or risk of corrupting the agent's state.

---

## Snapshot

The `Snapshot` struct is the top-level bundle that gathers every capability surface into a single object so a front-end can populate its entire UI in one call:

```go
type Snapshot struct {
    DefaultModel string         `json:"default_model"`
    Providers    []ProviderInfo `json:"providers"`
    Tools        []ToolInfo     `json:"tools"`
    Servers      []ServerInfo   `json:"servers"`
    Prompts      []PromptInfo   `json:"prompts"`
    Resources    []ResourceInfo `json:"resources"`
    Commands     []CommandInfo  `json:"commands"`
}
```

The `Capabilities` function builds a full `Snapshot` from the four core runtime objects:

```go
func Capabilities(cfg *config.Config, reg *tool.Registry, host *plugin.Host, cmds []command.Command) Snapshot
```

Any input may be `nil` or empty; the corresponding slice in the snapshot is then `nil` rather than an empty array, which gives front-ends a clear signal that the feature category is unavailable (not just empty). The `DefaultModel` is populated from the config when present.

This function is the single integration point — the CLI and desktop both call `Capabilities` with their live objects and serialize the result for display. No other code path needs to understand the full surface; the projection is the canonical source of truth for "what can this agent do right now."

---

## ProviderInfo

Each configured model provider is projected as a `ProviderInfo`:

```go
type ProviderInfo struct {
    Name          string       `json:"name"`
    Kind          string       `json:"kind"`
    Model         string       `json:"model"`
    BaseURL       string       `json:"base_url"`
    APIKeyEnv     string       `json:"api_key_env"`
    KeyReady      bool         `json:"key_ready"`
    ContextWindow int          `json:"context_window"`
    IsDefault     bool         `json:"is_default"`
    Pricing       *PricingInfo `json:"pricing,omitempty"`
}
```

### Key Design Decisions

- **`KeyReady` instead of `APIKey`**: The projection never exposes the actual API key value. Instead, it reports whether the environment variable referenced by `APIKeyEnv` is currently set and non-empty. A settings screen can show a green dot (key present) or amber dot (key missing) without ever handling the secret. This is computed by calling `e.APIKey()` on the config entry, which reads the environment variable — the boolean result is all that reaches the front-end.

- **`IsDefault`**: Marked true for the provider whose name matches `cfg.DefaultModel`. Only one provider in the list should have this flag set; it tells the UI which model the agent will use unless the user switches.

- **`ContextWindow`**: The configured token limit for this provider's model. A zero value means the window size is unknown or unconfigured; the UI can show "unknown" rather than "0".

### PricingInfo

When a provider has pricing configured, it is included as a `PricingInfo`:

```go
type PricingInfo struct {
    CacheHit float64 `json:"cache_hit"`
    Input    float64 `json:"input"`
    Output   float64 `json:"output"`
    Currency string  `json:"currency"`
}
```

The `Currency` field comes from `p.Symbol()` on the provider's pricing object, so it is a human-readable string like "USD" rather than an opaque code. The three rate fields are per-million-token costs. `Pricing` is omitted (`nil`) when no pricing is configured for a provider — the UI can then show "pricing not available" rather than zero-dollar rates.

The `Providers` function iterates `cfg.Providers` and projects each entry. If `cfg` is `nil`, it returns `nil` (not an empty slice), preserving the "unavailable" vs. "available but empty" distinction.

---

## ToolInfo

Each available tool is projected as a `ToolInfo`:

```go
type ToolInfo struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    ReadOnly    bool            `json:"read_only"`
    Previewable bool            `json:"previewable"`
    Source      string          `json:"source"`
    Schema      json.RawMessage `json:"schema,omitempty"`
}
```

### Field Details

- **`ReadOnly`**: Copied from `t.ReadOnly()`. Read-only tools never need user approval and are available in plan mode; the UI can display them with a distinct visual treatment (e.g., a shield icon).

- **`Previewable`**: Determined by whether the tool implements the `tool.Previewer` interface — specifically, the file-writer tools (`write_file`, `edit_file`, `multi_edit`, `delete_range`, `delete_symbol`) that can show a diff before the user approves. When `Previewable` is true, the UI knows it can render a diff view in the approval modal; when false, no preview is available and the user must approve blindly.

- **`Source`**: Either `"builtin"` or `"mcp:<server>"`. This is computed by the `toolSource` helper (see below).

- **`Schema`**: The raw JSON Schema for the tool's parameters, as returned by `t.Schema()`. The front-end can use this to render structured argument forms or validate user input before submission.

### Tool Source Classification

The `toolSource` function classifies tools by their naming convention:

```go
func toolSource(name string) string
```

- If the name matches the `mcp__<server>__<tool>` pattern (detected by `tool.SplitMCPName`), the source is `"mcp:<server>"`. For example, `mcp__github__create_issue` → `"mcp:github"`.
- If the name carries the `mcp__` prefix but is malformed (missing a part), the source is `"mcp"` — a catch-all for improperly named MCP tools.
- Otherwise, the tool is a compiled-in `"builtin"`.

This classification tells the UI whether a tool comes from the agent's core binary or from an external MCP server, which matters for status display (MCP tools may become unavailable if the server disconnects) and for security indicators (MCP tools run external code).

### Tools Function

```go
func Tools(reg *tool.Registry) []ToolInfo
```

Projects a runtime registry in its display order. The order is determined by `reg.Names()`, which returns tools in registration order. The `Previewable` check is done via a type assertion:

```go
_, previewable := t.(tool.Previewer)
```

If the registry is `nil`, the function returns `nil`. If a tool name appears in `Names()` but `reg.Get` returns false (a race with hot-reloading), that tool is silently skipped.

---

## ServerInfo

Each connected MCP server is projected as a `ServerInfo`:

```go
type ServerInfo struct {
    Name      string `json:"name"`
    Transport string `json:"transport"`
    Tools     int    `json:"tools"`
    Prompts   int    `json:"prompts"`
    Resources int    `json:"resources"`
}
```

The `Transport` field indicates how the server is connected — typically `"stdio"` or `"sse"`. The three count fields (`Tools`, `Prompts`, `Resources`) give the front-end a quick summary of what each server exposes without listing every item. The CLI's `/mcp` command uses these counts to show a compact server line, while the desktop settings panel can expand them into full lists on click.

The `Servers` function delegates to `host.Servers()`, which returns the current status of all connected servers. If the host is `nil` (no plugin system initialized), the function returns `nil`.

---

## PromptInfo

MCP prompts are surfaced as slash commands and projected as `PromptInfo`:

```go
type PromptInfo struct {
    Name        string          `json:"name"`
    Server      string          `json:"server"`
    Description string          `json:"description"`
    Args        []PromptArgInfo `json:"args,omitempty"`
}
```

- **`Name`**: The full `mcp__<server>__<prompt>` command body (without a leading slash). This is the identifier the user types to invoke the prompt.
- **`Server`**: The MCP server that owns this prompt, so the UI can group prompts by source.
- **`Args`**: The declared arguments of the prompt, each with a `Name`, `Description`, and `Required` flag:

```go
type PromptArgInfo struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Required    bool   `json:"required"`
}
```

The front-end can use these argument declarations to render a form with labels, placeholders, and required-field indicators, rather than making the user guess the prompt's expected input format.

---

## ResourceInfo

MCP resources are referenceable data objects projected as `ResourceInfo`:

```go
type ResourceInfo struct {
    Server      string `json:"server"`
    URI         string `json:"uri"`
    Name        string `json:"name"`
    Description string `json:"description"`
    MimeType    string `json:"mime_type"`
}
```

Resources are referenced in messages as `@<server>:<uri>`. The projection provides enough metadata for the front-end to display a resource picker or autocomplete: the `Name` for display, `URI` for referencing, `Description` for context, and `MimeType` for preview rendering (e.g., showing a JSON resource differently from a text one).

---

## CommandInfo

Custom slash commands loaded from `.reasonix/commands` are projected as `CommandInfo`:

```go
type CommandInfo struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    ArgHint     string `json:"arg_hint"`
    Source      string `json:"source"`
}
```

- **`Name`**: The command name without a leading slash (e.g., `"review"` or `"git:commit"`).
- **`ArgHint`**: A short description of expected arguments, displayed in the slash menu as a usage hint.
- **`Source`**: Where the command was loaded from (file path or "builtin"), so the UI can indicate whether a command is user-defined or system-provided.

The `Commands` function returns `nil` for an empty list rather than an empty slice, consistent with the other projections.

---

## Consumption Patterns

### CLI `/mcp` Command

The CLI's `/mcp` slash command calls `Capabilities` (or the individual projection functions) and renders the result as formatted terminal output. Servers are listed with their transport and surface counts; tools show their name, source, and read-only/previewable flags; prompts and resources list their arguments and URIs.

### Desktop Settings Panel

The desktop app's settings panel (the `CapabilitiesPanel` component) calls `Capabilities` and renders the result as a scrollable, filterable UI. Provider cards show the key-ready status with colored dots, tool lists can be filtered by source (builtin/MCP), and server entries show live connection status. The projection's JSON tags ensure the data flows cleanly through the Wails bridge without custom serialization.

### Zero-Mutation Guarantee

Because every projection function takes already-built objects and returns a view, the projection can be called from any goroutine at any time without acquiring locks on the source objects. The only external dependency is `e.APIKey()` for key readiness, which reads an environment variable — a thread-safe operation on all major platforms. This makes the projection suitable for periodic refresh (e.g., polling server status) without the complexity of snapshot isolation.
