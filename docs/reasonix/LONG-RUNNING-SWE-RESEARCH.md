# Long-Running SWE Capability Assessment — Research Findings

**Initiative**: LRSWE (Long-Running Software Engineering)
**Status**: Research Complete
**Date**: 2026-06-20
**Author**: Reasonix Capability Assessment

---

## Executive Summary

Reasonix already has the foundational primitives for long-running SWE (subagents, ACP workers, shared directories, skill system, heartbeat scheduling, dependency DAGs). The gap is in **durable execution** — the system layer that keeps the agent loop running for weeks across crashes, reboots, and context window limits. This document catalogs what exists, what the industry has converged on, and where Reasonix fits.

## 1. Problem Statement

Long-running SWE tasks (spanning days to weeks) require:

1. **Survivability** — crash recovery, session resumption, no work loss
2. **Context continuity** — bridging context windows without losing the plot
3. **Parallelism** — multiple agents/workers executing concurrently
4. **Coordination** — shared state without direct participant contact
5. **Verification** — deterministic checks before marking work complete
6. **Cost control** — bounded token usage per cycle, budget enforcement

## 2. Industry Reference Architectures

### 2.1 Anthropic Long-Running Agent Harness

**Source**: [anthropic.com/engineering/effective-harnesses-for-long-running-agents](https://www.anthropic.com/engineering/effective-harnesses-for-long-running-agents)

**Pattern**: Two-agent handoff
- **Initializer agent** (first run): sets up environment, feature list in JSON, `init.sh`, `claude-progress.txt`
- **Coding agent** (all subsequent sessions): reads progress, works on one feature, commits, updates progress file
- **Full context reset** between sessions — not compaction, complete rebuild from structured handoff file

**Key insight**: Each session starts by reading a structured progress file rather than parsing full git history. The progress file is the bridge between context windows.

**Reasonix mapping**: Subagent `continue_from`/`fork_from` already support session resumption. `subagent_store` persists transcripts. Missing: structured progress file pattern (`claude-progress.txt` equivalent).

### 2.2 OpenAI Harness Engineering (Codex)

**Source**: [openai.com/index/harness-engineering](https://openai.com/index/harness-engineering/)

**Pattern**: Environment-first development
- **Rigid architectural model** — fixed layers, validated dependency directions, custom linters
- **Golden principles** — opinionated mechanical rules encoded in repo
- **Background cleanup tasks** — scan for deviations, auto-open refactoring PRs
- **3.5 PRs/engineer/day** throughput with 7 engineers driving Codex

**Key insight**: Agents are most effective in environments with strict boundaries and predictable structure. The environment does the heavy lifting, not the model.

**Reasonix mapping**: Plan mode + evidence verification handle structured work. `heartbeat` system supports scheduled tasks. Missing: golden principle enforcement, background cleanup task orchestration.

### 2.3 LRA — Long-Running Agents (FareedKhan-dev)

**Source**: [github.com/FareedKhan-dev/long-running-agent](https://github.com/FareedKhan-dev/long-running-agent)

**Pattern**: Asymmetric agent organization
- **Lead Engineer** (single-threaded): owns all coupled code-writes, design coherence
- **Specialized agents**: fan out only for reading and reviewing (truly parallelizable)
- **Temporal** for durable execution: every LLM/tool call journaled, replay-from-cache
- **Git as source of truth**: every cycle commits progress

**Key insight**: The system runs for weeks while the model drives in verified bursts. Interruption is the normal case, not an edge case. Design for it from the start.

**Reasonix mapping**: Manager = Lead Engineer, subagents = read-only researchers, ACP workers = reviewers. Missing: durable execution engine (Temporal equivalent), mission-level task lifecycle.

### 2.4 Cortex Agent

**Source**: [github.com/fangxm233/cortex-agent](https://github.com/fangxm233/cortex-agent)

**Pattern**: Mission-driven multi-agent pipelines
- **TASKS.yaml** with priorities, dependencies, verifiable done-conditions
- **Multi-agent thread pipelines** — long jobs as relay of focused agents
- **Self-evolving skills** — agent catches repetitive work, writes a skill
- **Cron and interval scheduling**

**Key insight**: Work is partitioned across agent pipelines, each with bounded scope and fresh context. Handoffs carry only what the next stage needs.

**Reasonix mapping**: `parallel_tasks` has dependency DAG + wisdom sharing. `skill` system supports custom skills. Missing: mission-level task persistence (TASKS.yaml), self-evolving skill generation.

### 2.5 Night Shift

**Source**: [zeltrex.com/papers/nightshift-2026.pdf](https://zeltrex.com/papers/nightshift-2026.pdf)

**Pattern**: Four-layer architecture
- **Pulse**: budget governance
- **Mind**: task planning (BacklogManager, priority queue across 7 categories)
- **Hands**: execution (deterministic dispatch loop)
- **Reflect**: quality assessment

**Key insight**: Production deployment demands fundamentally different design priorities than benchmark performance. 269 tasks at $65.98 over 10+ days.

**Reasonix mapping**: Budget tracking exists. Plan mode handles task planning. Missing: category-based backlog, generation-based task optimization, reflective quality loop.

### 2.6 AiScientist

**Source**: [arxiv.org/pdf/2604.13018](https://arxiv.org/pdf/2604.13018)

**Pattern**: Thin control, thick state
- **File-as-Bus protocol** — state externalized into persistent project artifacts
- **Orchestrator** carries only summaries, all detail lives in files
- **Hierarchical research team** with role-owned artifacts

**Key insight**: Specialists inspect their role-owned files to resume prior work. Different specialists coordinate around shared evidence rather than lossy conversational handoffs.

**Reasonix mapping**: `_shared/` directory implements blackboard pattern. Missing: structured file-as-bus protocol, role-owned artifact conventions.

### 2.7 pm-go / Smithers

**Source**: [github.com/alex-reysa/pm-go](https://github.com/alex-reysa/pm-go), [github.com/smithersai/smithers](https://github.com/smithersai/smithers/)

**Pattern**: Durable control plane
- **Git worktrees** for isolation, dependency-ordered merge
- **Every step persisted** to SQLite/Postgres — crash resumes from last write
- **Approval + budget enforcement** — policy engine gates actions
- **Validator-first workflow** — tasks only advance when checks pass

**Key insight**: Move workflow out of chat transcript into durable stores (Postgres, Temporal, git worktrees). Runs are resumable, inspectable, and bounded.

**Reasonix mapping**: `worktree` skill handles isolation. `policy` layer handles permissions. Missing: step-level persistence, dependency-ordered merge automation.

### 2.8 CAS (Coding Agent System)

**Source**: [github.com/codingagentsystem/cas](https://github.com/codingagentsystem/cas)

**Pattern**: Factory + persistent context
- **Supervisor** plans work, creates tasks, assigns to workers, reviews, merges
- **Workers** each get own git worktree and branch
- **Shared context** via SQLite — all agents read/write same database
- **Terminal multiplexer** for parallel agent views

**Key insight**: Persistent context system (MCP server) gives agents memory, tasks, rules, skills across sessions.

**Reasonix mapping**: ACP workers with git worktrees. Missing: shared SQLite context, supervisor-worker task assignment protocol.

## 3. Convergence Patterns

Across all references, these patterns converge:

| Pattern | Prevalence | Reasonix Status |
|---------|-----------|-----------------|
| **Git as source of truth** | All | ✅ Workspace tracking exists |
| **Structured progress file** | Most | ❌ No `claude-progress.txt` equivalent |
| **Durable execution engine** | Most | ❌ No Temporal/checkpoint system |
| **Asymmetric agent org** | LRA, OpenAI | ⚠️ Manager exists but no lead-worker distinction |
| **Validator-first workflow** | pm-go, Smithers, Night Shift | ⚠️ Evidence verification exists but not gating |
| **Shared blackboard** | AiScientist, CAS, LRA | ⚠️ `_shared/` exists, no protocol |
| **Git worktree per task** | CAS, pm-go, Czarina | ⚠️ Skill exists, not auto-managed |
| **Budget governance** | Night Shift, pm-go | ⚠️ Basic tracking, no enforcement |
| **Self-evolving skills** | Cortex | ❌ Not implemented |
| **Dependency-ordered merge** | pm-go | ❌ Not implemented |

## 4. Reasonix Capability Inventory

### 4.1 Already Implemented

| Capability | Implementation | Files |
|-----------|---------------|-------|
| Subagent spawning | `task` tool, `parallel_tasks` | `internal/agent/task.go`, `parallel_tasks.go` |
| Dependency DAG | `depends_on` in `parallel_tasks` | `parallel_tasks.go:107-117` |
| Wisdom sharing | Temp files between parallel tasks | `parallel_tasks.go:138-189` |
| ACP worker delegation | `delegate_task`, `delegate_task_async` | `cmd/reasonix-plugin-acp-bridge/main.go` |
| Worker heartbeat | Periodic `session/list` ping | `main.go:1470-1513` |
| Worker auto-restart | Exponential backoff restart | `main.go:1516-1559` |
| Process pool | Warm ACP subprocess reuse | `main.go:347-483` |
| Session persistence | Transcript `.jsonl` + `.meta.json` | `internal/agent/subagent_store.go` |
| Continue/Fork | Resume or branch subagent runs | `subagent_store.go:250-337` |
| Cross-server parallel | `runServerGroups` | `agent.go:1487-1517` |
| Skill system | Built-in + custom, `runAs: subagent` | `internal/skill/` |
| Background jobs | `run_in_background` + `wait` | `task.go:294-335` |
| Shared directory | `_shared/` blackboard | Production layout |
| Heartbeat scheduling | Desktop scheduled prompts | `desktop/heartbeat.go` |
| Worktree isolation | `worktree` skill | `.mimocode/skills/worktree/` |
| Plan mode | Structured planning | `internal/control/` |
| Evidence verification | `complete_step` checking | `internal/evidence/` |

### 4.2 Missing for Long-Running SWE

| Gap | Impact | Complexity |
|-----|--------|-----------|
| **Durable execution loop** | Crashes lose in-progress work | High — needs Temporal or equivalent |
| **Structured progress file** | No cross-session bridge | Low — `claude-progress.txt` pattern |
| **Mission-level task lifecycle** | No week-long orchestration | Medium — TASKS.yaml + scheduler |
| **Step-level checkpointing** | Only session-level persistence | Medium — per-tool-call snapshots |
| **Self-evolving skills** | No automatic skill generation | High — pattern detection + codegen |
| **Golden principle enforcement** | No automated code quality gates | Medium — linter + background tasks |
| **Dependency-ordered merge** | No automated branch integration | Medium — git worktree + merge logic |
| **Budget enforcement** | No hard token/cost limits | Low — extend existing tracking |

## 5. Production Reference Layout

```
rx-workspaces/
├── _shared/          ← blackboard: all participants read/write
├── manager/          ← manager workspace (CWD)
├── worker-1/         ← worker-1 isolated workspace
├── worker-2/         ← worker-2 isolated workspace
└── ...
```

This is the existing production pattern for shared state coordination.

## 6. Recommendations Summary

See [LONG-RUNNING-SWE-PLAN.md](LONG-RUNNING-SWE-PLAN.md) for the implementation plan.

**Priority order** (highest impact, lowest effort first):

1. **Structured progress file** — immediate cross-session continuity (days, not weeks)
2. **Mission-level task persistence** — TASKS.yaml or equivalent for week-long work
3. **Budget enforcement** — hard limits on token usage per cycle
4. **Dependency-ordered merge** — automated branch integration after task completion
5. **Durable execution loop** — crash recovery for multi-week autonomy
6. **Self-evolving skills** — pattern detection and automatic skill generation

---
