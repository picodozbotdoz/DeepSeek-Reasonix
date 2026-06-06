# MCP Advanced Features

This document covers three advanced subsystems within the MCP (Model Context Protocol) plugin layer: startup statistics and automatic demotion, authentication diagnostics, and resource reading. These features operate behind the scenes to make MCP server integration robust, secure, and informative.

---

## MCP Plugin Startup Stats & Auto-Demotion

**Source:** `internal/plugin/stats.go`, `internal/plugin/lazy.go`

### Overview

When DeepSeek-Reasonix boots, MCP servers must be initialized before their tools become available to the model. Some servers start quickly (tens of milliseconds), while others — particularly those that spawn heavy subprocesses or connect to remote endpoints — can take seconds. The startup stats system tracks per-plugin latency across sessions and automatically demotes chronically slow "eager" plugins to "lazy" loading, ensuring that a single slow server never blocks the entire boot sequence.

### StartupStats Persistence

Each plugin's startup history is persisted to a tiny JSON file at `<cacheDir>/mcp/<slug>.stats.json`. The `StartupStats` struct contains:

| Field | Type | Description |
|---|---|---|
| `Version` | `int` | On-disk format version (currently `1`). A version mismatch causes the file to be re-initialized from scratch rather than migrated. |
| `SamplesMs` | `[]int64` | Rolling window of startup durations in milliseconds, ordered oldest → newest. Bounded to `maxSamples` (20). |
| `LastSeen` | `time.Time` | Wall-clock time of the most recent sample, for future stale-data pruning. |

### RecordStartup

`RecordStartup(name, dur)` appends one startup duration sample to the plugin's stats file. The function:

1. Resolves the stats file path via `statsPath(name)`, which returns `<cacheDir>/mcp/<slug>.stats.json`. If no cache directory is resolvable, the call silently returns — telemetry must never block real work.
2. Loads existing stats via `loadStats(path)`. Missing or corrupt files yield a zero-value `StartupStats`.
3. Checks the version field. If it doesn't match `statsVersion`, the entire record is reset — losing 20 samples is cheaper than maintaining migration code for a telemetry side-channel.
4. Appends the new sample (in milliseconds, clamped to ≥ 0) to `SamplesMs`.
5. Trims the slice from the front when it exceeds `maxSamples` (20), so older samples leave first.
6. Updates `LastSeen` to `time.Now()`.
7. Writes atomically via `writeStatsAtomic` (tmpfile + `os.Rename`), ensuring concurrent readers see either the old or new content, never a half-written file.

All I/O errors are logged with `slog.Warn` and silently dropped. Startup must never fail because telemetry cannot persist.

### Recommend: Auto-Demotion Logic

`Recommend(name, budget, demoteAfter)` inspects the rolling window and decides whether a plugin should be demoted to "lazy" this session:

- **`budget == 0`**: The check is disabled; returns no-demote.
- **`demoteAfter <= 0`**: Falls back to `defaultDemoteAfter` (3), meaning three consecutive slow startups trigger demotion.
- **Missing/empty stats**: No demote — a fresh plugin gets the benefit of the doubt.
- **Fewer samples than `demoteAfter`**: Not enough data; no demote.
- **Consecutive over-budget check**: Inspects the last `demoteAfter` samples. If every one of them meets or exceeds the budget threshold, `Demote` is set to `true` and a human-readable `Reason` is generated (e.g., `plugin "heavy-server" has been slow 3 startups in a row (last 5200ms, budget 3000ms); demoting to lazy this session`).

The `P99` field is also computed (the 99th-percentile sample duration) and included in the `Recommendation` for UI/notice display. With small windows (n ≤ 20), p99 effectively becomes "the slowest sample we have."

### Three-Tier Startup System

MCP plugins operate in one of three startup tiers:

| Tier | Behavior | Boot Impact |
|---|---|---|
| **eager** | Blocks at boot until the handshake completes. | Slow servers delay the entire startup. |
| **lazy** | Defers spawn to the first model call. | Zero boot impact; first call incurs handshake latency. |
| **background** | Kicks spawn at boot (non-blocking), so the handshake may complete before the first call. | Minimal boot impact; best-case: ready by first use. |

The auto-demotion system only demotes from "eager" to "lazy." Background and lazy tiers are unaffected.

### lazySpawn State Machine

