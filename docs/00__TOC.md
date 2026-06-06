## Documentation Created: 17 New Files (~4,000 lines)

All files are in `/home/z/my-project/DeepSeek-Reasonix/docs/`:

| # | File | Description |
|---|------|-------------|
| 1 | **architecture-overview.md** | Top-level system architecture, design principles, package layout, dependency graph, subsystem summary, key architectural decisions, data flow overview |
| 2 | **agent-loop-coordinator.md** | Deep dive into the Agent run loop, streaming, tool execution, storm detection, plan mode, Session, Coordinator (two-model), subagents, cache shape diagnostics |
| 3 | **provider-system.md** | Provider interface, factory registry, OpenAI/Anthropic implementations, message/chunk types, retry logic, pricing, auth errors, schema canonicalization |
| 4 | **tool-system.md** | Tool interface, Previewer, Registry, all built-in tools (file ops, search, execution, agent coordination), path confinement, encoding support, MCP namespace, output capping |
| 5 | **mcp-plugin-system.md** | MCP client architecture, stdio/HTTP transports, client lifecycle (Phase A/B), Host, hot-add/remove, tool namespace, readOnlyHint, prompt/resource surfaces, schema caching, configuration |
| 6 | **permission-sandbox.md** | Policy rules (allow/ask/deny), precedence, glob matching, Gate (interactive/non-interactive), bash read-only detection, auto-approve, YOLO mode, file-writer confinement, macOS Seatbelt |
| 7 | **controller-frontends.md** | Controller commands, run-guarded, Compose, Submit dispatch, plan approval flow, interactive approval, auto-plan classification, session persistence, memory integration, all three frontends (TUI, HTTP, Desktop), boot sequence |
| 8 | **memory-skills.md** | Hierarchical docs (REASONIX.md), memory Set, auto-memory store, queue integration, WriteDoc/AppendDoc, skill structure, run modes (inline/subagent), discovery, built-in skills, skill index, management |
| 9 | **hooks-system.md** | Hook events (10 types), configuration (settings.json), trust model, execution flow, exit codes, PostLLMCall reasoning transformation, PreCompact guidance, timeouts, Runner |
| 10 | **context-management.md** | Compaction problem/solution, parameters, compaction process, stuck detection, cache-first design, system prompt prefix, memory updates without cache break, plan mode cache-friendliness, prefix shape diagnostics |
| 11 | **configuration-reference.md** | Full TOML schema with all sections, model references, multi-model providers, .mcp.json alternative, environment variables, .env loading, config migration, setup wizard |
| 12 | **desktop-app.md** | Wails architecture, Go backend (wire, tray, tabs, sessions, workspace, updater, single instance), React frontend (30+ components), system tray, multi-tab sessions, auto-updater |
| 13 | **http-server-acp.md** | HTTP/SSE server routes, SSE event stream, CSRF protection, CORS, model/effort switching, session titles, ETag caching, graceful shutdown, ACP protocol, dispatch, service layer, broadcaster |
| 14 | **event-system.md** | Sink interface, event types (15 kinds), event structure, key event flows, broadcaster, synchronous sink, cache diagnostics, frontend rendering |
| 15 | **checkpoints-rewind.md** | Data model, store, capture seam, controller API (Rewind/Fork), encoding preservation, path safety, turn boundaries, phasing |
| 16 | **code-intelligence-lsp.md** | CodeGraph (tree-sitter + SQLite), installation, tier system, tools, read-only mode, symlink safety; LSP client architecture, manager, JSON-RPC, position conversion, supported languages |
| 17 | **building-distribution.md** | Makefile targets, build command, cross-compilation, version injection, npm packaging, Homebrew, release archives, example plugin, testing, CodeGraph e2e, desktop build, dependency policy |
| 18 | **data-flow-sequences.md** | 7 detailed ASCII sequence diagrams: interactive chat turn, plan mode, two-model coordinator, permission gate, compaction, MCP plugin startup, checkpoint & rewind |
| 19 | **additional-subsystems.md** | Diff engine, evidence ledger, instruction system, frontmatter parser, file utilities (atomic write, encoding detection), billing, i18n, boot sequence, jobs manager, doctor, MCP diagnostics, nil utilities, system proxy, file reference search, command system, process utilities, output style |

Each document includes cross-references to related documents, creating a navigable documentation graph. The 3 pre-existing docs (`SPEC.md`, `MIGRATING.md`, `CHECKPOINTS.md`) are preserved.


**Commit**: `84517f8` on branch `dev/1.2.0`
**Message**: `docs: add comprehensive technical documentation`
**Files pushed**: 19 documentation files (3,323 insertions)

The following docs were pushed to `origin/dev/1.2.0`:
1. `docs/architecture-overview.md`
2. `docs/agent-loop-coordinator.md`
3. `docs/tool-system.md`
4. `docs/provider-system.md`
5. `docs/mcp-plugin-system.md`
6. `docs/event-system.md`
7. `docs/hooks-system.md`
8. `docs/context-management.md`
9. `docs/memory-skills.md`
10. `docs/code-intelligence-lsp.md`
11. `docs/permission-sandbox.md`
12. `docs/checkpoints-rewind.md`
13. `docs/controller-frontends.md`
14. `docs/desktop-app.md`
15. `docs/http-server-acp.md`
16. `docs/data-flow-sequences.md`
17. `docs/configuration-reference.md`
18. `docs/building-distribution.md`
19. `docs/additional-subsystems.md`


