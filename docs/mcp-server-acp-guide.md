# Reasonix as an MCP/ACP Server

Reasonix can expose itself as an **Agent Client Protocol (ACP)** server — a stdio JSON-RPC 2.0 agent that editors, IDEs, and other host clients can drive programmatically. This is how tools like VS Code extensions, Cursor, or custom editors embed Reasonix as their AI coding assistant.

The ACP protocol is wire-compatible with the v1 TypeScript agent specification. It provides a structured, bidirectional communication channel between a host client and Reasonix's agent, supporting session management, streaming updates, interactive permission requests, and MCP server injection.

---

## Table of Contents

1. [Overview](#overview)
2. [Starting the ACP Server](#starting-the-acp-server)
3. [Protocol Handshake](#protocol-handshake)
   - [initialize](#initialize)
   - [Agent Capabilities](#agent-capabilities)
4. [Session Lifecycle](#session-lifecycle)
   - [session/new](#sessionnew)
   - [session/prompt](#sessionprompt)
   - [session/load](#sessionload)
   - [session/cancel](#sessioncancel)
5. [Streaming Updates](#streaming-updates)
   - [Update Types](#update-types)
   - [Tool Kind Mapping](#tool-kind-mapping)
   - [Result Clipping](#result-clipping)
6. [Permission Requests](#permission-requests)
   - [Permission Kinds](#permission-kinds)
   - [Handling Flow](#handling-flow)
7. [MCP Server Injection](#mcp-server-injection)
8. [Content Blocks](#content-blocks)
9. [Error Codes](#error-codes)
10. [Example: Minimal Host Client](#example-minimal-host-client)
11. [Architecture Internals](#architecture-internals)
    - [Connection Layer](#connection-layer)
    - [Service Layer](#service-layer)
    - [Dispatch Layer](#dispatch-layer)
    - [Factory Interface](#factory-interface)
12. [Comparison: MCP Client vs ACP Server](#comparison-mcp-client-vs-acp-server)

---

## Overview

The ACP server transforms Reasonix from a standalone CLI tool into a programmable agent backend. Instead of a user typing in a terminal, a host client sends structured JSON-RPC messages over stdin/stdout, and Reasonix responds with streaming updates, permission requests, and session results.

**Key characteristics:**

- **Transport:** Stdio only (newline-delimited JSON-RPC 2.0)
- **Protocol version:** `1`
- **Message size cap:** 32 MiB per message
- **Concurrency:** One active session at a time per connection, but the host can create/load/cancel sessions sequentially
- **MCP integration:** The host can inject MCP servers into each session via `session/new` or `session/load` params

---

## Starting the ACP Server

```bash
# Start with the default model from config
reasonix acp

# Start with a specific model
reasonix acp --model deepseek-r1

# Start with a specific model and config
reasonix acp --model deepseek-r1 --config /path/to/reasonix.toml
```

The ACP server reads from stdin and writes to stdout. Stderr is used for internal logging and diagnostics. The host client typically launches `reasonix acp` as a subprocess and communicates over the subprocess's stdin/stdout pipes.

---

## Protocol Handshake

### initialize

The first message the host must send after launching the ACP server. It establishes the protocol version and identifies the client.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "clientInfo": {
      "name": "vscode-extension",
      "version": "1.0.0"
    }
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": 1,
    "agentCapabilities": {
      "loadSession": true,
      "promptCapabilities": {
        "image": false,
        "audio": false,
        "embeddedContext": true
      },
      "mcpCapabilities": {
        "http": false,
        "sse": false
      }
    },
    "agentInfo": {
      "name": "reasonix",
      "version": "1.2.0"
    },
    "authMethods": []
  }
}
```

### Agent Capabilities

| Capability | Value | Meaning |
|---|---|---|
| `loadSession` | `true` | Reasonix can resume existing sessions via `session/load` |
| `promptCapabilities.image` | `false` | Does not accept image content blocks in prompts |
| `promptCapabilities.audio` | `false` | Does not accept audio content blocks in prompts |
| `promptCapabilities.embeddedContext` | `true` | Accepts inline `resource` content blocks in prompts |
| `mcpCapabilities.http` | `false` | Does not accept HTTP-based MCP servers from the host |
| `mcpCapabilities.sse` | `false` | Does not accept SSE-based MCP servers from the host |

The `mcpCapabilities` values mean that the host can only inject **stdio** MCP servers. Remote HTTP/SSE servers should be configured in `reasonix.toml` instead.

---

## Session Lifecycle

### session/new

Creates a new agent session with a fresh conversation history. The host specifies the working directory and optionally injects MCP servers.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "session/new",
  "params": {
    "cwd": "/home/user/my-project",
    "mcpServers": [
      {
        "name": "filesystem",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/my-project"],
        "env": {}
      },
      {
        "name": "custom-tool",
        "command": "/usr/local/bin/my-mcp-server",
        "args": ["--verbose"],
        "env": { "API_KEY": "sk-xxx" }
      }
    ]
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "sessionId": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

**Session creation process:**

1. A UUID session ID is generated
2. An `updateSink` is created to map agent events to ACP notifications
3. The `Factory.NewSession()` method assembles a per-session controller:
   - Provider initialized from the config model (or `--model` flag)
   - Built-in tools rooted at the session's `cwd` via `builtin.Workspace`
   - MCP plugins: config's `AutoStartPlugins()` plus the host's per-session `mcpServers`
   - Phase B (prompts + resources) runs in a background goroutine
   - Permission policy and interactive approval are enabled
   - Optional planner model and task tool are configured
4. Interactive approval is bridged to ACP `session/request_permission` round-trips
5. The session is registered and ready for prompts

**Note:** The `cwd` parameter determines the root directory for file operations. If omitted, the current working directory of the `reasonix acp` process is used.

### session/prompt

Sends a user message to the agent and runs a full turn. The agent processes the prompt, potentially makes tool calls, and eventually returns with a stop reason.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "session/prompt",
  "params": {
    "sessionId": "550e8400-e29b-41d4-a716-446655440000",
    "prompt": [
      {
        "type": "text",
        "text": "Explain the main function in main.go"
      },
      {
        "type": "resource",
        "resource": {
          "uri": "file:///home/user/my-project/main.go"
        },
        "mimeType": "text/plain"
      }
    ]
  }
}
```

**Response (when agent completes):**

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "stopReason": "end_turn",
    "transcriptPath": "/home/user/.local/share/reasonix/sessions/550e8400/transcript.jsonl"
  }
}
```

**Stop reasons:**

| Value | Meaning |
|---|---|
| `"end_turn"` | Agent completed normally — finished processing and generated a response |
| `"cancelled"` | The turn was cancelled by the client via `session/cancel` |
| `"error"` | An error occurred during processing |

The `transcriptPath` is an optional field pointing to the JSONL transcript file for the session. The host can use this for debugging, auditing, or session replay.

**During execution**, the agent sends multiple `session/update` notifications (see [Streaming Updates](#streaming-updates)) and may send `session/request_permission` requests (see [Permission Requests](#permission-requests)).

### session/load

Resumes an existing session by loading its transcript and replaying history. This is useful for continuing conversations across editor restarts or reconnecting after a disconnection.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 4,
  "method": "session/load",
  "params": {
    "sessionId": "550e8400-e29b-41d4-a716-446655440000",
    "cwd": "/home/user/my-project",
    "mcpServers": [
      {
        "name": "filesystem",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/my-project"]
      }
    ]
  }
}
```

**Response:** Same as `session/new` — returns the same `sessionId`.

**Session loading process:**

1. The controller is rebuilt from the transcript file
2. History is replayed as `session/update` notifications so the client can reconstruct the conversation UI
3. The session is ready for new `session/prompt` calls
4. MCP servers are reconnected (both config servers and host-injected servers)

The `cwd` and `mcpServers` parameters work the same as in `session/new`. If `cwd` is omitted, the original session's working directory is used. If `mcpServers` is omitted, only the config's auto-start servers are connected.

### session/cancel

Aborts the currently running agent turn. This is a notification (no response expected).

**Notification:**

```json
{
  "jsonrpc": "2.0",
  "method": "session/cancel",
  "params": {
    "sessionId": "550e8400-e29b-41d4-a716-446655440000"
  }
}
```

After cancellation, the current `session/prompt` request will return with `stopReason: "cancelled"`. The session remains active and can accept new prompts.

---

## Streaming Updates

While the agent is processing a prompt, Reasonix sends `session/update` notifications to keep the host client informed in real time. These updates allow the client to render a live UI showing the agent's progress.

### Update Types

| Update Type | Trigger | Content |
|---|---|---|
| `agent_thought_chunk` | Agent reasoning output | Incremental reasoning text (for "thinking" models like DeepSeek-R1) |
| `agent_message_chunk` | Agent text output | Incremental response text |
| `user_message_chunk` | Replay of user messages | Original user text (primarily during session/load replay) |
| `tool_call` | Tool dispatched | Tool name, kind, call ID (status: pending) |
| `tool_call_update` | Tool completed or failed | Result content, status (completed/failed) |

**Example — Agent text chunk:**

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": {
    "sessionId": "550e8400-...",
    "type": "agent_message_chunk",
    "content": "The main function in main.go "
  }
}
```

**Example — Tool call:**

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": {
    "sessionId": "550e8400-...",
    "type": "tool_call",
    "callId": "call_abc123",
    "toolName": "bash",
    "toolKind": "execute",
    "status": "pending"
  }
}
```

**Example — Tool result:**

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": {
    "sessionId": "550e8400-...",
    "type": "tool_call_update",
    "callId": "call_abc123",
    "status": "completed",
    "content": "main.go:5: func main() {\nmain.go:6:     fmt.Println(\"Hello, World!\")\nmain.go:7: }"
  }
}
```

### Tool Kind Mapping

Reasonix maps its internal tool names to standardized ACP tool kinds for consistent rendering across host clients:

| Internal Tool | ACP Kind |
|---|---|
| `read_file`, `ls`, `glob` | `"read"` |
| `grep` | `"search"` |
| `edit_file`, `multiedit`, `write_file` | `"edit"` |
| `bash` | `"execute"` |
| MCP tools (`mcp__*`) | Heuristic: name matching against known patterns |
| All others | `"other"` |

The heuristic for MCP tools checks the tool name for patterns like `read`, `search`, `edit`, `exec`, etc., and maps accordingly. Tools that don't match any pattern fall back to `"other"`.

### Result Clipping

Tool results are clipped to **8000 characters** (`maxResultChars`) before being sent as `tool_call_update` notifications. This prevents extremely large outputs (e.g., reading a 10 MB file) from overwhelming the JSON-RPC message channel. The full result is still available in the transcript file.

---

## Permission Requests

When the agent needs user approval for a tool call (e.g., executing a bash command, writing a file), Reasonix sends a `session/request_permission` request to the host client. This is a JSON-RPC **request** (not a notification), meaning the host must respond with a choice.

### Permission Kinds

| Kind | Meaning |
|---|---|
| `allow_once` | Allow this specific invocation only |
| `allow_always` | Allow all invocations of this tool in this session |
| `allow_persistent` | Allow all invocations and persist the decision across sessions |
| `reject_once` | Reject this specific invocation |
| `reject_always` | Reject all invocations of this tool in this session |

### Handling Flow

**1. Agent requests permission:**

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "session/request_permission",
  "params": {
    "sessionId": "550e8400-...",
    "toolCall": {
      "toolName": "bash",
      "toolKind": "execute",
      "input": {
        "command": "rm -rf /tmp/test-build"
      }
    },
    "options": [
      { "optionId": "1", "name": "Allow once", "kind": "allow_once" },
      { "optionId": "2", "name": "Allow always", "kind": "allow_always" },
      { "optionId": "3", "name": "Allow persistent", "kind": "allow_persistent" },
      { "optionId": "4", "name": "Reject", "kind": "reject_once" },
      { "optionId": "5", "name": "Reject always", "kind": "reject_always" }
    ]
  }
}
```

**2. Host client displays a UI dialog and collects the user's choice.**

**3. Host responds with the selected option:**

```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "optionId": "1"
  }
}
```

**4. Reasonix applies the decision and either proceeds with or cancels the tool call.**

The available options may vary depending on the tool and the current permission policy. For example, read-only tools in a permissive mode may not require any permission at all, while destructive operations always require explicit approval.

---

## MCP Server Injection

The host client can inject MCP servers into a Reasonix session via the `mcpServers` parameter in `session/new` and `session/load`. These servers are connected alongside Reasonix's configured plugins and are available for the duration of the session.

**Supported:**

- **Stdio servers only** — the `mcpCapabilities.http` and `mcpCapabilities.sse` are `false`
- Each server is specified with `name`, `command`, `args`, and `env`
- Servers are namespaced the same way as configured plugins: `mcp__<name>__<tool>`

**Not supported via host injection:**

- HTTP/SSE remote servers — configure these in `reasonix.toml` instead
- Servers requiring complex startup (e.g., custom working directories, PATH resolution) — configure these in `reasonix.toml` for full control

**Lifecycle:**

- Injected servers are connected when the session is created
- They are disconnected when the session ends or the ACP connection closes
- They are NOT persisted across sessions — each `session/new` starts fresh
- Phase B (prompts and resources) for injected servers runs asynchronously after the session is created

---

## Content Blocks

The `session/prompt` method accepts an array of content blocks, allowing rich input beyond plain text. Two content block types are supported:

### Text Block

```json
{
  "type": "text",
  "text": "Explain this code"
}
```

The primary content type — plain text that the agent processes as a user message.

### Resource Block

```json
{
  "type": "resource",
  "resource": {
    "uri": "file:///home/user/project/main.go"
  },
  "mimeType": "text/plain"
}
```

An inline resource reference that provides context to the agent. The `embeddedContext: true` capability means Reasonix can process these blocks. The resource URI can be a file path, an MCP resource URI (`@server:uri`), or any URI the agent can resolve.

**Binary data block** (not fully supported):

```json
{
  "type": "resource",
  "resource": {
    "uri": "file:///home/user/project/image.png"
  },
  "mimeType": "image/png",
  "data": "<base64-encoded-content>"
}
```

While the protocol defines this format, Reasonix's current `image: false` capability means it does not process image content blocks. This may be added in future versions.

---

## Error Codes

The ACP server uses standard JSON-RPC 2.0 error codes:

| Code | Name | Meaning |
|---|---|---|
| `-32700` | `ErrParse` | Invalid JSON received |
| `-32600` | `ErrInvalidRequest` | The JSON sent is not a valid Request object |
| `-32601` | `ErrMethodNotFound` | The method does not exist or is not available |
| `-32602` | `ErrInvalidParams` | Invalid method parameter(s) |
| `-32603` | `ErrInternal` | Internal JSON-RPC error |

**Example error response:**

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "error": {
    "code": -32602,
    "message": "invalid params: sessionId is required"
  }
}
```

---

## Example: Minimal Host Client

This Python pseudocode demonstrates a minimal host client that launches Reasonix, creates a session, sends a prompt, and collects streaming updates:

```python
import subprocess
import json
import sys

class ReasonixACPClient:
    def __init__(self, model="deepseek-r1"):
        self.proc = subprocess.Popen(
            ["reasonix", "acp", "--model", model],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0
        )
        self.next_id = 1

    def send(self, method, params=None):
        msg = {"jsonrpc": "2.0", "id": self.next_id, "method": method}
        if params:
            msg["params"] = params
        self.next_id += 1
        self.proc.stdin.write((json.dumps(msg) + "\n").encode())
        self.proc.stdin.flush()

    def send_response(self, request_id, result):
        msg = {"jsonrpc": "2.0", "id": request_id, "result": result}
        self.proc.stdin.write((json.dumps(msg) + "\n").encode())
        self.proc.stdin.flush()

    def read_message(self):
        line = self.proc.stdout.readline().decode()
        return json.loads(line)

    def run(self):
        # 1. Initialize
        self.send("initialize", {
            "protocolVersion": 1,
            "clientInfo": {"name": "my-editor", "version": "1.0.0"}
        })
        init_result = self.read_message()
        print(f"Agent: {init_result['result']['agentInfo']['name']} v{init_result['result']['agentInfo']['version']}")

        # 2. Create session
        self.send("session/new", {
            "cwd": "/home/user/my-project",
            "mcpServers": [
                {
                    "name": "filesystem",
                    "command": "npx",
                    "args": ["-y", "@modelcontextprotocol/server-filesystem", "/home/user/my-project"]
                }
            ]
        })
        session_result = self.read_message()
        session_id = session_result["result"]["sessionId"]
        print(f"Session created: {session_id}")

        # 3. Send prompt
        self.send("session/prompt", {
            "sessionId": session_id,
            "prompt": [{"type": "text", "text": "List all TODO comments in the codebase"}]
        })

        # 4. Collect updates and handle permissions
        while True:
            msg = self.read_message()

            if "method" in msg:
                if msg["method"] == "session/update":
                    params = msg["params"]
                    if params["type"] == "agent_message_chunk":
                        print(params["content"], end="", flush=True)
                    elif params["type"] == "agent_thought_chunk":
                        print(f"[thinking] {params['content']}", end="", flush=True)
                    elif params["type"] == "tool_call":
                        print(f"\n[tool] {params['toolName']} ({params['toolKind']})")
                    elif params["type"] == "tool_call_update":
                        print(f"  -> {params['status']}: {params.get('content', '')[:200]}")

                elif msg["method"] == "session/request_permission":
                    # Show permission dialog to user
                    params = msg["params"]
                    print(f"\n[permission] {params['toolCall']['toolName']}: {params['toolCall']['input']}")
                    for opt in params["options"]:
                        print(f"  {opt['optionId']}: {opt['name']}")

                    choice = input("Choose: ")
                    self.send_response(msg["id"], {"optionId": choice})

            elif "result" in msg:
                # This is the prompt result
                print(f"\nDone: {msg['result']['stopReason']}")
                break

            elif "error" in msg:
                print(f"\nError: {msg['error']}")
                break

        self.proc.stdin.close()
        self.proc.wait()

if __name__ == "__main__":
    client = ReasonixACPClient()
    client.run()
```

---

## Architecture Internals

### Connection Layer

The `Conn` type in `internal/acp/server.go` handles the low-level JSON-RPC 2.0 communication over a reader/writer pair (typically stdin/stdout):

```go
type Conn struct {
    r       io.Reader
    wmu     sync.Mutex         // serializes writes to the output
    enc     *json.Encoder
    nextID  atomic.Int64       // auto-incrementing request ID
    pmu     sync.Mutex
    pending map[int64]chan rpcResult  // pending outbound requests
    reqH    map[string]RequestHandler  // registered request handlers
    notH    map[string]NotificationHandler  // registered notification handlers
}
```

**Key methods:**

| Method | Description |
|---|---|
| `Handle(method, handler)` | Register a handler for incoming requests |
| `HandleNotify(method, handler)` | Register a handler for incoming notifications |
| `Serve(ctx)` | Read loop — reads frames, dispatches to handlers, cancels in-flight handlers on exit |
| `Notify(method, params)` | Fire-and-forget notification (no response expected) |
| `Request(ctx, method, params)` | Outbound request — blocks until the host responds |

**Message size cap:** 32 MiB (`maxMessageBytes`) — frames larger than this are rejected to prevent memory exhaustion.

### Service Layer

The `Serve()` function in `internal/acp/service.go` is the single entry point for the ACP server:

```go
func Serve(ctx context.Context, r io.Reader, w io.Writer, factory Factory, info AgentInfo) error
```

It creates a `Conn`, registers all protocol handlers, and starts serving:

| Handler | Method | Description |
|---|---|---|
| `svc.initialize` | `"initialize"` | Protocol handshake |
| `svc.sessionNew` | `"session/new"` | Create a new session |
| `svc.sessionLoad` | `"session/load"` | Resume an existing session |
| `svc.sessionPrompt` | `"session/prompt"` | Run an agent turn |
| Notification handler | `"session/cancel"` | Abort the current turn |

### Dispatch Layer

The `updateSink` in `internal/acp/dispatch.go` maps the agent's typed event stream onto ACP notifications:

| Agent Event | ACP Notification |
|---|---|
| `event.Reasoning` | `agent_thought_chunk` |
| `event.Text` | `agent_message_chunk` |
| `event.ToolDispatch` | `tool_call` (status: pending) |
| `event.ToolResult` | `tool_call_update` (status: completed/failed) |
| `event.Notice` (warn) | `agent_message_chunk` with `[warning]` prefix |
| `event.CompactionDone` | `agent_message_chunk` with `[compacted]` note |
| `event.ApprovalRequest` | `session/request_permission` (round-trip) |

The dispatch layer also handles:
- **Tool kind mapping** — translates internal tool names to ACP `toolKind` values
- **Result clipping** — truncates tool results to 8000 characters for transmission
- **Event filtering** — skips internal events that don't map to ACP notifications

### Factory Interface

The `Factory` interface is the composition root that supplies per-session controllers:

```go
type Factory interface {
    NewSession(ctx context.Context, p SessionParams) (*control.Controller, error)
}

type SessionParams struct {
    Cwd        string
    MCPServers []plugin.Spec
    Sink       event.Sink
}
```

The `acpFactory` implementation (in `internal/cli/acp.go`) assembles each session with:
- Provider from the config model or `--model` flag
- Built-in tools rooted at the session's `cwd`
- MCP plugins: config's `AutoStartPlugins()` + host-injected `mcpServers`
- Phase B (prompts + resources) running in a background goroutine
- Permission policy with interactive approval bridged to ACP
- Optional planner model and task tool for sub-agent spawning

---

## Comparison: MCP Client vs ACP Server

| Aspect | MCP Client Mode | ACP Server Mode |
|---|---|---|
| **Purpose** | Connect to external MCP servers for tool/prompt/resource discovery | Expose Reasonix as a programmable agent backend for editors and tools |
| **Command** | `reasonix` (normal usage) | `reasonix acp [--model <name>]` |
| **Transport** | Stdio or Streamable HTTP (outbound) | Stdio only (inbound) |
| **Protocol** | MCP (JSON-RPC 2.0, version `2024-11-05`) | ACP (JSON-RPC 2.0, version `1`) |
| **Config** | `reasonix.toml`, `.mcp.json`, CLI commands | Via `session/new` params from host client |
| **Tools direction** | Discovers remote tools → `mcp__<server>__<tool>` | Exposes built-in + MCP tools to the agent |
| **Prompts** | Remote prompts → `/mcp__<server>__<prompt>` slash commands | Agent receives prompts from host |
| **Resources** | Remote resources → `@<server>:<uri>` references | Embedded as content blocks in prompts |
| **Auth** | Headers, env vars, auto-diagnosis | Permission requests to host client |
| **Hot management** | `/mcp add`, `/mcp remove`, MCP Manager TUI | `mcpServers` in `session/new` or `session/load` |
| **Streaming** | N/A (Reasonix is the client) | `session/update` notifications (chunks, tool calls) |
| **Session** | Single session per CLI invocation | Multiple sequential sessions per ACP connection |
