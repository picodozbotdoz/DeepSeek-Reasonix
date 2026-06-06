# Diagnostics & Billing

This document covers two user-facing diagnostic features in DeepSeek-Reasonix: the doctor diagnostics system for comprehensive issue reporting, and the billing/wallet balance system for monitoring account credit.

---

## Doctor Diagnostics

**Source:** `internal/doctor/report.go`

The doctor diagnostics system collects comprehensive, privacy-redacted information about the local Reasonix installation for issue filing and troubleshooting. It is invoked via the `reasonix doctor` CLI command and produces a human-readable report covering every subsystem that could affect the agent's behavior.

### Report Structure

The `Report` struct contains the following sections, each designed to answer a specific class of "what's different about your setup?" questions:

| Section | Type | Purpose |
|---------|------|---------|
| Version | `string` | The Reasonix build version, essential for reproducing bugs against the correct code |
| OS/Arch | `string` | Operating system and CPU architecture (e.g., `darwin/arm64`, `windows/amd64`) |
| CWD | `string` | Current working directory (redacted) — reveals whether the user is in a project root |
| Config | `ConfigReport` | Configuration file paths and default model |
| Providers | `[]ProviderReport` | All configured LLM providers with their status |
| Plugins | `[]PluginReport` | MCP plugins with their transport and auto-start status |
| Codegraph | `CodegraphReport` | Code intelligence engine status and version |
| LSP | `LSPReport` | Language Server Protocol configuration |
| Sessions | `SessionsReport` | Saved session count and total size |
| Sandbox | `SandboxReport` | OS sandbox availability and configuration |
| Network | `NetworkReport` | Proxy settings and connectivity |
| Permission | `PermissionReport` | Permission mode and rule counts |
| Warnings | `[]string` | Top-level warnings (e.g., config parse failures) |

### ProviderReport

Each configured provider is reported with:

- **Name**: The provider's configured name (e.g., "deepseek", "openai")
- **Kind**: The provider type (e.g., "openai", "anthropic")
- **BaseURLHost**: Only the hostname:port of the base URL — the full URL path is omitted for privacy (it might contain internal network details)
- **Model**: The default model for this provider
- **KeyPresent**: A boolean — `true` if the API key environment variable is set, `false` if missing. **The key value itself is never included**, not even a masked version. This is the most critical privacy measure: leaked API keys from issue reports are a real security risk.
- **IsDefault**: Whether this provider is the default
- **ContextWindow**: The configured context window size

### Privacy: redactHome

The `redactHome` function rewrites any path under the user's home directory to start with `~`. For example, `/home/alice/.config/reasonix/credentials.env` becomes `~/.config/reasonix/credentials.env`. This prevents the user's account name from appearing in shared diagnostics reports. Paths outside the home directory (e.g., `/usr/local/bin/codegraph`) are returned unchanged, since they don't contain personal information.

The `redactHomeAll` helper applies `redactHome` to a slice of paths, used for lists like `WriteRoots`.

### SandboxReport

The sandbox report includes an `Available` boolean that indicates whether an OS-level sandbox (bubblewrap on Linux, seatbelt on macOS) is actually present on the host. This is critical for diagnosing why "enforce" mode might be running unconfined: on Windows, for example, there is no OS sandbox, so `Available` is always `false` even when the config says `bash = "enforce"`. The `RenderText` function appends a parenthetical warning when this mismatch is detected: "(inactive: no OS sandbox on this host — bash runs unconfined)".

### PluginReport

Each plugin reports its name, transport type (stdio or http), auto-start status, and a redacted target. For HTTP plugins, `pluginTarget` returns only the hostname from the URL. For stdio plugins, it returns only the basename of the command (e.g., `npx` rather than `/usr/local/npx`), preventing internal path details from leaking.

### RenderText

The `RenderText` function produces a human-readable text format with section headers and indented key-value pairs. It is designed to be copy-pasted directly into a GitHub issue. Key design choices:

- **Warnings at the top**: Configuration warnings (like a failed TOML parse) are shown immediately after the basic version/OS info, not buried in their own section where they might be missed. A warning that the config fell back to defaults explains every subsequent "wrong" value.
- **Provider key status**: Shown as `key:present` or `key:missing` with a `default` marker, making it immediately obvious whether a provider is functional.
- **Sandbox mismatch warning**: Inline in the sandbox section, not a separate warning, because it's directly relevant to interpreting the sandbox mode.

### collectSessions

The session collection function walks the session directory (typically `~/.local/share/reasonix/sessions/`) and counts the number of `.jsonl` session files along with their total size on disk. This helps diagnose storage issues and gives a sense of how heavily the installation has been used. If the session directory is missing or unreadable, the error is recorded in the `Error` field rather than causing the entire report to fail.

---

## Billing / Wallet Balance

**Source:** `internal/billing/balance.go`

The billing package queries a provider's wallet balance endpoint and normalizes the response for display in the status line. It is currently implemented for the **DeepSeek-style** `GET /user/balance` endpoint, which is the only documented balance API shape.

### Balance Struct

The `Balance` struct contains:

- **Available** (`bool`): Whether the provider reports the account can still serve API calls. This maps to the `is_available` field in the DeepSeek response.
- **Infos** (`[]Info`): One entry per currency the provider returns. Each `Info` contains:
  - `Currency` — ISO currency code (e.g., "CNY", "USD")
  - `TotalBalance` — Total available balance (granted + topped-up), as a string to preserve exact decimal representation
  - `GrantedBalance` — Unexpired promotional credit
  - `ToppedUpBalance` — User-paid credit

### Fetch Function

`Fetch` queries the balance endpoint with a Bearer API key and returns the normalized balance. Key behaviors:

- **Empty URL → (nil, nil)**: If the provider has no `balance_url` configured, `Fetch` returns `nil, nil` — "not configured", not an error. This is intentional: most providers don't offer a balance endpoint, and callers should simply omit the readout rather than showing an error.
- **12-second timeout**: The HTTP client has a 12-second timeout so a slow or unreachable balance endpoint can't hang the status line rendering. The per-call `context.Context` still cancels the request on shutdown.
- **1 KiB response limit**: The response body is read with a 16 KiB limit to prevent a misbehaving endpoint from consuming unbounded memory.

### FetchWithClient

`FetchWithClient` is the testable variant that accepts a custom `*http.Client`. A nil client falls back to the package-default 12-second client. This enables deterministic unit testing with httptest servers.

### Currency Symbol Mapping

The `symbol` function maps ISO currency codes to compact display symbols:

| Currency | Symbol |
|----------|--------|
| CNY, RMB | ¥ |
| USD | $ |
| Other | `CODE ` (e.g., "EUR 12.00") |

### Display Method

The `Display` method renders the primary balance compactly, e.g., "¥110.00". It prefers **CNY** (the most common currency for DeepSeek), then falls back to the first currency reported by the provider. An empty string is returned when there's nothing to show (nil Balance or empty Infos), so the caller can simply check for non-empty rather than handling nil specially.

### Usage in the Status Line

The balance display is used by the status line in both the CLI (`internal/cli/statusline.go`) and the desktop app (`desktop/frontend/src/components/StatusBar.tsx`). It appears as a compact readout alongside the model name, giving users real-time visibility into their remaining credit without requiring a separate dashboard visit.
