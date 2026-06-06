# Background Jobs

**Source:** `internal/jobs/jobs.go`

## Overview

The background jobs system powers the agent's ability to run long-running operations asynchronously: `bash run_in_background` and `task run_in_background` launch processes that continue across turns, while `bash_output`, `kill_shell`, and `wait` provide control and observation. A `Manager` owns a session-scoped context whose lifetime spans the entire session — not a single turn — so a job started in one turn keeps running across subsequent turns and is only cancelled when the controller closes or `kill_shell` is called.

This design is essential for real-world coding workflows. A test suite might take 30 seconds to run; a build might take minutes; a sub-agent task might need to iterate through multiple tool calls. Without background execution, the agent would block on each operation, unable to respond to the user or start other work. With background jobs, the agent launches the operation, continues the conversation, and checks back for results when it needs them.

---

## Manager

The `Manager` is the session's background-job registry. It is safe for concurrent use — tool calls from parallel execution, status bar polling, and the controller's shutdown sequence all interact with it from different goroutines.

```go
type Manager struct {
    sink   event.Sink              // receives lifecycle notices
    root   context.Context         // session-scoped context
    cancel context.CancelFunc      // cancels all jobs on Close

    mu        sync.Mutex
    seq       int                   // monotonic ID counter
    jobs      map[string]*Job       // ID → Job
    order     []string              // insertion-order keys
    completed []string              // finished-job summaries awaiting drain
}
```

### Construction

```go
func NewManager(sink event.Sink) *Manager
```

Creates a manager with a fresh session-scoped context (cancelled by `Close`). The `sink` receives job-lifecycle notices; it should be the session's synchronized `event.Sync` because jobs emit from goroutines that may race the main event loop. If `sink` is `nil`, it falls back to `event.Discard` (no notices emitted).

### Session-Scoped Context

The manager's `root` context is independent of any individual turn's context. It is created at session start and cancelled at session end. This means:

- A job started in turn 3 continues running through turn 4, turn 5, and so on
- The controller closing the session (e.g., user closing the app) cancels all jobs
- Individual tool call timeouts do not affect background jobs — they run until completion, failure, or explicit kill

---

## Job

```go
type Job struct {
    ID    string          // e.g., "bash-1", "task-3"
    Kind  string          // "bash" or "task"
    Label string          // human-readable description

    mu         sync.Mutex
    buf        bytes.Buffer  // streaming output accumulator
    readOffset int           // how far Output has read into buf
    status     Status        // current lifecycle state
    result     string        // terminal result text (for task jobs)
    resultRead bool          // whether result was already surfaced by Output
    startedAt  int64         // unix milliseconds at start time
    cancel     context.CancelFunc
    done       chan struct{} // closed when the run goroutine finishes
}
```

The `buf` field is an append-only buffer that accumulates streamed output from the running process. The `readOffset` tracks how far the `Output` method has read, so each call returns only the new text since the last read — a streaming window pattern that lets the agent check for incremental progress without re-reading the entire output.

### Status Lifecycle

```go
type Status string

const (
    Running Status = "running"
    Done    Status = "done"
    Failed  Status = "failed"
    Killed  Status = "killed"
)
```

Every job starts in `Running`. The transition to a terminal state happens inside the run goroutine when the `run` function returns:

- **`Done`**: The `run` function completed without error. For bash jobs, this means the process exited successfully; for task jobs, it means the sub-agent finished and returned a result.
- **`Failed`**: The `run` function returned a non-nil error. The error message is stored in `result` (unless `result` was already set by the run function).
- **`Killed`**: The job's context was cancelled (via `Kill` or session close). This is set both synchronously by `Kill` (for immediate visibility) and by the run goroutine on return (for consistency).

Once a job reaches a terminal state, it never transitions again.

---

## Start

```go
func (m *Manager) Start(kind, label string, run func(ctx context.Context, out io.Writer) (string, error)) *Job
```

Launches `run` on a goroutine under the manager's session context and returns the job immediately. The `run` function receives:

- **`ctx`**: A child of the manager's session context, cancelled on `Kill` or `Close`
- **`out`**: A `jobWriter` that appends to the job's buffer under its lock

The `run` function is expected to:
- Stream output to `out` as it produces it (bash jobs write process output here)
- Return a result string and an error (task jobs return their final answer; bash jobs return `""`)

On return, the goroutine sets the terminal status, records the completion (queueing a drain note and emitting a closing Notice), and closes the `done` channel — in that specific order. The ordering matters: `recordCompletion` must happen before `close(done)` so that a `Wait` call that unblocks on the channel sees the completion note already queued. Otherwise, `DrainCompletedNote` could race ahead of the bookkeeping and miss the just-finished job.

### ID Assignment

Job IDs are sequentially assigned: `bash-1`, `bash-2`, `task-1`, `task-2`, etc. The sequence counter is monotonic per manager, so IDs are unique within a session even across different kinds.

### Notice Emission

On start, a `Notice` event is emitted:

```
background bash started: bash-1 (build the project)
```

On completion, another `Notice` is emitted with the terminal status:

```
background bash finished: bash-1
background bash failed: bash-2 — exit status 1
background bash killed: bash-3
```

Failed jobs emit at `LevelWarn`; successful and killed jobs emit at `LevelInfo`.

---

## jobWriter

```go
type jobWriter struct{ j *Job }

func (w jobWriter) Write(p []byte) (int, error) {
    w.j.mu.Lock()
    defer w.j.mu.Unlock()
    return w.j.buf.Write(p)
}
```

The `jobWriter` is an append-only `io.Writer` that locks per write for concurrent safety. The running process's stdout/stderr is piped through this writer, ensuring that output from the goroutine and reads from `Output` never race. Because the buffer is append-only, the `readOffset` in the `Job` struct always points to a valid position in the buffer — earlier data is never overwritten.

