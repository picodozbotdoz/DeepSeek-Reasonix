# Architecture Overview

Reasonix is a DeepSeek-native AI coding agent written in Go, distributed as a single static binary. It follows a config- and plugin-driven architecture where the core knows only interfaces, and concrete implementations are resolved from registries at runtime or registered at compile time via `init()` functions. This document provides a top-level view of the entire system, its major subsystems, and how they interconnect.

## Core Design Principles

The architecture rests on five foundational principles that inform every design decision in the codebase:

1. **Config- and plugin-driven core.** The core binary knows only interfaces (`Provider`, `Tool`). Concrete models and tools are resolved by name from registries, declared in `reasonix.toml`, or injected by MCP plugins. There is no hardcoded `switch model` anywhere in the codebase — adding a new model is a config edit, not a code change.

2. **Single static binary.** The build uses `CGO_ENABLED=0` and cross-compiles to six targets (darwin/linux/windows × amd64/arm64) with one command (`make cross`). The only dependency beyond the Go standard library is a TOML parser (`BurntSushi/toml`). The TUI uses the Charm stack (bubbletea, lipgloss, bubbles) for rich terminal rendering.

3. **Interface-first & registry-based.** `Provider` and `Tool` are Go interfaces. Compile-time built-ins self-register via `init()`, and runtime plugins are adapted to the same interfaces. The agent loop never knows or cares whether a tool is built-in or remote — it only sees `*tool.Registry`.

4. **Cache-first prompt design.** The system prompt prefix (base prompt + tools + memory) must stay byte-stable across turns so that DeepSeek's automatic prefix cache stays warm. Never mutate the prefix mid-session — ride the turn tail instead. This principle governs how compaction, memory updates, and plan mode toggles work.

5. **Transport-agnostic controller.** One `control.Controller` sits behind every frontend (the Bubble Tea TUI, the HTTP/SSE server, the Wails desktop app). Add behavior to the controller, not a frontend, so all three inherit it identically.

