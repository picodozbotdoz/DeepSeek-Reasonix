
Based on my analysis, here are the significant features/aspects in the `desktop-v1.2.1` tag that are **not yet covered** by separate documentation:

1. **Session Persistence & Branching** — JSONL format, branch metadata, conversation tree
2. **Sub-Agent / Task Tool** — spawning, tool filtering, nesting, background execution
3. **Builtin Tools Reference** — all 24+ tools with arguments and behavior
4. **Cache Diagnostics & Prefix Stability** — cache-first design, prefix shape tracking
5. **Loop Guards & Safety** — storm breaker, repeat-success guard, final readiness
6. **CLI TUI System** — 78 files, rendering, themes, interactive components
7. **Custom Slash Commands** — Markdown-based commands with frontmatter
8. **i18n & Localization** — multi-language catalog system
9. **Network & Proxy** — proxy modes, OS-level detection, SSRF protection
10. **Desktop Frontend Components** — 30+ React components
11. **Version Migration (v0→v1)** — session format migration



## Gap Analysis Results

Comparing the `desktop-v1.2.1` tag against the existing 19 docs, I identified **11 undocumented features/subsystems** that deserved separate documentation:

### New Docs Added (11 files, 2,736 lines)

| # | Document | Why It Deserves Separate Docs |
|---|----------|-------------------------------|
| 1 | **`session-persistence-branching.md`** | JSONL format, atomic saves, branch metadata (conversation tree), fork topology, scope/workspace, topic system, v0→v1 migration — a rich subsystem only briefly mentioned elsewhere |
| 2 | **`sub-agent-task-tool.md`** | Task tool is a deep system: tool filtering, meta-tool exclusion, event nesting, background execution, RunSubAgent shared core — not just "another tool" |
| 3 | **`builtin-tools-reference.md`** | 24+ tools with complete argument tables, behavior notes, and classification — essential reference that was missing |
| 4 | **`cache-diagnostics-prefix-stability.md`** | The cache-first design is the #1 architectural principle — PrefixShape tracking, churn diagnosis, session-level cache stats deserve their own treatment |
| 5 | **`loop-guards-safety.md`** | Storm breaker, repeat-success guard, final readiness system, evidence ledger, plan mode, max steps — a sophisticated safety system previously only mentioned in passing |
| 6 | **`cli-tui-system.md`** | The CLI is the largest subsystem (78 files, 20K+ lines) — markdown rendering, streaming redraw, themes, all interactive components |
| 7 | **`custom-slash-commands.md`** | User-facing feature for creating reusable prompt templates from Markdown files |
| 8 | **`i18n-localization.md`** | Multi-language catalog system with 200+ translatable strings — important for contributors |
| 9 | **`network-proxy.md`** | Four proxy modes, OS-level detection, SSRF protection, separate security boundaries |
| 10 | **`desktop-frontend-components.md`** | 30+ React components documented with descriptions — essential for desktop contributors |
| 11 | **`version-migration.md`** | v0.x → v1+ session migration with safety guarantees and format differences |

**Commit**: `5d39eb1` pushed to `origin/dev/1.2.0`

**Total docs now**: 30 files (19 original + 11 new)