---

## Output

```go
func (m *Manager) Output(id string) (text string, status Status, ok bool)
```

Returns the job's output produced since the last `Output` call, plus its current status. This implements a streaming window pattern:

1. Read `buf.String()` under the lock
2. Slice from `readOffset` to the end — this is the new text since the last read
3. Update `readOffset` to the full buffer length
4. Return the new text and the current status

For **task jobs**, the situation is different: task jobs stream nothing to the buffer (their output is the final result string). When a task job reaches a terminal state and `Output` has no buffered text to return, the method surfaces the `result` string once (setting `resultRead` to prevent re-surfacing). This ensures that `bash_output`'s promise of showing task results is fulfilled.

If the job ID is unknown, `ok` is `false`.

---

## Kill

```go
func (m *Manager) Kill(id string) bool
```

Cancels a running job. Returns `false` when the ID is unknown or the job has already finished.

A critical design choice: the status is flipped to `Killed` **synchronously** before the context is cancelled:

```go
j.mu.Lock()
running := j.status == Running
if running {
    j.status = Killed   // synchronous flip
}
j.mu.Unlock()
if !running {
    return false
}
j.cancel()              // actually cancel the context
return true
```

Why not wait for the goroutine to set the status? Because a killed process tree can take time to tear down — the `cmd.Wait` call trails by `WaitDelay` while cancelled processes exit. If `Kill` returned before the status changed, a subsequent `Output` or `Wait` call would still see `Running` even though the user explicitly asked to kill the job. The synchronous flip ensures that `Output` and `Wait` reflect the kill the instant it is requested.

The goroutine still sets `Killed` and records completion on return; this is harmless because the status is already `Killed`. The real terminal status (Done/Failed) is preserved for jobs that just finished — the synchronous flip only fires when the job is actually `Running`.

---

## Wait

```go
func (m *Manager) Wait(ctx context.Context, ids []string, timeoutSec int) []Result
```

Blocks until the named jobs (or every currently-running job when `ids` is empty) reach a terminal state, or the context is cancelled, or `timeoutSec` elapses (0 = no timeout).

For each target job, `Wait` selects on three channels:
1. `<-j.done`: The job's run goroutine finished
2. `<-ctx.Done()`: The caller's context was cancelled (e.g., the agent turn timed out)
3. `<-timeout`: The timeout elapsed

On any of these conditions, `Wait` returns `Result` snapshots for all target jobs — even on timeout or cancellation, partial progress is reported. This is important for the user experience: if a `wait` call times out, the agent still learns how far the jobs have progressed rather than getting nothing.

### Result

```go
type Result struct {
    ID     string
    Kind   string
    Label  string
    Status Status
    Output string
}
```

`Output` is the terminal result text if the job set one (task jobs), otherwise the full buffer contents (bash jobs).

### Resolve Logic

When `ids` is empty, `Wait` resolves to all currently-running jobs (in insertion order). This is how `wait` with no arguments works — "wait for everything that's still running." When `ids` is non-empty, only the specified jobs are targeted (unknown IDs are silently skipped).

---

## DrainCompletedNote

```go
func (m *Manager) DrainCompletedNote() string
```

Returns (and clears) a one-line summary of jobs that finished since the last drain, for the controller to fold into the next turn so the model learns of completions:

```
Background jobs finished since your last message: bash-1 (build) — done; task-2 — failed. Read their output with bash_output or wait if you still need it.
```

This is the key mechanism that bridges the gap between asynchronous job completion and the synchronous agent loop. Without it, the model would have no way to learn that a background build finished — it would only discover the result by explicitly calling `bash_output`, which it might not think to do. The drain note proactively informs the model, enabling it to incorporate the results into its next action.

The controller calls `DrainCompletedNote` at the start of each turn and injects the result as a system message, so the model sees it before planning its next tool calls.

---

## Running

```go
func (m *Manager) Running() []View
```

Returns a snapshot of the still-running jobs for the status bar display:

```go
type View struct {
    ID        string `json:"id"`
    Kind      string `json:"kind"`
    Label     string `json:"label"`
    Status    string `json:"status"`
    StartedAt int64  `json:"startedAt"`
}
```

The `StartedAt` timestamp (unix milliseconds) allows the UI to show elapsed time. Only jobs with status `Running` are included. The snapshot is taken under lock, so it is consistent even if jobs are starting or finishing concurrently.

---

## Close

```go
func (m *Manager) Close()
```

Cancels the session context, terminating every running job. Safe to call once at controller shutdown. After `Close`, no new jobs can be started (they will immediately see a cancelled context), and all running jobs will transition to `Killed`.

---

## Context Injection

The manager follows the same context-value injection pattern as the ask tool:

```go
func WithManager(ctx context.Context, m *Manager) context.Context
func FromContext(ctx context.Context) (*Manager, bool)
```

The agent sets `WithManager` on every tool call's context, so tools like `bash` (for `run_in_background`), `bash_output`, `kill_shell`, and `wait` can reach the manager without needing a direct reference to the agent or controller. If no manager is set (e.g., in headless tests or calls outside the run loop), `FromContext` returns `ok = false`, and the tool can return an appropriate error.

---

## Concurrency Safety

Every shared field in both `Manager` and `Job` is guarded by a mutex. The two-level locking (manager-level for the job map, job-level for per-job state) allows fine-grained concurrency: reading output from one job does not block starting another, and killing one job does not block waiting for a different one. The `jobWriter` takes the job's lock per write, ensuring that output streaming and output reading never race.

The only potential deadlock scenario — holding the manager lock while taking a job lock — is avoided by the `resolve` function, which takes the manager lock to build the target list and then releases it before calling `Wait` on individual jobs.