The `lazySpawn` struct manages the deferred initialization lifecycle for lazy and background plugins:

```
idle → inFlight → ready
idle → inFlight → failed
```

All transitions are gated by `lazySpawn.mu` (a `sync.Mutex`), ensuring that only one goroutine runs the handshake even when multiple `Execute` calls race on first use. The state machine fields include:

- `real map[string]tool.Tool` — namespaced name → real tool, populated on success.
- `spawnErr` — stored error when the handshake fails.
- `swapped` — whether the real tools have been installed into the registry.
- `removePrefix` — set for cache-miss placeholders so `trySwap` can drop the single `<server>__connect` stub before re-registering real tools.

### lazyTool: Placeholder with Cached Schema or Stub

`lazyTool` implements `tool.Tool` and acts as a placeholder backed by a shared `lazySpawn`. The model sees either:

- **Cache hit** (`hasCache = true`): The tool carries a cached schema from a previous session, so the model can pass real arguments. On first `Execute`, the handshake runs **synchronously**, and the call forwards through to the real tool in the same turn.
- **Cache miss** (`hasCache = false`): The tool has an empty schema (returns `{"type":"object"}`). On first `Execute`, the handshake is kicked **asynchronously** and the model is told to retry on the next turn, by which time the swap will have installed the real tools with real schemas.

The `Execute` method handles all four spawn states:

1. **`spawnReady`**: Forwards to the real tool immediately (or catches up on a background spawn that finished while idle).
2. **`spawnFailed`**: Returns the stored error.
3. **`spawnInFlight`**: Returns a "still initializing — call again next turn" error.
4. **`spawnIdle`**:
   - Cache-miss: kicks async spawn, asks for retry.
   - Cache-hit: runs the handshake synchronously and forwards through.

### LazyToolset

`LazyToolset(spec, cs, host, reg, sessionCtx, kick)` returns the placeholder tools for one lazy/background spec:

- **With cached schema (`cs != nil`)**: Returns one `lazyTool` per cached tool, each carrying the real schema and `hasCache = true`.
- **Without cached schema (`cs == nil`)**: Returns a single stub named `mcp__<server>__connect` with a descriptive prompt: "Connect MCP server — call this once to drive the handshake; the server's real tools become available on the next turn."
- **`kick = true`** (background tier): Also fires `shared.kick()` to start the spawn immediately, warming up without waiting for the first model call.

