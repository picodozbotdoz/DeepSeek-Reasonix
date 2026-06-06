# Event System

The event system is Reasonix's internal message bus. Every subsystem — the agent, controller, plugins, frontends — communicates through typed events emitted into an `event.Sink`. This decouples producers from consumers and makes every frontend (TUI, HTTP, desktop) a simple renderer of the same event stream.

## Sink Interface

```go
type Sink interface {
    Emit(event Event)
}
```

A nil-safe discard sink (`event.Discard`) is available for headless runs and testing. Every component that takes a `Sink` replaces nil with `Discard`, so it can always emit unconditionally.

## Event Types

```go
type Kind string

const (
    TurnStarted   Kind = "turn_started"
    Phase         Kind = "phase"
    Text          Kind = "text"
    Reasoning     Kind = "reasoning"
    Message       Kind = "message"
    ToolDispatch  Kind = "tool_dispatch"
    ToolProgress  Kind = "tool_progress"
    ToolResult    Kind = "tool_result"
    Usage         Kind = "usage"
    Notice        Kind = "notice"
    Retrying      Kind = "retrying"
    AskRequest    Kind = "ask_request"
    ApprovalRequest Kind = "approval_request"
    TurnDone      Kind = "turn_done"
    MCPSurfaceReady Kind = "mcp_surface_ready"
)
```

### Event Structure

```go
type Event struct {
    Kind            Kind
    Text            string
    Reasoning       string
    Tool            Tool
    Usage           *provider.Usage
    Pricing         *provider.Pricing
    CacheDiagnostics *CacheDiagnostics
    SessionHit      int
    SessionMiss     int
    Ask             Ask
    Approval        Approval
    Level           Level    // Info|Warn
    Err             error
    RetryAttempt    int
    RetryMax        int
}
```

### Key Event Flows

#### Normal Turn

1. `TurnStarted` — the turn begins
2. `Phase` — model name + "executing" (or "planning" for coordinator)
3. `Reasoning` — thinking-mode chain-of-thought (may be streamed live or buffered)
4. `Text` — visible answer text (streamed live)
5. `ToolDispatch` — tool call begins (with args + diff preview)
6. `ToolProgress` — incremental output from the tool
7. `ToolResult` — tool call completes (with output/error/truncation)
8. `Usage` — token accounting with cost and cache diagnostics
9. `Message` — close the text stream (sink may re-render as markdown)
10. `TurnDone` — the turn is complete

#### Approval Flow

1. `ApprovalRequest` — tool needs user approval (id + tool name + subject)
2. User responds via `Approve(id, ...)`
3. Tool proceeds or is blocked

#### Ask Flow

1. `AskRequest` — the `ask` tool needs user input (id + questions)
2. User responds via `AnswerQuestion(id, answers)`
3. The `ask` tool receives the answers

#### Compaction

1. `Notice` (Info) — "compacting context..."
2. Internally, the session is rewritten
3. `Usage` — updated token counts after compaction

#### Error / Retry

1. `Retrying` — provider retry attempt (attempt/max)
2. `Notice` (Warn) — error message
3. `TurnDone` — with `Err` set on failure

## Broadcaster

The `Broadcaster` (`internal/serve/broadcaster.go`) implements `Sink` and fans out events to multiple subscribers:

```go
func (b *Broadcaster) Subscribe() (<-chan []byte, func())
```

Each subscriber gets a buffered channel of JSON-serialized events. The SSE server and ACP server both subscribe to the same broadcaster. Slow subscribers that don't drain their channel are dropped (non-blocking sends).

## Synchronous Sink

`event/sync.go` provides a synchronous sink that blocks on each `Emit()` call, useful for testing and for ensuring events are processed in order.

## Cache Diagnostics

The `Usage` event carries `CacheDiagnostics` — a comparison of the previous and current prefix shapes:

```go
type CacheDiagnostics struct {
    PrefixChanged bool
    Detail        string
}
```

When the prefix shape changes between turns (e.g. tool schemas were reordered), this explains the cache miss. Common diagnostics:
- "prefix shape unchanged" — cache should be warm
- "tools schema differs" — tool set changed
- "session rewritten" — compaction occurred

## Frontend Rendering

Each frontend interprets the event stream differently:
- **TUI** — renders text as live markdown, tool calls as cards, reasoning as dim text above the answer
- **HTTP/SSE** — serializes each event as a JSON `data:` frame
- **Desktop** — React components re-render on each event type

The event system is the single source of truth — frontends never query the agent directly; they only consume events and issue commands.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Controller & Frontends](controller-frontends.md)
- [HTTP/SSE Server & ACP](http-server-acp.md)
