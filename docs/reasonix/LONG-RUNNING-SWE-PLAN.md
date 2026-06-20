# Long-Running SWE — Implementation Plan

**Initiative**: LRSWE (Long-Running Software Engineering)
**Status**: Planning
**Date**: 2026-06-20
**Tracking**: This file is the implementation tracker. Update status as phases complete.

---

## Overview

Enable Reasonix to handle SWE tasks spanning days to weeks, using existing primitives (subagents, ACP workers, shared directory, skills) plus targeted additions. Each phase is independently shippable and builds on the previous.

## Architecture Principle

**Hub-and-spoke**: the Manager is the sole coordination point. Subagents and workers never communicate directly — all orchestration flows through the Manager. Shared state flows through `_shared/` (blackboard pattern).

## Phase 1: Structured Progress File (Days)

**Goal**: Bridge context windows with a persistent progress file, enabling session-to-session continuity.

**Effort**: 1-2 days
**Risk**: Low
**Dependencies**: None

### Deliverables

| # | Task | Status | Files |
|---|------|--------|-------|
| 1.1 | Define progress file format (`PROGRESS.md`) | ✅ | `internal/agent/progress.go` |
| 1.2 | Add `ProgressWriter` to subagent_store — auto-write on subagent completion | ✅ | `internal/agent/task.go` |
| 1.3 | Add progress file injection into subagent system prompt on resume | ✅ | `internal/agent/task.go` (`ProgressContext()`) |
| 1.4 | Add `/progress` slash command to view current state | ✅ | `internal/control/controller.go`, `internal/control/slash.go` |
| 1.5 | Write tests for progress file round-trip | ✅ | `internal/agent/progress_test.go` |

### Progress File Format

```markdown
# Session Progress

## Current Mission
<one-line description of the overall goal>

## Completed
- [x] <task description> (committed: abc123)
- [x] <task description> (committed: def456)

## In Progress
- [ ] <task description> — <current state>

## Blocked
- <blocker description>

## Key Decisions
- <decision and rationale>

## Files Changed
- `path/to/file.go` — <what changed>

## Next Actions
1. <immediate next step>
2. <step after that>
```

### Design Details

- Progress file lives at `_shared/PROGRESS.md` (shared) or `<session>/PROGRESS.md` (session-local)
- Written automatically when a subagent completes or on explicit `/checkpoint` command
- Read automatically when a subagent starts (injected as context prefix)
- Format is markdown for human readability and diff-ability
- Git-friendly: can be committed alongside code changes

---

## Phase 2: Mission-Level Task Persistence (1-2 Weeks)

**Goal**: Orchestrate multi-day work with a persistent task list, dependencies, and verifiable done-conditions.

**Effort**: 3-5 days
**Risk**: Medium
**Dependencies**: Phase 1

### Deliverables

| # | Task | Status | Files |
|---|------|--------|-------|
| 2.1 | Define mission file format (`MISSION.toml`) | ✅ | `internal/agent/mission.go`, `mission_toml.go` |
| 2.2 | Add `MissionManager` — load, query, update mission state | ✅ | `internal/agent/mission.go` |
| 2.3 | Add `mission` tool — manager can create/update/query missions | ✅ | `internal/agent/mission_tool.go` |
| 2.4 | Integrate with `parallel_tasks` — mission tasks auto-dispatch | ⬜ | `internal/agent/parallel_tasks.go` |
| 2.5 | Add mission status dashboard (CLI + desktop) | ⬜ | — |
| 2.6 | Write tests | ✅ | `internal/agent/mission_test.go` |

### Mission File Format

```yaml
# _shared/MISSION.yaml
mission: "Implement user authentication system"
created: 2026-06-20T10:00:00Z
status: in_progress

tasks:
  - id: T1
    title: "Design auth schema"
    status: done
    depends_on: []
    done_when: "schema file exists and passes validation"
    committed: "abc123"
    worker: null

  - id: T2
    title: "Implement login endpoint"
    status: in_progress
    depends_on: [T1]
    done_when: "POST /auth/login returns 200 with valid JWT"
    started: 2026-06-20T11:00:00Z
    worker: "worker-1"

  - id: T3
    title: "Write integration tests"
    status: blocked
    depends_on: [T2]
    done_when: "all tests pass in CI"
    blocked_by: "waiting for T2 completion"

budget:
  max_tokens: 1000000
  spent_tokens: 250000
  max_cost_usd: 50.00
  spent_cost_usd: 12.50
```

