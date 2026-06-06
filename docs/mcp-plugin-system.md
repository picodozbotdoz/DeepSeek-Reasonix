# MCP Plugin System

Reasonix is an MCP (Model Context Protocol) client. External tools run as subprocesses or remote servers, communicating over JSON-RPC 2.0. The plugin system discovers, connects, and adapts remote tools to the `tool.Tool` interface so the agent treats plugin tools and built-ins uniformly.

## Architecture Overview

```
┌───────────────────────────────────────────────────────┐
│                    Plugin Host                         │
│                                                       │
│  ┌─────────┐  ┌─────────┐  ┌─────────┐               │
│  │ Client  │  │ Client  │  │ Client  │  ...           │
│  │(stdio)  │  │(http)   │  │(stdio)  │               │
│  └────┬────┘  └────┬────┘  └────┬────┘               │
│       │            │            │                      │
│  ┌────▼────┐  ┌────▼────┐  ┌────▼────┐               │
│  │Process  │  │HTTP     │  │Process  │               │
│  │(stdin/  │  │Client   │  │(stdin/  │               │
│  │ stdout) │  │         │  │ stdout) │               │
│  └─────────┘  └─────────┘  └─────────┘               │
│                                                       │
│  Aggregates: tools, prompts, resources                │
└───────────────────────────────────────────────────────┘
```

## Transport Layer

A `transport` interface hides the wire format so MCP-level logic (handshake, tools/list, tools/call) is written once:

```go
type transport interface {
    call(ctx context.Context, method string, params any) (json.RawMessage, error)
    notify(ctx context.Context, method string, params any) error
    close()
}
```

### Stdio Transport

The default transport. Launches a local subprocess and communicates via newline-delimited JSON-RPC 2.0 on stdin/stdout:
- Subprocess lifetime is bound to the parent context (`exec.CommandContext`).
- Stderr is captured in a bounded buffer for failure diagnostics.
- `${VAR}` / `${VAR:-default}` expansion in `command`, `args`, `env`.

### Streamable HTTP Transport

Connects to a remote MCP server at a URL:
- Each request is an HTTP POST.
- The server replies with `application/json` (one response) or `text/event-stream` (SSE stream).
- `Mcp-Session-Id` header, once seen, is echoed on subsequent requests.
- Static `headers` (e.g. bearer tokens) are sent on every request.
- `${VAR}` / `${VAR:-default}` expansion in `url` and `headers`.

### SSE Transport (Legacy)

The legacy 2024-11-05 HTTP+SSE transport is recognized but returns a clear error — it is deprecated upstream. Use `type = "http"` (Streamable HTTP) instead.

## Client Lifecycle

Each connected server is represented by a `Client`:

```go
type Client struct {
    name         string
    t            transport
    spec         Spec
    hasPrompts   bool
    hasResources bool
    toolCount    int
    transport    string
    prompts      []Prompt
    resources    []Resource
    tools        []ToolInfo
}
```

### Phase A: Startup (Tools)

1. **Open transport** — start the subprocess or HTTP client.
2. **Initialize** — `initialize` RPC with protocol version + client info → server capabilities.
3. **Send `notifications/initialized`** — complete the handshake.
4. **List tools** — `tools/list` RPC → discover tool names, descriptions, schemas, and `readOnlyHint` annotations.
5. **Adapt to `tool.Tool`** — each remote tool becomes a `remoteTool` struct implementing the `Tool` interface.
6. **Cache schema** — `SaveCachedSchema()` persists the tool list for next launch (lazy startup optimization).

### Phase B: Auxiliary Surfaces (Prompts & Resources)

After Phase A returns, the host asynchronously fetches optional surfaces:
- **Prompts** (`prompts/list`) — surface as `/mcp__<server>__<prompt>` slash commands.
- **Resources** (`resources/list`) — surface as `@<server>:<uri>` references.

Each completed surface fires an `MCPSurfaceReady` event so UIs can refresh.

## The Host

```go
type Host struct {
    clients   []*Client
    prompts   []Prompt
    resources []Resource
    failures  []Failure
}
```

