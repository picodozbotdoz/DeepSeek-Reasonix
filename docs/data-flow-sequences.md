# Data Flow & Sequence Diagrams

This document provides detailed data flow diagrams and sequence descriptions for the key operations in Reasonix. These diagrams trace the path of data through the system, from user input to final output, illustrating how the major subsystems interact.

## 1. Interactive Chat Turn

```
User          TUI/Desktop/HTTP     Controller         Agent            Provider        Tools
 │                 │                    │                 │                 │              │
 │  type message   │                    │                 │                 │              │
 │────────────────►│                    │                 │                 │              │
 │                 │  Submit(input)     │                 │                 │              │
 │                 │───────────────────►│                 │                 │              │
 │                 │                    │ Compose()       │                 │              │
 │                 │                    │ (memory, plan,  │                 │              │
 │                 │                    │  @-refs)        │                 │              │
 │                 │                    │                 │                 │              │
 │                 │                    │ beginCheckpoint │                 │              │
 │                 │                    │                 │                 │              │
 │                 │                    │ runner.Run()    │                 │              │
 │                 │                    │────────────────►│                 │              │
 │                 │                    │                 │ Add(user msg)   │              │
 │                 │                    │                 │                 │              │
 │                 │                    │                 │ ┌─── Loop ────┐│              │
 │                 │                    │                 │ │              ││              │
 │                 │                    │                 │ │ Stream()     ││              │
 │                 │                    │                 │ │──────────────►│              │
 │                 │  Emit(Text)        │  Emit(Text)     │ │              ││              │
 │ │◄──────────────│◄──────────────────│◄────────────────│ │  chunks      ││              │
 │                 │                    │                 │ │◄─────────────││              │
 │                 │                    │                 │ │              ││              │
 │                 │                    │                 │ │ tool calls?  ││              │
 │                 │                    │                 │ │─ yes ────────││              │
 │                 │                    │                 │ │              ││              │
 │                 │                    │                 │ │ executeBatch ││              │
 │                 │                    │                 │ │─────────────────────────────►│
 │                 │  Emit(ToolResult)  │  Emit(ToolDisp) │ │  results     ││              │
 │ │◄──────────────│◄──────────────────│◄────────────────│◄──────────────────────────────│
 │                 │                    │                 │ │              ││              │
 │                 │                    │                 │ │ maybeCompact ││              │
 │                 │                    │                 │ │              ││              │
 │                 │                    │                 │ └── repeat ───┘│              │
 │                 │                    │                 │                 │              │
 │                 │                    │  Emit(TurnDone) │                 │              │
 │ │◄──────────────│◄──────────────────│◄────────────────│                 │              │
```

## 2. Plan Mode Flow

```
User          Frontend          Controller         Agent
 │               │                   │                 │
 │  /plan on     │                   │                 │
 │──────────────►│  SetPlanMode(true)│                 │
 │               │──────────────────►│                 │
 │               │                   │ SetPlanMode(true)│
 │               │                   │────────────────►│
 │               │                   │                 │
 │  "refactor X" │                   │                 │
 │──────────────►│  Send(input)      │                 │
 │               │──────────────────►│                 │
 │               │                   │ runTurn()       │
 │               │                   │────────────────►│
 │               │                   │                 │ Run() — writers blocked
 │  [plan text]  │  Emit(Text)       │  Emit(Text)     │ │
 │◄──────────────│◄──────────────────│◄────────────────│ │
 │               │                   │                 │
 │               │                   │ requestApproval()│
 │  [approve?]   │  ApprovalRequest  │                 │
 │◄──────────────│◄──────────────────│                 │
 │               │                   │                 │
 │  [approve]    │  Approve(id,true) │                 │
 │──────────────►│──────────────────►│                 │
 │               │                   │ SetPlanMode(false)│
 │               │                   │ seedPlanTodos() │
 │               │                   │ autoApprove=true│
 │               │                   │ Run(planMsg)    │
 │               │                   │────────────────►│
 │  [execution]  │                   │  Emit(ToolDisp) │ writers auto-approved
 │◄──────────────│◄──────────────────│◄────────────────│
 │               │                   │                 │
 │               │                   │  Emit(TurnDone) │
 │◄──────────────│◄──────────────────│◄────────────────│
```

