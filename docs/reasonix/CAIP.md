# Capability Assessment & Implementation Protocol (CAIP)

**Scope**: Reasonix project
**Version**: 1.0
**Date**: 2026-06-20

---

## Purpose

This protocol standardizes how Reasonix evaluates, plans, and implements new capabilities. It defines the terminology, artifacts, phases, and conventions that all capability initiatives must follow.

## 1. Terminology

| Term | Definition |
|------|-----------|
| **Capability** | A user-facing feature or system-level improvement that extends Reasonix's functionality |
| **Initiative** | A bounded effort to deliver one or more related capabilities |
| **Phase** | A deliverable stage within an initiative, independently shippable |
| **Task** | A discrete unit of work within a phase |
| **Artifact** | A document produced during the initiative (research, plan, protocol, spec) |
| **Owner** | The person or agent responsible for an initiative |
| **Tracker** | The plan document that records task status throughout implementation |
| **Gate** | A validation checkpoint that must pass before advancing to the next phase |

## 2. Artifact Types

Every initiative produces these documents, in order:

| Artifact | Purpose | Naming Convention | Location |
|----------|---------|-------------------|----------|
| **Research** | Industry analysis, capability inventory, gap analysis | `<NAME>-RESEARCH.md` | `docs/reasonix/` |
| **Plan** | Phased implementation with task tracking | `<NAME>-PLAN.md` | `docs/reasonix/` |
| **Spec** | Technical design for a specific phase (optional, for complex phases) | `REQ_DESIGN_<NAME>.md` | `docs/reasonix/` |
| **Protocol** | Process governance (this document) | `CAIP.md` | `docs/reasonix/` |

### Naming Convention

- All caps with hyphens: `LONG-RUNNING-SWE-RESEARCH.md`
- Prefix with initiative abbreviation: `LRSWE-PLAN.md` or descriptive: `LONG-RUNNING-SWE-PLAN.md`
- Chinese translations use `.zh-CN.md` suffix when needed

## 3. Initiative Lifecycle

```
┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐    ┌──────────┐
│ ASSESS   │───▶│ PLAN     │───▶│ BUILD    │───▶│ VERIFY   │───▶│ SHIP     │
│          │    │          │    │          │    │          │    │          │
│ Research │    │ Phases   │    │ Implement│    │ Test     │    │ Merge    │
│ Gaps     │    │ Tasks    │    │ Code     │    │ Lint     │    │ Document │
│ Options  │    │ Tracker  │    │ Config   │    │ Typecheck│    │ Release  │
└──────────┘    └──────────┘    └──────────┘    └──────────┘    └──────────┘
```

### Stage 1: ASSESS

**Input**: User request, problem statement, or opportunity
**Output**: Research document (`<NAME>-RESEARCH.md`)

Activities:
1. Search industry for existing solutions and patterns
2. Inventory current Reasonix capabilities relevant to the initiative
3. Identify gaps between current state and desired state
4. Evaluate options (build vs buy, complexity, risk)
5. Produce research document with findings and recommendations

**Gate**: Research document reviewed and approved by owner.

### Stage 2: PLAN

**Input**: Approved research document
**Output**: Plan document (`<NAME>-PLAN.md`)

Activities:
1. Break initiative into phases (each independently shippable)
2. Define deliverables per phase (tasks with status tracking)
3. Identify dependencies between phases
4. Estimate effort and risk per phase
5. Define open questions and design decisions needed
6. Produce plan document as implementation tracker

**Gate**: Plan document reviewed, phases prioritized, open questions resolved or deferred.

### Stage 3: BUILD

**Input**: Approved plan document
**Output**: Working code changes

Activities:
1. Start with Phase 1 (lowest risk, highest impact)
2. Update plan tracker as tasks complete
3. Follow Reasonix code conventions (SPEC.md)
4. Write tests for all new functionality
5. Run lint and typecheck after each task

**Gate**: All tasks in phase complete, all tests pass.

### Stage 4: VERIFY

**Input**: Completed code changes
**Output**: Verification evidence

Activities:
1. Run full test suite
2. Run lint (`golangci-lint`)
3. Run typecheck (Go vet, frontend tsc)
4. Manual testing of new functionality
5. Security review (if applicable)
6. Performance check (if applicable)

**Gate**: All verification checks pass. Evidence recorded.