The `sessionCtx` must outlive any single `Execute` call (typically the controller's `PluginCtx`), because a turn-scoped context would kill the stdio child process between turns.

---

## MCP Auth Diagnostics

**Source:** `internal/mcpdiag/auth.go`

### Overview

MCP servers that connect over HTTP transports (streamable-http, SSE) may require authentication. When a server fails to start or returns auth errors, the diagnostics subsystem determines whether the failure is due to missing credentials and surfaces actionable information to the user. This module is deliberately kept separate from the plugin system so it can be used by CLI diagnostics, the desktop UI, and the boot path without pulling in heavy dependencies.

### AuthDiagnosis

The `AuthDiagnosis` struct describes the authentication state of an MCP server:

| Field | Type | Description |
|---|---|---|
| `Status` | `string` | One of `"none"`, `"possible"`, or `"required"`. |
| `URL` | `string` | The remote server URL, when applicable. |

### DiagnoseAuth

`DiagnoseAuth(transport, status, errText, url, authConfigured)` performs a multi-step diagnosis:

1. **Auth failure detected**: If `IsAuthFailure(errText)` returns true, the diagnosis is `AuthRequired` with the remote auth URL.
2. **Auth already configured**: If `authConfigured` is true, the failure is not auth-related → `AuthNone`.
3. **Not a remote transport**: Non-HTTP transports (stdio) don't have auth → `AuthNone`.
4. **Non-HTTP URL**: URLs that don't look like `http://` or `https://` → `AuthNone`.
5. **Non-empty error text**: If there's an error but it's not an auth failure, assume the issue is something else → `AuthNone`.
6. **Deferred/initializing remote server without auth config**: Status values like `"deferred"`, `"initializing"`, or `"disabled"` suggest the server hasn't fully started yet and might need auth → `AuthPossible` with the URL.
7. **Connected or failed status**: The server has already attempted and either succeeded or failed for non-auth reasons → `AuthNone`.

### IsAuthFailure

`IsAuthFailure(errText)` checks the error text (case-insensitive) for common authentication failure indicators:

- HTTP status codes: `401`, `403`
- Literal strings: `unauthorized`, `forbidden`, `invalid token`, `login required`, `authentication`, `not authenticated`

### HasAuthConfig

`HasAuthConfig(headers, env, url)` inspects the server configuration for authentication material:

1. **Headers**: Checks each header key with `isAuthish` (contains `auth`, `token`, `secret`, `credential`, `api_key`, `api-key`, `apikey`, `cookie`) and each value with `containsExplicitAuthMaterial` (contains `access_token`, `id_token`, `refresh_token`, `api_key`, `api-key`, `apikey`, `bearer `).
2. **URL**: Checks the URL for auth material via `containsAuthMaterial` (template variables `${...}` or explicit auth tokens in the URL string).
3. **Environment variables**: Same `isAuthish` key check and `containsAuthMaterial` value check.

### ClearAuthConfig

`ClearAuthConfig(headers, env, rawURL)` strips auth-related configuration for safe display in diagnostics UI:

- Removes headers whose keys are auth-like or whose values contain explicit auth material.
- Removes environment variables with the same criteria.
- Strips auth-related query parameters from URLs (keys matching `isAuthQueryKey`: `key`, or any auth-like key).
- Returns the cleaned maps and URL, plus a boolean indicating whether any changes were made.

### IsRemoteTransport

`IsRemoteTransport(transport)` returns true for HTTP-based transports: `"http"`, `"streamable-http"`, `"sse"`. These are the transports where authentication is relevant — stdio servers run locally and don't need auth.

---

## MCP Resource Reading

**Source:** `internal/plugin/resources.go`

### Overview

The MCP protocol allows servers to expose **resources** — static or semi-static content that can be referenced in chat messages. Resources are distinct from tools: they provide data (like documentation, configuration files, or database schemas) rather than executable actions. DeepSeek-Reasonix integrates MCP resources so users can reference them with `@server:uri` syntax in their messages, and the content is automatically fetched and prepended to the prompt sent to the model.

### Resource Struct

The `Resource` struct represents an MCP resource exposed by a server:

| Field | Type | Description |
|---|---|---|
| `Server` | `string` | The owning server name. |
| `URI` | `string` | The canonical resource URI (e.g., `file://README.md`). |
| `Name` | `string` | Human-readable label for display. |
| `Description` | `string` | What the resource contains. |
| `MimeType` | `string` | MIME type of the resource content. |

### Referencing Resources in Chat

Resources are referenced using the `@<server>:<uri>` convention. For example:

```
@docs:file://README.md
```

When the controller encounters such a reference, it resolves the server name and URI, fetches the resource content, and prepends it to the user's message. This gives the model access to external documentation, code snippets, or configuration data without requiring the user to manually paste large blocks of text.

### listResources

`listResources(ctx)` calls the MCP `resources/list` method on the server. It sends a `resources/list` JSON-RPC call with an empty parameters object, then decodes the response into an array of resource objects. Each resource's `URI`, `Name`, `Description`, and `MimeType` are extracted and wrapped in a `Resource` struct with the owning server's name attached. Errors in decoding are wrapped with the server name for clear diagnostics.

### readResource

`readResource(ctx, uri)` fetches a resource's content by calling the MCP `resources/read` method with the specified URI. The response contains an array of content blocks, each of which may be:

- **Text content** (`text` field): Directly appended to the output.
- **Binary/blob content** (`blob` field): Noted with a placeholder message (e.g., `[binary resource file://image.png, image/png — 12345 base64 bytes omitted]`) rather than decoded. This design choice reflects the fact that DeepSeek-Reasonix is a coding agent that primarily consumes text — binary resources like images are acknowledged but not passed through to the model.

Multiple content blocks are concatenated with double-newline separators. If the server returns no content for a given URI, the function returns an empty string without error.

### Design Considerations

The resource reading system is intentionally simple:

- **No caching**: Resources are fetched on every reference, ensuring freshness. MCP servers are expected to handle caching internally if their resources are static.
- **No streaming**: The entire resource content is read into memory before being returned. This is appropriate for the typical use case (documentation snippets, configuration files) where resources are small.
- **Text-first**: Binary resources are acknowledged but not decoded, keeping the agent's context window focused on textual information.
