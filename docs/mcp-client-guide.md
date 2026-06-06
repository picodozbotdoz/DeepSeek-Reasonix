# Reasonix as an MCP Client

Reasonix can connect to external MCP (Model Context Protocol) servers, discover their tools, prompts, and resources, and make them available to the agent at runtime. This allows Reasonix to extend its capabilities dynamically by integrating with any MCP-compatible server — file systems, databases, APIs, code analysis tools, and more.

This document covers everything you need to know about configuring, using, and troubleshooting Reasonix's MCP client functionality.

---

## Table of Contents

1. [Overview](#overview)
2. [Configuration](#configuration)
   - [reasonix.toml (Primary)](#reasonixtoml-primary)
   - [.mcp.json (Claude Code Compatible)](#mcpjson-claude-code-compatible)
   - [CLI Commands](#cli-commands)
   - [In-Chat Slash Commands](#in-chat-slash-commands)
   - [Config Source Priority](#config-source-priority)
3. [Transport Types](#transport-types)
   - [Stdio Transport](#stdio-transport)
   - [Streamable HTTP Transport](#streamable-http-transport)
4. [Startup Tiers](#startup-tiers)
   - [Eager](#eager)
   - [Lazy (Default)](#lazy-default)
   - [Background](#background)
   - [Auto-Demotion](#auto-demotion)
5. [Tool Namespacing](#tool-namespacing)
6. [Prompts Surface](#prompts-surface)
7. [Resources Surface](#resources-surface)
8. [Schema Caching](#schema-caching)
9. [Read-Only Tool Hint](#read-only-tool-hint)
10. [MCP Manager TUI](#mcp-manager-tui)
11. [Authentication Diagnostics](#authentication-diagnostics)
12. [Reference Plugin Example](#reference-plugin-example)
13. [Troubleshooting](#troubleshooting)

---

## Overview

When acting as an MCP client, Reasonix follows the MCP specification to communicate with external servers over JSON-RPC 2.0. The lifecycle consists of two phases:

**Phase A — Startup (Tools):** Must complete before the agent can use any tools from the server. This involves the `initialize` handshake, capability discovery, and `tools/list` to discover available tools.

**Phase B — Auxiliary Surfaces (Prompts & Resources):** Runs asynchronously after Phase A. Discovers prompts (`prompts/list`) and resources (`resources/list`), surfacing them as slash commands and `@`-references respectively.

The entire system is managed by the `plugin.Host`, which owns all running plugin connections, aggregates tools/prompts/resources, and handles lifecycle operations like hot-adding or removing servers mid-session.

---

## Configuration

### reasonix.toml (Primary)

The primary configuration file is `reasonix.toml` (or `.reasonix.toml`). MCP servers are declared as `[[plugins]]` entries:

```toml
# Stdio MCP server — launches a subprocess
[[plugins]]
name    = "filesystem"
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/projects"]
env     = { NODE_OPTIONS = "--max-old-space-size=4096" }
tier    = "lazy"          # eager | lazy (default) | background

# Another stdio server with a custom working directory
[[plugins]]
name    = "database"
type    = "stdio"
command = "npx"
args    = ["-y", "@modelcontextprotocol/server-postgres", "postgresql://localhost/mydb"]
env     = { PG_PASSWORD = "secret" }
dir     = "/home/user/db-project"
tier    = "background"

# Streamable HTTP MCP server — connects to a remote endpoint
[[plugins]]
name    = "my-remote-api"
type    = "http"
url     = "https://api.example.com/mcp"
headers = { Authorization = "Bearer sk-xxx", "X-Custom-Header" = "value" }
tier    = "lazy"

# Disable a server without removing its config
[[plugins]]
name       = "experimental"
command    = "npx"
args       = ["-y", "@experimental/mcp-server"]
auto_start = false
```

**Field Reference:**

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Unique server identifier (required) |
| `type` | string | Transport type: `"stdio"` (default), `"http"` / `"streamable-http"`, `"sse"` |
| `command` | string | Executable to launch (stdio only) |
| `args` | string[] | Arguments passed to the command |
| `env` | map | Environment variables for the subprocess |
| `url` | string | Remote server URL (HTTP/SSE only) |
| `headers` | map | Static HTTP headers sent on every request (HTTP only) |
| `dir` | string | Working directory for the subprocess (stdio only) |
| `auto_start` | bool | Whether to auto-start at boot (default: `true`) |
| `tier` | string | Startup tier: `"eager"`, `"lazy"` (default), `"background"` |

**Variable Expansion:** The fields `command`, `url`, `args`, `env`, and `headers` support `${VAR}` and `${VAR:-default}` syntax for environment variable expansion at load time.

### .mcp.json (Claude Code Compatible)

Reasonix can read Claude Code's `.mcp.json` format, making it easy to share MCP server configurations across teams. Place the file in your project root or home directory:

```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "ghp_xxx" }
    },
    "remote-tool": {
      "type": "http",
      "url": "https://tools.example.com/mcp",
      "headers": { "X-API-Key": "key-xxx" }
    }
  }
}
```

When both `reasonix.toml` and `.mcp.json` define a server with the same name, the `reasonix.toml` entry wins. This allows project-level overrides of shared configurations.

### CLI Commands

MCP servers can be managed from the command line. These commands persist changes to the config file and take effect on the next session:

```bash
# List all configured MCP servers
reasonix mcp list

# Add a stdio server
reasonix mcp add github npx -y @modelcontextprotocol/server-github

# Add with environment variables (repeatable flag)
reasonix mcp add github npx -y @modelcontextprotocol/server-github \
  --env GITHUB_TOKEN=ghp_xxx

# Add a Streamable HTTP server
reasonix mcp add my-api --http https://api.example.com/mcp \
  --header "Authorization=Bearer sk-xxx" \
  --header "X-Custom=value"

# Add a legacy SSE server
reasonix mcp add legacy-api --sse https://old.example.com/sse

# Remove a server
reasonix mcp remove github
```

### In-Chat Slash Commands

Servers can also be managed dynamically during an active session. These take effect immediately:

```
# Hot-add a server mid-session (connects immediately)
/mcp add filesystem npx -y @modelcontextprotocol/server-filesystem /home/user/projects

# Disconnect and remove a server
/mcp remove filesystem

# Open the MCP Manager TUI overlay
/mcp
```

### Config Source Priority

When the same server name appears in multiple config sources, the priority order is:

1. **`reasonix.toml`** `[[plugins]]` — highest priority, wins on name collision
2. **`.mcp.json`** `mcpServers` — secondary, shared across tools
3. **Legacy** `~/.reasonix/config.json` — v0.x backward compatibility

---

## Transport Types

### Stdio Transport

The stdio transport launches an MCP server as a subprocess and communicates over its stdin/stdout using newline-delimited JSON-RPC 2.0. This is the most common transport for local MCP servers.

**How it works:**

1. Reasonix launches the subprocess via `exec.CommandContext`, binding its lifetime to the parent context
2. A dedicated reader goroutine demultiplexes JSON-RPC responses by matching the `id` field
3. Requests are serialized over the shared stdin pipe (`callMu` mutex ensures one-at-a-time writes)
4. Stderr output is captured in a bounded 16 KiB ring buffer for failure diagnostics
5. On close, the entire process tree is killed (handles grandchild processes) with a 5-second grace period

**PATH resolution:** If the specified command is not found in the inherited environment PATH, Reasonix falls back to the user's login shell PATH by trying `-l -i -c`, then `-l -c`, then `-c` shell invocation patterns. On Windows, `PATHEXT` expansion handles bare commands (e.g., `python` → `python.exe`).

**Process management on Windows:** Uses Windows Job Objects for reliable process tree reaping, ensuring child processes are cleaned up when Reasonix exits.

### Streamable HTTP Transport

The Streamable HTTP transport connects to remote MCP servers over HTTP. Every JSON-RPC message is sent as an HTTP POST to the server URL.

**How it works:**

1. Each JSON-RPC request is an HTTP POST to the configured URL
2. The server may reply with `application/json` (single response) or `text/event-stream` (SSE stream)
3. The `Mcp-Session-Id` header is captured from responses and echoed on subsequent requests to maintain session affinity
4. Static `headers` (e.g., bearer tokens) are sent on every request
5. SSE stream scanning accumulates `data:` lines across events, matches responses by `id`, and skips server-initiated notifications
6. Maximum body size: 16 MiB (`maxHTTPBody`)
7. Calls to the **same** server are serialized by a mutex, but calls to **different** servers run concurrently

**SSE transport (legacy):** The `type: "sse"` is recognized but returns an error: `"legacy sse transport not yet supported — use type=\"http\" (Streamable HTTP)"`. Use `type: "http"` instead.

---

## Startup Tiers

The `tier` setting controls how aggressively Reasonix connects to each MCP server at boot. Choosing the right tier is important for balancing startup speed against tool availability.

### Eager

- Blocks at boot until the full handshake completes (initialize + tools/list)
- Tools are guaranteed to be available from the first agent turn
- **Trade-off:** Slow servers delay agent startup. If a critical tool must be available immediately, use eager; otherwise, prefer lazy or background
- Auto-demotion applies: if an eager server is chronically slow, it gets demoted to lazy

### Lazy (Default)

- Placeholder tools are registered using cached schemas (if available) without connecting
- The actual connection only happens when the model first attempts to use a tool from this server
- **With cache hit:** Placeholder carries real tool descriptions; first `Execute` triggers a synchronous handshake, then the call goes through
- **Without cache:** A single stub tool `mcp__<server>__connect` is shown; first use triggers an async handshake and tells the model to retry next turn
- **Trade-off:** Zero boot impact, but the first tool call has connection latency

### Background

- Same placeholder behavior as lazy, but starts the handshake immediately at boot (non-blocking)
- By the time the model needs a tool, the connection may already be established
- **Trade-off:** Minimal boot impact with a good chance of immediate availability; uses resources for the handshake even if the server is never used

### Auto-Demotion

Reasonix tracks startup latency for each server across sessions. If an eager server exceeds the 5-second budget on 3 consecutive startups, it is automatically demoted to lazy for the current session. This prevents a single slow server from blocking the entire agent startup.

Statistics are stored at `<cacheDir>/mcp/<slug>.stats.json` with a rolling window of up to 20 samples. The `Recommend()` function inspects the last 3 samples and recommends demotion if all exceed the budget.

---

## Tool Namespacing

Remote MCP tools are namespaced to avoid collisions between servers that may expose tools with the same name. The naming convention matches the Claude Code standard:

```
mcp__<server>__<tool>
```

Examples:
- Server `filesystem` exposing tool `read_file` → `mcp__filesystem__read_file`
- Server `github` exposing tool `create_issue` → `mcp__github__create_issue`
- Server `my-api` exposing tool `search` → `mcp__my-api__search`

**Name normalization:** Characters not matching `[a-zA-Z0-9_-]` are replaced with `_`. If the name was modified during normalization, a 6-character FNV hash is appended to ensure uniqueness.

**Tool prefix removal:** The `ToolPrefix(server)` function returns `"mcp__<server>__"` so that the tool registry can strip the prefix when needed for display or dispatch.

---

## Prompts Surface

MCP servers can expose **prompts** — reusable prompt templates with arguments. Reasonix discovers these during Phase B and surfaces them as slash commands.

**Discovery:**

1. After the tools handshake completes, Reasonix calls `prompts/list` on servers that advertised prompt capabilities
2. Each prompt is mapped to a slash command: `mcp__<server>__<prompt>`
3. The `MCPSurfaceReady` event fires, triggering a UI refresh

**Usage:**

```
/mcp__example__review
```

If the prompt accepts arguments, Reasonix prompts the user to fill them in. For example, a `review` prompt with a `path` argument will ask for the file path before rendering.

**Execution:**

1. User invokes the slash command
2. Reasonix calls `prompts/get` with the prompt name and user-supplied arguments
3. The returned messages are flattened into text and injected into the conversation

---

## Resources Surface

MCP servers can expose **resources** — static or dynamic data that can be referenced in conversations. Reasonix discovers these during Phase B.

**Discovery:**

1. After the tools handshake, Reasonix calls `resources/list` on servers that advertised resource capabilities
2. Each resource records the server name, URI, display name, description, and MIME type

**Usage:**

Resources are referenced using the `@` syntax in conversations:

```
@example:doc://style-guide
```

Reasonix resolves the reference by calling `resources/read` on the server with the given URI. The URI does not need to be listed in `resources/list` — servers may expose templated URIs that accept arbitrary paths.

**Content handling:**

- Text content is extracted and embedded directly in the conversation
- Binary/blob content is noted with a placeholder (Reasonix is text-first for resource content)

---

## Schema Caching

Reasonix caches MCP tool schemas to support lazy and background startup tiers. This allows the agent to display meaningful tool descriptions without connecting to the server first.

**Cache format:**

```json
{
  "version": 1,
  "spec_hash": "a1b2c3d4e5f6...",
  "capabilities": { "prompts": true, "resources": false },
  "tools": [
    {
      "name": "read_file",
      "description": "Read the contents of a file",
      "schema": { "type": "object", "properties": { "path": { "type": "string" } } },
      "read_only": true
    }
  ],
  "last_validated": "2026-06-06T12:00:00Z"
}
```

**Storage:** `<cacheDir>/mcp/<slug>.json` where `<slug>` is derived from the server name.

**Fingerprinting:** The `spec_hash` is computed from the load-bearing fields: type, command, url, dir, args, env, headers (with deterministic map ordering). Any change to the spec invalidates the cache.

**Graceful degradation:** If the cache is stale or missing, Reasonix falls back to a fresh handshake. Cache writes happen asynchronously and never block the startup path.

---

## Read-Only Tool Hint

MCP tools can be annotated with `annotations.readOnlyHint: true` to indicate they perform no side effects. Reasonix maps this to its internal `Tool.ReadOnly()` flag, which has several important effects:

1. **Auto-approval:** In less restrictive permission modes (e.g., `auto-edit` or `full-auto`), read-only tools can be automatically approved without user confirmation
2. **Parallel dispatch:** Read-only tools can run concurrently with other read-only tools, improving throughput when the agent needs to gather information from multiple sources
3. **Reader-default permission:** The permission system defaults to allowing read-only operations, reducing friction for common tasks like reading files or searching code

**Manual override:** You can also mark specific tools as read-only via the `Spec.ReadOnlyToolNames` configuration, which is useful for first-party tools where you want to override the server's annotation:

```toml
[[plugins]]
name = "my-api"
type = "http"
url  = "https://api.example.com/mcp"
# Treat these tools as read-only even if the server doesn't annotate them
```

---

## MCP Manager TUI

The MCP Manager is an interactive terminal UI overlay for managing MCP servers during a session. Open it by typing `/mcp` in the chat.

**Stages:**

| Stage | Description |
|-------|-------------|
| **List** | Shows all servers grouped by Built-in/User, with status indicators (connected, failed, disabled, deferred) |
| **Detail** | Shows server status, auth, transport, capabilities, tools list, and error messages |
| **Tools** | Lists all tools exposed by the server with names and descriptions |
| **Logs** | Shows failure error text for debugging |
| **Mode** | Change the connection tier (lazy/background/eager) |
| **Confirm Remove** | Confirmation dialog for removing a server |
| **Confirm Clear Auth** | Confirmation dialog for stripping authentication material |

**Actions by server state:**

| Server State | Available Actions |
|---|---|
| **Connected** | Reconnect, Change mode, Edit config, Clear auth, Disable, Remove |
| **Failed** | Authenticate (if auth required), Retry, Clear auth, View logs, Change mode, Edit config, Disable, Remove |
| **Disabled** | Enable and connect, Change mode, Edit config, Clear auth, Remove |
| **Deferred / Initializing** | Connect now, Change mode, Edit config, Clear auth, Disable, Remove |

---

## Authentication Diagnostics

Reasonix includes built-in authentication diagnostics for remote MCP servers. The system automatically detects and helps troubleshoot authentication issues.

**Diagnosis levels:**

| Level | Meaning |
|-------|---------|
| `none` | No authentication needed or already configured |
| `possible` | Remote server without auth configuration — may require credentials |
| `required` | Server returned 401/403 or an auth-related error |

**Detection logic:**

- `IsAuthFailure()` checks for HTTP status codes (401, 403) and error messages containing keywords like `unauthorized`, `forbidden`, `invalid token`, `login required`, `authentication`, `not authenticated`
- `HasAuthConfig()` inspects headers, URL, and env for authentication material (bearer tokens, API keys, etc.)
- `IsRemoteTransport()` returns true for `http`, `streamable-http`, and `sse` transport types

**Clearing auth:** The `ClearAuthConfig()` function strips auth-like keys/values from a server's configuration for safe display. The MCP Manager TUI provides a "Clear auth" action that removes credentials from the config while keeping the server definition intact.

---

## Reference Plugin Example

Reasonix includes a complete, minimal MCP stdio server at `cmd/reasonix-plugin-example/` that demonstrates the full MCP contract. It exposes:

- **echo** tool — echoes input text (read-only)
- **wordcount** tool — counts lines/words/bytes/runes (read-only)
- **review** prompt — generates a code review request (with `path` argument)
- **doc://style-guide** resource — exposes a coding style guide

**Configure:**

```toml
[[plugins]]
name    = "example"
command = "reasonix-plugin-example"
```

**Resulting surfaces:**

| Surface | Name |
|---------|------|
| Tool | `mcp__example__echo` |
| Tool | `mcp__example__wordcount` |
| Prompt | `/mcp__example__review` |
| Resource | `@example:doc://style-guide` |

Build and run the example to test your MCP client integration:

```bash
go build -o reasonix-plugin-example ./cmd/reasonix-plugin-example/
```

---

## Troubleshooting

### Server fails to connect

1. Check the MCP Manager TUI (`/mcp`) for error details in the **Logs** stage
2. Verify the command is in your PATH (`which npx` or `which python`)
3. Try running the server command manually to check for startup errors
4. Check environment variables are set correctly (especially API keys)
5. If the server is slow, consider switching from `eager` to `lazy` or `background` tier

### Tools not appearing

1. Ensure Phase A has completed — check the MCP Manager for connection status
2. Verify the server advertises tools in its `initialize` response
3. Check the tool names — they are prefixed with `mcp__<server>__`
4. If using lazy tier without cache, the model must attempt to use a tool first

### Authentication failures on remote servers

1. Check the MCP Manager — it will show "Authenticate" action if auth is required
2. Verify bearer tokens, API keys, and other credentials in headers/env
3. Use the "Clear auth" action to remove stale credentials and reconfigure
4. Ensure the URL is correct and the server is reachable

### Slow startup

1. Change eager servers to `lazy` or `background` tier
2. Auto-demotion should handle chronically slow servers, but you can manually adjust
3. Use `auto_start = false` for non-essential servers to skip them entirely
4. Check network connectivity for remote HTTP servers — DNS resolution and TLS handshake add latency

### Process cleanup issues

1. Reasonix kills the entire process tree on close with a 5-second grace period
2. On Windows, Job Objects handle process tree reaping automatically
3. If orphaned processes remain, check for daemonization flags in the MCP server command
4. Avoid running MCP servers that fork into background processes