## High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Frontends                                    │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────────────────┐  │
│  │  Chat TUI    │  │  HTTP/SSE    │  │  Desktop (Wails + React) │  │
│  │  (bubbletea) │  │  Server      │  │                          │  │
│  └──────┬───────┘  └──────┬───────┘  └──────────┬───────────────┘  │
│         │                 │                      │                  │
│         └─────────────────┼──────────────────────┘                  │
│                           │                                         │
│                    ┌──────▼───────┐                                  │
│                    │  Controller  │  (control.Controller)            │
│                    │  - commands  │  - Send / Cancel / Approve       │
│                    │  - session   │  - SetPlanMode / Compact         │
│                    │  - approval  │  - Rewind / Fork / Branch        │
│                    └──────┬───────┘                                  │
│                           │                                         │
│         ┌─────────────────┼────────────────────┐                    │
│         │                 │                    │                     │
│  ┌──────▼──────┐  ┌──────▼──────┐    ┌────────▼────────┐           │
│  │   Agent     │  │ Coordinator │    │  Plugin Host     │           │
│  │ (single)    │  │ (two-model) │    │  (MCP Client)    │           │
│  └──────┬──────┘  └──────┬──────┘    └────────┬────────┘           │
│         │                │                     │                     │
│  ┌──────▼────────────────▼─────────────────────▼────────┐           │
│  │                   Core Registries                      │           │
│  │  ┌─────────────┐  ┌──────────────┐  ┌─────────────┐  │           │
│  │  │  Provider   │  │    Tool      │  │   Skill     │  │           │
│  │  │  Registry   │  │   Registry   │  │   Store     │  │           │
│  │  └──────┬──────┘  └──────┬───────┘  └─────────────┘  │           │
│  └─────────┼────────────────┼───────────────────────────┘           │
│            │                │                                        │
│  ┌─────────▼─────┐  ┌──────▼──────────────────────┐                 │
│  │  Providers     │  │  Tools                      │                 │
│  │  - openai      │  │  Built-ins: read_file,      │                 │
│  │  - anthropic   │  │    write_file, edit_file,   │                 │
│  │                │  │    multi_edit, bash, ls,    │                 │
│  │                │  │    glob, grep, web_fetch,   │                 │
│  │                │  │    task, todo_write, ask,   │                 │
│  │                │  │    complete_step, ...        │                 │
│  │                │  │  MCP: mcp__<server>__<tool> │                 │
│  └────────────────┘  └─────────────────────────────┘                 │
└─────────────────────────────────────────────────────────────────────┘
```

## Package Layout & Dependency Direction

The project follows a strict acyclic dependency graph. Higher-level packages import lower-level ones, never the reverse. Built-in subpackages import their parent to self-register; parents never import children.

```
cmd/reasonix/main.go          # Entry point; blank-imports built-in providers/tools
  │
  ├── internal/cli/            # TUI, subcommands, setup wizard
  │     ├── internal/agent/    # Agent loop, session, coordinator
  │     ├── internal/control/  # Transport-agnostic controller
  │     ├── internal/plugin/   # MCP client (stdio + HTTP)
  │     └── internal/config/   # TOML configuration loading
  │
  ├── internal/provider/       # Provider interface + factory registry
  │     ├── provider/openai/   # OpenAI-compatible impl
  │     └── provider/anthropic/# Anthropic-native impl
  │
  ├── internal/tool/           # Tool interface + registry
  │     └── tool/builtin/      # Built-in tools (bash, read_file, …)
  │
  ├── internal/permission/     # Per-call permission gating
  ├── internal/sandbox/        # OS-level sandboxing (macOS Seatbelt)
  ├── internal/memory/         # REASONIX.md hierarchy + auto-memory
  ├── internal/skill/          # Skill discovery from Markdown
  ├── internal/hook/           # Shell hooks (PreToolUse, PostToolUse, …)
  ├── internal/checkpoint/     # Snapshot-based rewind
  ├── internal/serve/          # HTTP/SSE server frontend
  ├── internal/event/          # Typed event stream
  ├── internal/evidence/       # Evidence ledger for complete_step
  ├── internal/instruction/    # Project instruction parsing
  ├── internal/codegraph/      # CodeGraph integration (tree-sitter + SQLite)
  ├── internal/lsp/            # LSP client for diagnostics
  ├── internal/diff/           # Diff computation
  ├── internal/billing/        # Wallet balance queries
  ├── internal/i18n/           # Internationalization (en/zh)
  ├── internal/acp/            # Agent Communication Protocol
  ├── internal/command/        # Custom slash commands
  ├── internal/frontmatter/    # Frontmatter parsing for skills/commands
  ├── internal/doctor/         # Diagnostic report
  ├── internal/jobs/           # Background job manager
  └── internal/boot/           # Session bootstrapping