## 3. Two-Model Collaboration (Coordinator)

```
User          Frontend          Controller       Coordinator       Planner        Executor
 │               │                   │                 │               │              │
 │  "implement X"│                   │                 │               │              │
 │──────────────►│  Send(input)      │                 │               │              │
 │               │──────────────────►│                 │               │              │
 │               │                   │ Run(input)      │               │              │
 │               │                   │────────────────►│               │              │
 │               │                   │                 │               │              │
 │               │                   │                 │ shouldPlan?   │              │
 │               │                   │                 │─ yes ────────►│              │
 │               │                   │                 │               │              │
 │               │  Emit(Phase:      │  Emit(Phase:    │ plan()        │              │
 │               │   "planning")     │   "planning")   │──────────────►│              │
 │◄──────────────│◄──────────────────│◄────────────────│               │              │
 │               │                   │                 │  [plan text]  │              │
 │               │                   │                 │◄──────────────│              │
 │               │                   │                 │               │              │
 │               │  Emit(Phase:      │  Emit(Phase:    │ formatHandoff │              │
 │               │   "executing")    │   "executing")  │───────────────│─────────────►│
 │◄──────────────│◄──────────────────│◄────────────────│               │              │
 │               │                   │                 │               │  Run(task+plan)│
 │  [execution]  │                   │                 │               │  │           │
 │◄──────────────│◄──────────────────│◄────────────────│               │  └─tool loop │
 │               │                   │                 │               │              │
 │               │                   │  Emit(TurnDone) │               │              │
 │◄──────────────│◄──────────────────│◄────────────────│               │              │
```

## 4. Permission Gate Flow

```
Agent                  Gate                    Policy               Approver (TUI)
  │                      │                        │                       │
  │  executeOne(call)    │                        │                       │
  │─────────────────────►│                        │                       │
  │                      │                        │                       │
  │                      │  Plan mode check       │                       │
  │                      │  Writer? → Block       │                       │
  │                      │                        │                       │
  │                      │  Decide(tool, readOnly, │                       │
  │                      │          args)          │                       │
  │                      │───────────────────────►│                       │
  │                      │                        │                       │
  │                      │  ┌─ Precedence ──────┐ │                       │
  │                      │  │ Deny → Deny        │ │                       │
  │                      │  │ Ask  → Ask         │ │                       │
  │                      │  │ Allow→ Allow       │ │                       │
  │                      │  │ Fallback            │ │                       │
  │                      │  └────────────────────┘ │                       │
  │                      │                        │                       │
  │                      │  Decision: Ask         │                       │
  │                      │                        │                       │
  │                      │  Approve(ctx, tool,    │                       │
  │                      │           subject)     │                       │
  │                      │───────────────────────────────────────────────►│
  │                      │                        │     [approval UI]    │
  │                      │                        │                       │
  │                      │  allow, remember       │                       │
  │                      │◄───────────────────────────────────────────────│
  │                      │                        │                       │
  │  allow=true          │                        │                       │
  │◄─────────────────────│                        │                       │
  │                      │                        │                       │
  │  Execute(tool)       │                        │                       │
  │─── ... ─────────────►│                        │                       │
```

## 5. Compaction Flow

