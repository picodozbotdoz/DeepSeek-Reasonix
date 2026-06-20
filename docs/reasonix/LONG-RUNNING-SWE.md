# Long-Running Software Engineering (LRSWE)

Enable Reasonix to handle SWE tasks spanning days to weeks, using structured task management, budget enforcement, git worktree isolation, crash-recoverable execution, and self-evolving skills.

## Quick Start

### 1. Set Up Shared Directory

Create a `_shared/` directory in your project root:

```bash
mkdir -p _shared
```

This is the blackboard where all participants (manager, subagents, workers) read/write state.

### 2. Create a Mission

Use the `mission` tool to create a structured task list:

```
mission(action="create", name="Implement user authentication")
mission(action="add", task_id="T1", title="Design auth schema", done_when="schema file exists")
mission(action="add", task_id="T2", title="Implement login endpoint", depends_on=["T1"], done_when="POST /auth/login returns 200")
mission(action="add", task_id="T3", title="Write integration tests", depends_on=["T2"], done_when="all tests pass")
mission(action="start")
```

### 3. Dispatch Tasks

Check which tasks are ready and dispatch them:

```
mission(action="ready")        # see which tasks can run
mission(action="dispatch")     # get parallel_tasks arguments
```

Then use `parallel_tasks` to execute ready tasks concurrently.

### 4. Track Progress

Progress is automatically written to `_shared/PROGRESS.md` when subagents complete. View it:

```
/progress
```

### 5. Monitor Mission Status

```
/mission
```

Shows the full mission state with task statuses, dependencies, and budget.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Manager Agent (main session)                               │
│  - Reads mission, dispatches tasks                          │
│  - Manages budget, progress, worktrees                      │
└──────────────┬──────────────────────────┬───────────────────┘
               │                          │
               ▼                          ▼
┌──────────────────────┐  ┌──────────────────────────────────┐
│  Subagents (in-proc) │  │  ACP Workers (separate process)  │
│  - parallel_tasks    │  │  - delegate_task_async           │
│  - task tool         │  │  - heartbeat monitoring          │
│  - dependency DAG    │  │  - auto-restart                  │
└──────────────────────┘  └──────────────────────────────────┘
               │                          │
               ▼                          ▼
┌─────────────────────────────────────────────────────────────┐
│  Shared State (_shared/)                                    │
│  - PROGRESS.md (context bridge)                             │
│  - MISSION.toml (task tracker)                              │
│  - Checkpoints (crash recovery)                             │
└─────────────────────────────────────────────────────────────┘
```

## Components

### Progress File (Phase 1)

**File**: `_shared/PROGRESS.md`

Bridges context windows across sessions. Written automatically when subagents complete.

```markdown
# Session Progress

## Current Mission
Implement user authentication

## Completed
- [x] Design auth schema (committed: abc123)
- [x] Implement login endpoint (committed: def456)

## In Progress
- [ ] Write integration tests — 50% complete

## Next Actions
1. Finish tests
2. Run CI
```

**Auto-injected**: New subagents receive the progress content in their prompt.

### Mission System (Phase 2)

**File**: `_shared/MISSION.toml`

Structured task list with dependencies and done-conditions.

```toml
mission = "Implement user authentication"
created = "2026-06-20T10:00:00Z"
status = "in_progress"

[[tasks]]
id = "T1"
title = "Design auth schema"
status = "done"
done_when = "schema file exists and passes validation"
committed = "abc123"