### Stage 5: SHIP

**Input**: Verified code changes
**Output**: Merged code, updated documentation

Activities:
1. Create commit with descriptive message
2. Push to branch
3. Create PR (if applicable)
4. Update relevant documentation
5. Update CHANGELOG (if applicable)
6. Close initiative tracker

**Gate**: PR approved, CI passes, documentation updated.

## 4. Phase Structure

Each phase within an initiative follows this structure:

```markdown
## Phase N: <Name>

**Goal**: <one-line description>
**Effort**: <estimate>
**Risk**: Low | Medium | High
**Dependencies**: <which phases must complete first>

### Deliverables

| # | Task | Status | Files |
|---|------|--------|-------|
| N.1 | <task description> | ⬜ | <affected files> |
| N.2 | <task description> | ⬜ | <affected files> |

### Design Details

<architecture, data flow, key decisions>

### Open Questions

<decisions needed before or during implementation>
```

### Task Status Values

| Status | Meaning |
|--------|---------|
| ⬜ | Not started |
| 🔄 | In progress |
| ✅ | Complete |
| ❌ | Blocked |
| ⏭️ | Skipped (with reason) |

## 5. Tracking Conventions

### Plan Document as Tracker

The plan document is the single source of truth for initiative progress. Update it directly — no separate tracking tools needed.

**When to update**:
- After completing a task: change ⬜ to ✅
- When starting a task: change ⬜ to 🔄
- When blocked: change to ❌ with reason in notes
- When phase completes: update cross-phase tracking table

### Progress File (for long-running work)

For initiatives spanning multiple sessions, maintain a progress file at `_shared/PROGRESS.md`:

```markdown
# Initiative Progress

## Current Phase
Phase 2: Mission-Level Task Persistence

## Completed
- Phase 1: Structured Progress File ✅
  - Task 1.1: Progress file format defined
  - Task 1.2: ProgressWriter implemented

## In Progress
- Task 2.1: Mission file format (50% complete)

## Blocked
- (none)

## Next Actions
1. Complete MissionManager implementation
2. Add mission tool to agent
```

## 6. Design Document Format

For complex phases requiring detailed design, produce a `REQ_DESIGN_<NAME>.md`:

```markdown
# <Feature Name> — Design Document

**Feature**: <feature name>
**Status**: Design | In Review | Approved | Implemented
**Date**: <date>

---

## Problem

<what problem does this solve?>

## Goal

<what is the desired outcome?>

## Use Cases

| Use Case | Trigger | What It Produces |
|----------|---------|-----------------|
| <case> | <trigger> | <output> |

---

## Design

### Architecture

<how it works, data flow, key components>

### Configuration

<config schema, file locations, defaults>

### API Surface

<new tools, methods, or interfaces>

---

## Implementation Notes

<files to create/modify, dependencies, risks>

## Testing

<what to test, how to test, edge cases>
```

## 7. Code Conventions for New Capabilities

All new code must follow existing Reasonix conventions:

- **Interface-first**: define `interface` before implementation
- **Registry-based**: self-register via `init()` for built-ins
- **Config-driven**: expose configuration via `reasonix.toml`
- **Test-required**: every new function needs a test
- **English-only**: code, comments, docs in English
- **No new dependencies**: prefer standard library; justify any addition

## 8. Review Checklist

Before marking a phase complete:

- [ ] All tasks in phase marked ✅
- [ ] All tests pass (`go test ./...`)
- [ ] Lint passes (`golangci-lint run`)
- [ ] Typecheck passes (`go vet ./...`)
- [ ] No new dependencies without justification
- [ ] Documentation updated (if user-facing)
- [ ] Plan tracker updated with final status
- [ ] Progress file updated (if multi-session work)

## 9. Example Initiative

```
Initiative: LRSWE (Long-Running Software Engineering)
Research:   docs/reasonix/LONG-RUNNING-SWE-RESEARCH.md
Plan:       docs/reasonix/LONG-RUNNING-SWE-PLAN.md
Phases:     6 (Progress File → Mission → Budget → Merge → Durable Execution → Skills)
Status:     Planning
```

## 10. Protocol Evolution

This protocol is a living document. Update it when:
- A new artifact type is needed
- A phase structure proves inadequate
- A new gate or review step is required
- Naming conventions change

---