### Design Details

- Mission file is the single source of truth for task status
- Manager reads mission, dispatches ready tasks to workers/subagents
- Workers report completion by updating mission file (via `_shared/`)
- `done_when` field enables deterministic verification
- Budget tracking per mission with hard enforcement
- Task dependencies form a DAG — manager resolves execution order

---

## Phase 3: Budget Enforcement (1 Week)

**Goal**: Hard limits on token usage and cost per mission, per task, and per session.

**Effort**: 2-3 days
**Risk**: Low
**Dependencies**: Phase 2

### Deliverables

| # | Task | Status | Files |
|---|------|--------|-------|
| 3.1 | Add `BudgetTracker` — incremental cost tracking with limits | ⬜ | `internal/agent/budget.go` |
| 3.2 | Add budget check before each LLM call | ⬜ | `internal/agent/agent.go` |
| 3.3 | Add `[agent]` config fields: `max_tokens_per_task`, `max_cost_per_mission` | ⬜ | `internal/config/config.go` |
| 3.4 | Add budget exhaustion handling — pause, notify, or abort | ⬜ | — |
| 3.5 | Add budget dashboard to mission status | ⬜ | — |
| 3.6 | Write tests | ⬜ | — |

---

## Phase 4: Dependency-Ordered Merge (1-2 Weeks)

**Goal**: After tasks complete in isolated worktrees, automatically merge in dependency order.

**Effort**: 3-5 days
**Risk**: Medium
**Dependencies**: Phase 2

### Deliverables

| # | Task | Status | Files |
|---|------|--------|-------|
| 4.1 | Add `WorktreeManager` — create/merge/delete worktrees per task | ⬜ | `internal/agent/worktree_mgr.go` |
| 4.2 | Auto-create worktree when task starts, auto-merge when done | ⬜ | — |
| 4.3 | Add conflict resolution strategy — auto-merge, skip, or escalate | ⬜ | — |
| 4.4 | Add merge verification — tests pass after merge | ⬜ | — |
| 4.5 | Integrate with mission lifecycle | ⬜ | — |
| 4.6 | Write tests | ⬜ | — |

### Design Details

- Each task gets its own git worktree: `git worktree add ../task-T1 -b feature/T1`
- Task executes in worktree, commits changes
- On completion: `git merge feature/T1` into main in dependency order
- If merge conflict: pause, notify manager, escalate to human
- After merge: run verification suite, only mark done if tests pass

---

## Phase 5: Durable Execution Loop (2-3 Weeks)

**Goal**: Agent loop survives crashes, reboots, and context window limits. The system runs for weeks while the model drives in verified bursts.

**Effort**: 7-10 days
**Risk**: High
**Dependencies**: Phases 1-4

### Deliverables

| # | Task | Status | Files |
|---|------|--------|-------|
| 5.1 | Design durable execution architecture | ⬜ | — |
| 5.2 | Implement `Cycle` abstraction — one unit of work (task + verify + commit) | ⬜ | `internal/agent/cycle.go` |
| 5.3 | Implement `CycleRunner` — execute cycle, persist state, handle crash | ⬜ | `internal/agent/cycle_runner.go` |
| 5.4 | Implement checkpoint system — state persisted at cycle boundaries | ⬜ | — |
| 5.5 | Implement recovery — resume from last checkpoint on startup | ⬜ | — |
| 5.6 | Implement mission orchestrator — read mission, dispatch cycles | ⬜ | `internal/agent/mission_orchestrator.go` |
| 5.7 | Add systemd/supervisor integration for auto-restart | ⬜ | — |
| 5.8 | Write crash-recovery tests | ⬜ | — |