[[tasks]]
id = "T2"
title = "Implement login endpoint"
status = "in_progress"
depends_on = ["T1"]
done_when = "POST /auth/login returns 200 with valid JWT"
worker = "worker-1"
```

**Mission tool actions**:
- `create` — create a new mission
- `add` — add a task with dependencies
- `start` — transition to in_progress
- `start_task` — mark a task as running
- `complete` — mark done with commit hash
- `fail` — mark failed with error
- `block` — mark blocked with reason
- `ready` — list tasks with all dependencies met
- `dispatch` — format ready tasks for `parallel_tasks`
- `status` — display full mission state

### Budget Enforcement (Phase 3)

Hard limits on token usage, cost, and turns per task/mission.

**Config** (`reasonix.toml`):
```toml
[agent]
max_tokens_per_task = 100000
max_cost_per_mission = 50.0
max_turns_per_task = 20
```

**Behavior**: When limits are exceeded, the agent loop stops after the current step with a warning notice. No partial work is lost — the cycle checkpoint preserves state.

### Worktree Isolation (Phase 4)

Each task gets its own git worktree for isolated execution.

```go
wm := agent.NewWorktreeManager("/path/to/repo", "main")
wm.CreateWorktree("T1")           // creates .worktrees/T1/ on branch feature/lrswe-T1
wm.CommitInWorktree("T1", "msg")  // stage + commit in worktree
wm.MergeWorktree("T1", agent.MergeStrategyAbort)  // merge into main
wm.MergeInDependencyOrder(tasks, strategy)          // merge in topological order
```

**Merge strategies**:
- `abort` — revert merge on conflict, leave worktree intact
- `escalate` — leave merge in progress for human resolution

### Durable Execution (Phase 5)

Crash-recoverable cycle execution with checkpoint persistence.

```
MissionOrchestrator.Run():
  while mission not complete:
    ready = mission.ReadyTasks()
    for task in ready:
      cycle = NewCycle(task.ID)
      cycle = runner.RunCycle(ctx, cycle, prompt)
      // checkpoint saved at each status transition
```

**Cycle lifecycle**: `pending → running → verifying → committing → done`

**Checkpoints**: Persisted to `_shared/checkpoints/` as JSON files. On restart, `Incomplete()` finds interrupted cycles for resume.

**Recovery**: Load last checkpoint → verify git state → resume from last completed step.

### Self-Evolving Skills (Phase 6)

Automatic pattern detection and skill generation from tool call sequences.

```go
// Detect patterns across sessions
pd := agent.NewPatternDetector(3, 0.6, 5)  // min 3 occurrences, 60% confidence
patterns := pd.DetectPatterns(records)

// Generate skill from pattern
sg := agent.NewSkillGenerator("auto")
skill := sg.Generate(pattern)
fmt.Println(skill.ToSkillFrontmatter())  // installable skill file

// Evolve existing skill with feedback
evolved := sg.Evolve(skill, agent.SkillFeedback{
    BetterDescription: "Improved description",
    AdditionalSteps:   []string{"new step"},
})
```

## Configuration

### reasonix.toml

```toml
[agent]
# Budget limits (0 = unlimited)
max_tokens_per_task = 100000
max_cost_per_mission = 50.0
max_turns_per_task = 20

# Subagent model override
subagent_model = "deepseek-pro"
```

### Directory Structure

```
project/
├── _shared/
│   ├── PROGRESS.md          # auto-managed context bridge
│   ├── MISSION.toml         # task tracker
│   └── checkpoints/         # crash recovery state
├── .worktrees/              # git worktrees per task
│   ├── T1/                  # task T1 worktree
│   └── T2/                  # task T2 worktree
└── reasonix.toml            # config
```

## Workflow Example

### Multi-Day Feature Implementation

```
# Day 1: Plan and start
mission(action="create", name="Implement payment system")
mission(action="add", task_id="T1", title="Design payment schema", done_when="schema validated")
mission(action="add", task_id="T2", title="Implement Stripe integration", depends_on=["T1"])
mission(action="add", task_id="T3", title="Implement PayPal integration", depends_on=["T1"])
mission(action="add", task_id="T4", title="Write integration tests", depends_on=["T2", "T3"])
mission(action="start")

# Day 1: Dispatch T1
mission(action="ready")  # → T1 is ready
# Use parallel_tasks to execute T1

# Day 2: T1 complete, dispatch T2 and T3 in parallel
mission(action="complete", task_id="T1", commit="abc123")
mission(action="ready")  # → T2 and T3 are ready
# parallel_tasks dispatches both

# Day 3: All tasks complete
mission(action="complete", task_id="T2", commit="def456")
mission(action="complete", task_id="T3", commit="ghi789")
mission(action="ready")  # → T4 is ready
# Execute T4