```

Dependency direction (acyclic): `cli → {agent, plugin, config} → {tool, provider}`.

## Subsystem Summary

| Subsystem | Package | Purpose |
|-----------|---------|---------|
| Agent Loop | `internal/agent` | Drives a single task: provider streaming, tool execution, context management |
| Coordinator | `internal/agent` | Two-model collaboration (planner + executor) in separate cache-stable sessions |
| Controller | `internal/control` | Transport-agnostic session driver; commands + event stream |
| Provider | `internal/provider` | Model-backend abstraction; factory registry keyed by "kind" |
| Tool | `internal/tool` | Capability abstraction; built-in + plugin registry |
| Plugin | `internal/plugin` | MCP client; stdio + Streamable HTTP transports |
| Permission | `internal/permission` | Per-call allow/ask/deny policy + interactive approver |
| Sandbox | `internal/sandbox` | OS-level confinement (macOS Seatbelt) |
| Memory | `internal/memory` | Hierarchical docs + auto-memory store |
| Skill | `internal/skill` | Invokable playbooks from Markdown |
| Hook | `internal/hook` | Shell-command hooks around the agent loop |
| Checkpoint | `internal/checkpoint` | Snapshot-based edit safety net |
| Serve | `internal/serve` | HTTP/SSE server frontend |
| Event | `internal/event` | Typed event stream (Sink interface) |
| Config | `internal/config` | TOML loading with resolution hierarchy |
| CodeGraph | `internal/codegraph` | Tree-sitter symbol/call-graph search |
| LSP | `internal/lsp` | Language Server Protocol client |
| Boot | `internal/boot` | Session bootstrapping (wiring all components) |
| Desktop | `desktop/` | Wails-based desktop app (separate Go module) |
| CLI | `internal/cli` | Bubble Tea TUI + subcommands |

## Key Architectural Decisions

### Why Go?

The v1 line (0.x) was TypeScript/Node. The 1.0 rewrite chose Go for three reasons: (1) single static binary distribution with no runtime dependencies, (2) excellent concurrency primitives (goroutines for parallel tool dispatch, streaming providers), and (3) compile-time speed and type safety for a long-running agent process.

### Why Registry-Based Extensibility?

Rather than hardcoding model or tool switching, the codebase uses registries (`provider.Register`, `tool.RegisterBuiltin`). This means adding a new provider is one file plus one import, and adding a new tool is one file plus one `init()` call. The agent core never has a `switch model` statement.

### Why a Transport-Agnostic Controller?

Three frontends (TUI, HTTP/SSE, Desktop) all drive the same controller. Turn lifecycle, cancellation, approval, plan mode, compaction, and session persistence are implemented once in the controller, and every frontend inherits them by issuing commands and rendering events.

### Why Separate Sessions for Planner/Executor?

Switching models inside one shared conversation would break the cache-stable prefix and tank DeepSeek's prefix cache hit rate. Instead, the Coordinator runs planner and executor in separate sessions that never mix, so both grow prepend-only and stay cache-friendly.

## Data Flow: A Single Turn

1. **User types input** → frontend calls `controller.Send(input)` or `controller.Submit(input)`
2. **Controller composes the turn** — resolves `@`-references, prepends pending memory notes, applies plan-mode framing
3. **Controller starts a guarded goroutine** — `runTurn` / `runTurnWithRaw`
4. **Agent.Run** appends the user message to the session, then enters the loop:
   - Build `Request` with session messages + tool schemas
   - Call `provider.Stream` → receive text/reasoning/tool-call chunks
   - If no tool calls → final answer (with readiness checks) → return
   - If tool calls → `executeBatch` (parallel for read-only, serial for writers)
   - After each tool batch → `maybeCompact` (context management)
   - Repeat until model gives a final answer or maxSteps reached
5. **If plan mode is on** → after the agent turn, controller requests user approval for the plan
6. **On approval** → plan mode off, auto-approve writers, execute the plan
7. **Events emitted throughout** → `event.Sink` dispatches to the active frontend

## See Also

- [Agent Loop & Coordinator](agent-loop-coordinator.md) — deep dive into the agent's run loop and two-model coordination
- [Provider System](provider-system.md) — provider interface, OpenAI/Anthropic implementations, streaming
- [Tool System](tool-system.md) — tool interface, built-in tools, registry, parallel dispatch
- [MCP Plugin System](mcp-plugin-system.md) — MCP client, transports, tool namespace, prompts/resources
- [Permission & Sandbox](permission-sandbox.md) — policy rules, interactive approval, OS confinement
- [Controller & Frontends](controller-frontends.md) — transport-agnostic controller, TUI, HTTP/SSE, desktop
- [Memory & Skills](memory-skills.md) — hierarchical docs, auto-memory, skill discovery
- [Hooks System](hooks-system.md) — shell-command hooks around the agent loop
- [Context Management](context-management.md) — compaction, cache-first design, prefix shaping
- [Configuration Reference](configuration-reference.md) — full config schema and resolution
- [Desktop App](desktop-app.md) — Wails architecture, React frontend, bindings
- [HTTP/SSE Server & ACP](http-server-acp.md) — REST API, SSE streaming, Agent Communication Protocol
- [Event System](event-system.md) — typed event stream, Sink interface, broadcaster
- [Checkpoint & Rewind](checkpoints-rewind.md) — snapshot store, capture seam, restore
- [Code Intelligence & LSP](code-intelligence-lsp.md) — CodeGraph, LSP client
- [Building & Distribution](building-distribution.md) — build system, cross-compilation, npm packaging
- [Data Flow & Sequences](data-flow-sequences.md) — detailed sequence diagrams