### Design Details

```
┌─────────────────────────────────────────────────┐
│  Mission Orchestrator (runs continuously)        │
│  Reads MISSION.yaml, dispatches cycles           │
└──────────────────────┬──────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────┐
│  Cycle                                          │
│  1. Read task from mission                       │
│  2. Create/resume worktree                       │
│  3. Run model (bounded turns)                    │
│  4. Verify (tests, lint, done_when)              │
│  5. Commit + merge                               │
│  6. Update mission file                          │
│  7. Checkpoint state                             │
└──────────────────────┬──────────────────────────┘
                       │ crash?
                       ▼
┌─────────────────────────────────────────────────┐
│  Recovery                                       │
│  1. Load last checkpoint                         │
│  2. Verify git state (worktree intact?)          │
│  3. Resume from step 3 or 4                      │
└─────────────────────────────────────────────────┘
```

- Each cycle is deliberately small — max loss from crash is one unfinished cycle
- State checkpointed at cycle boundaries (not per-tool-call) for simplicity
- Model runs in bounded bursts (max N turns per cycle)
- Deterministic verification is the only thing allowed to call work "done"

---

## Phase 6: Self-Evolving Skills (3-4 Weeks)

**Goal**: Agent detects repeated patterns and automatically generates reusable skills.

**Effort**: 10-14 days
**Risk**: High
**Dependencies**: Phase 5

### Deliverables

| # | Task | Status | Files |
|---|------|--------|-------|
| 6.1 | Add pattern detection — analyze tool call sequences across sessions | ⬜ | — |
| 6.2 | Add skill generation — convert pattern to skill template | ⬜ | — |
| 6.3 | Add skill validation — test generated skill before committing | ⬜ | — |
| 6.4 | Add skill registry integration — install generated skill | ⬜ | — |
| 6.5 | Add skill evolution — improve existing skills based on usage data | ⬜ | — |
| 6.6 | Write tests | ⬜ | — |

---

## Cross-Phase Tracking

| Phase | Status | Start | End | Blocked By |
|-------|--------|-------|-----|-----------|
| Phase 1: Progress File | ⬜ Not Started | — | — | — |
| Phase 2: Mission Persistence | ⬜ Not Started | — | — | Phase 1 |
| Phase 3: Budget Enforcement | ⬜ Not Started | — | — | Phase 2 |
| Phase 4: Dependency-Ordered Merge | ⬜ Not Started | — | — | Phase 2 |
| Phase 5: Durable Execution | ⬜ Not Started | — | — | Phases 1-4 |
| Phase 6: Self-Evolving Skills | ⬜ Not Started | — | — | Phase 5 |

## Open Questions

1. **Durable execution engine**: Temporal (external dependency, heavy) vs simpler checkpoint-resume (SQLite, lighter)? Temporal is proven but adds infra. SQLite checkpointing is lighter but requires custom replay logic.
2. **Mission file format**: YAML vs JSON vs TOML? YAML is most human-readable for task lists. TOML is already a Reasonix dependency.
3. **Budget granularity**: Per-task, per-mission, per-session, or all three? All three gives most flexibility.
4. **Merge strategy**: Auto-merge with conflict detection, or always escalate conflicts to human? Auto-merge is faster but riskier.
5. **Skill evolution safety**: How to prevent generated skills from introducing regressions? Need a validation gate.

## Terminology

| Term | Definition |
|------|-----------|
| **Mission** | A multi-day SWE task with structured tasks, dependencies, and budget |
| **Cycle** | One unit of work: task + execute + verify + commit + checkpoint |
| **Progress File** | Persistent markdown file bridging context windows across sessions |
| **Blackboard** | Shared directory (`_shared/`) where all participants read/write state |
| **Durable Execution** | System-level crash recovery ensuring no work is lost |
| **Golden Principle** | Mechanical rule encoded in repo to keep codebase legible for agents |
| **Done-Condition** | Verifiable criterion that must pass before a task is marked complete |
| **Worktree Isolation** | Each task executes in its own git worktree to avoid conflicts |

---