The `Host` owns all running plugin connections and aggregates their surfaces:
- `StartAll()` — connects every plugin, aborts on first failure.
- `StartAvailable()` — connects every plugin it can, records failures.
- `Add()` — hot-adds one server mid-session (for `/mcp add`).
- `Remove()` — disconnects a server and drops its tools/prompts/resources.
- `ReadResource()` — resolves an `@<server>:<uri>` reference.
- `Close()` — terminates all connections.

### Hot-Add / Hot-Remove

The `/mcp add <spec>` and `/mcp remove <name>` commands let users connect or disconnect MCP servers during a session. `Add()` performs the full handshake, discovers tools, and returns them for the caller to register. `Remove()` returns the tool-name prefix for the caller to unregister.

### Startup Policy

```go
type StartPolicy struct {
    PerPluginTimeout time.Duration
    Concurrency      int
    AbortOnError     bool
}
```

- `PerPluginTimeout` — caps handshake duration (default: 5s for `StartAvailable`).
- `Concurrency` — caps parallel handshakes (default: 8, the standard "process storm" guardrail).
- `AbortOnError` — whether any single failure tears down the batch.

## Tool Namespace

Remote tools are namespaced `mcp__<server>__<tool>` (matching Claude Code):

```go
func toolName(server, raw string) string {
    return "mcp__" + normalizeName(server) + "__" + normalizeName(raw)
}
```

Spaces in server or tool names are normalized to underscores so the name is a clean identifier. Invalid characters are replaced with `_` and a short hash is appended to avoid collisions.

## ReadOnlyHint

A tool's MCP `annotations.readOnlyHint` maps to `Tool.ReadOnly()`:
- `true` → the tool joins parallel dispatch and the permission layer's reader-default.
- `false` or absent → the tool is treated as opaque (defaults to writer behavior).

First-party adapters can override this via `Spec.ReadOnlyToolNames` — trusted tools known to be read-only even when the server omits the annotation.

## Remote Tool Adapter

```go
type remoteTool struct {
    client   *Client
    name     string   // namespaced "mcp__<server>__<tool>"
    rawName  string   // original name for tools/call
    desc     string
    schema   json.RawMessage
    readOnly bool
}
```

- `Execute()` calls `tools/call {name, arguments}` on the client.
- The MCP result is parsed: text content is extracted, `isError` is checked.

## Prompt & Resource Surfaces

### Prompts as Slash Commands

MCP prompts appear as `/mcp__<server>__<prompt>` slash commands. Positional arguments after the command are passed as prompt arguments. The controller dispatches these through the same "start a turn" path as typed messages.

### Resources as @-References

MCP resources are referenced as `@<server>:<uri>` in chat. The controller resolves them by calling `resources/read` on the appropriate client. The URI need not be one listed by `resources/list` — servers may expose templated URIs.

## Schema Caching

`SaveCachedSchema()` / `LoadCachedSchema()` persist tool schemas between launches. This enables **lazy startup**: when a plugin's spec hasn't changed (fingerprinted), its cached tools are available immediately as placeholder entries, and the actual handshake happens on-demand when the model first uses one.

## Configuration Sources

MCP servers can be declared in two places:

1. **`[[plugins]]` in `reasonix.toml`** — the Reasonix-native source.
2. **`.mcp.json` in the project root** — Claude Code's exact `mcpServers` schema.

Both sources are merged; on a name collision, `reasonix.toml` wins. The `.mcp.json` format maps field-for-field onto `[[plugins]]`, so servers configured for Claude Code work in Reasonix unchanged.

## Example Plugin

`cmd/reasonix-plugin-example/` is a runnable reference stdio server:
- `echo` tool — echoes back its input.
- `wordcount` tool — counts words in text.
- `review` prompt — generates a code review prompt.
- Style-guide resource — exposes a coding style guide.

Build it with `make build` and configure it:

```toml
[[plugins]]
name    = "example"
command = "reasonix-plugin-example"
```

## See Also

- [Architecture Overview](architecture-overview.md)
- [Tool System](tool-system.md)
- [Configuration Reference](configuration-reference.md)