# Day 4: Mission complete
mission(action="complete", task_id="T4", commit="jkl012")
/mission  # Shows: [completed] 4 tasks: 4 done
```

### Crash Recovery

If the agent crashes mid-cycle:

1. On restart, `CheckpointStore.Incomplete()` finds interrupted cycles
2. Load last checkpoint → verify git state (worktree intact?)
3. Resume from the last completed step
4. Mission state is preserved in MISSION.toml — no work lost

### Parallel Task Execution

```
# Dispatch multiple independent tasks
mission(action="dispatch")
# Returns parallel_tasks arguments for ready tasks

parallel_tasks(tasks=[
  {prompt: "Implement auth module", description: "T2: auth"},
  {prompt: "Implement API layer", description: "T3: api"}
])
```

Tasks with dependencies are dispatched only after their prerequisites complete.

## API Reference

### Mission Tool

| Action | Parameters | Returns |
|--------|-----------|---------|
| `create` | `name`, `max_tokens?`, `max_cost_usd?` | Mission created |
| `add` | `task_id`, `title`, `depends_on?`, `done_when?` | Task added |
| `start` | — | Mission started |
| `start_task` | `task_id`, `worker?` | Task started |
| `complete` | `task_id`, `commit?` | Task completed |
| `fail` | `task_id`, `error` | Task failed |
| `block` | `task_id`, `reason` | Task blocked |
| `ready` | — | List ready tasks |
| `dispatch` | — | parallel_tasks arguments |
| `status` | — | Full mission report |

### Slash Commands

| Command | Description |
|---------|-------------|
| `/progress` | Display current PROGRESS.md state |
| `/mission` | Display current MISSION.toml state |

### Go API

```go
// Progress file
pf := agent.NewProgressFile("_shared")
pf.Write(state)
state := pf.Read()

// Mission
mm := agent.NewMissionManager("_shared/MISSION.toml")
mm.CreateMission("name", tasks)
mm.CompleteTask("T1", "commit-hash")

// Budget
bt := agent.NewBudgetTracker(agent.BudgetLimits{MaxTokens: 100000})
status := bt.Check()

// Worktree
wm := agent.NewWorktreeManager("/repo", "main")
wm.CreateWorktree("T1")
wm.MergeInDependencyOrder(tasks, agent.MergeStrategyAbort)

// Checkpoint
cs := agent.NewCheckpointStore("_shared/checkpoints")
cs.Save(checkpoint)
incomplete, _ := cs.Incomplete()

// Pattern detection
pd := agent.NewPatternDetector(3, 0.6, 5)
patterns := pd.DetectPatterns(records)

// Skill generation
sg := agent.NewSkillGenerator("auto")
skill := sg.Generate(pattern)
```

## Design Principles

1. **Hub-and-spoke**: Manager is the sole coordination point. Subagents and workers never communicate directly.
2. **File-as-bus**: State flows through shared files (`_shared/`), not conversation history.
3. **Small cycles**: Each cycle is deliberately small — max loss from crash is one unfinished cycle.
4. **Deterministic verification**: Only verified work is marked complete.
5. **Git as source of truth**: All code changes are committed, worktrees provide isolation.

## File Reference

| File | Purpose | Format |
|------|---------|--------|
| `_shared/PROGRESS.md` | Context bridge across sessions | Markdown |
| `_shared/MISSION.toml` | Task tracker with dependencies | TOML |
| `_shared/checkpoints/*.json` | Crash recovery state | JSON |
| `.worktrees/<taskID>/` | Isolated git worktrees | Git |
| `reasonix.toml` | Configuration | TOML |

## Test Coverage

| Phase | Tests | Status |
|-------|-------|--------|
| Phase 1: Progress File | 9 | ✅ |
| Phase 2: Mission Persistence | 15 | ✅ |
| Phase 3: Budget Enforcement | 14 | ✅ |
| Phase 4: Dependency-Ordered Merge | 12 | ✅ |
| Phase 5: Durable Execution | 13 | ✅ |
| Phase 6: Self-Evolving Skills | 14 | ✅ |
| **Total** | **77** | **All passing** |