```
Agent                  Session                Provider
  │                      │                       │
  │  maybeCompact()      │                       │
  │  ratio exceeded?     │                       │
  │─ yes ───────────────►│                       │
  │                      │                       │
  │  compact()           │                       │
  │                      │                       │
  │  Identify boundary   │                       │
  │  (old vs recent)     │                       │
  │                      │                       │
  │  Build summary       │                       │
  │  prompt              │                       │
  │                      │  Stream(summary       │
  │                      │   request)            │
  │                      │──────────────────────►│
  │                      │                       │
  │                      │  [summary text]       │
  │                      │◄──────────────────────│
  │                      │                       │
  │  Replace messages:   │                       │
  │  system + summary +  │                       │
  │  recentKeep          │                       │
  │─────────────────────►│                       │
  │                      │  IncrementRewrite()   │
  │                      │                       │
  │  Archive originals   │                       │
  │──────────────────► [archive file]            │
```

## 6. MCP Plugin Startup

```
Boot                   Host                   Client (stdio)         MCP Server Process
  │                      │                        │                       │
  │  StartAvailable()    │                        │                       │
  │─────────────────────►│                        │                       │
  │                      │                        │                       │
  │                      │  Phase A: start()      │                       │
  │                      │───────────────────────►│                       │
  │                      │                        │  newStdioTransport()  │
  │                      │                        │──────────────────────►│
  │                      │                        │  [subprocess starts]  │
  │                      │                        │                       │
  │                      │                        │  initialize()         │
  │                      │                        │──────────────────────►│
  │                      │                        │  [capabilities]       │
  │                      │                        │◄──────────────────────│
  │                      │                        │                       │
  │                      │                        │  notifications/       │
  │                      │                        │  initialized          │
  │                      │                        │──────────────────────►│
  │                      │                        │                       │
  │                      │                        │  tools/list           │
  │                      │                        │──────────────────────►│
  │                      │                        │  [tool schemas]       │
  │                      │                        │◄──────────────────────│
  │                      │                        │                       │
  │                      │  tools []tool.Tool     │                       │
  │                      │◄───────────────────────│                       │
  │                      │                        │                       │
  │  [tools registered]  │                        │                       │
  │◄─────────────────────│                        │                       │
  │                      │                        │                       │
  │                      │  Phase B (async):      │                       │
  │                      │  prompts/list          │                       │
  │                      │  resources/list        │                       │
  │                      │───────────────────────►│──────────────────────►│
  │                      │  [surfaces stream in]  │                       │
```

## 7. Checkpoint & Rewind

```
Controller            Checkpoint Store         Agent              File System
  │                      │                       │                    │
  │  beginCheckpoint()   │                       │                    │
  │─────────────────────►│                       │                    │
  │                      │  Begin(turn, prompt)  │                    │
  │                      │──────────────────────►│                    │
  │                      │                       │                    │
  │  runTurn()           │                       │                    │
  │─────────────────────────────────────────────►│                    │
  │                      │                       │                    │
  │                      │                       │  executeOne()      │
  │                      │                       │  Previewer.Preview │
  │                      │  Snapshot(change)     │                    │
  │                      │◄──────────────────────│                    │
  │                      │  [record pre-edit]    │                    │
  │                      │                       │  Execute(tool)     │
  │                      │                       │───────────────────►│
  │                      │                       │                    │
  │  ... more turns ...  │                       │                    │
  │                      │                       │                    │
  │  Rewind(turn, Both)  │                       │                    │
  │─────────────────────►│                       │                    │
  │                      │  RestoreCode(turn)    │                    │
  │                      │───────────────────────────────────────────►│
  │                      │  [files restored]     │                    │
  │                      │◄──────────────────────────────────────────│
  │                      │                       │                    │
  │  Truncate messages   │                       │                    │
  │─────────────────────────────────────────────►│                    │
  │                      │                       │                    │
  │  Emit(Notice)        │                       │                    │
  │◄─────────────────────│                       │                    │
```

## See Also

- [Architecture Overview](architecture-overview.md)
- [Agent Loop & Coordinator](agent-loop-coordinator.md)
- [Controller & Frontends](controller-frontends.md)
- [MCP Plugin System](mcp-plugin-system.md)
- [Permission & Sandbox](permission-sandbox.md)
- [Checkpoint & Rewind](checkpoints-rewind.md)
