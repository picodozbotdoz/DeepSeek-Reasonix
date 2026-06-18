# Async Worker Delegation

Fire-and-forget delegation for multi-worker scenarios in Reasonix.

## Overview

The ACP bridge now supports async worker delegation, allowing the manager to start multiple workers without blocking and check their status later. This avoids context window pollution from blocking delegation.

## Tools

### delegate_task_async

Start a worker task without blocking. Returns a worker ID immediately.

```json
{
  "name": "delegate_task_async",
  "arguments": {
    "task": "Implement feature X",
    "cwd": "/path/to/worker/workspace"
  }
}
```

**Response:**
```
Worker started: worker-1. Use worker_status(worker_id="worker-1") to check progress.
```

### worker_status

Check the status of a background worker. Does not block.

```json
{
  "name": "worker_status",
  "arguments": {
    "worker_id": "worker-1"
  }
}
```

**Response (running):**
```
[worker-1] running
Task: Implement feature X
Cwd: /path/to/worker
Started: 2026-06-18T21:51:19+07:00
Elapsed: 32s
Heartbeat: OK (last response: 12s ago)
```

**Response (done):**
```
[worker-1] done
Task: Implement feature X
Cwd: /path/to/worker
Started: 2026-06-18T21:51:19+07:00
Duration: 45s
Result:
Feature X implemented successfully...
```

### worker_kill

Kill a running background worker.

```json
{
  "name": "worker_kill",
  "arguments": {
    "worker_id": "worker-1"
  }
}
```

**Response:**
```
[worker-1] killed (was running)
```

## Configuration

### CLI Flags

```bash
reasonix-plugin-acp-bridge \
  -learner-dir ./worker \
  -pool-max-total 3 \          # Max concurrent ACP processes
  -task-timeout 30m \          # Per-task timeout
  -heartbeat-interval 5m \     # Heartbeat ping interval (future)
  -dead-timeout 10m            # Kill if no response (future)
```

### reasonix.toml (Multiple Workers)

```toml
[[plugins]]
name = "worker-1"
command = "reasonix-plugin-acp-bridge"
args = ["-learner-dir", "./workers/worker-1"]

[[plugins]]
name = "worker-2"
command = "reasonix-plugin-acp-bridge"
args = ["-learner-dir", "./workers/worker-2"]

[[plugins]]
name = "worker-3"
command = "reasonix-plugin-acp-bridge"
args = ["-learner-dir", "./workers/worker-3"]
```

## How It Works

### Async Delegation Flow

```
Manager                          ACP Bridge                      Worker
   |                                |                               |
   |-- delegate_task_async(task) -->|                               |
   |<-- worker_id: worker-1 --------|                               |
   |                                |-- start ACP process -------->|
   |                                |-- session/new -------------->|
   |                                |-- session/prompt(task) ----->|
   |                                |                               |
   |   (manager continues)          |   (worker runs in background) |
   |                                |                               |
   |-- worker_status(worker-1) --->|                               |
   |<-- running, elapsed: 32s ------|                               |
   |                                |                               |
   |-- worker_status(worker-1) --->|                               |
   |<-- done, result: ... ----------|<-- session/close ------------|
```

### Heartbeat Monitoring

Every 5 minutes, the bridge pings each running worker to check if it's alive:

```
heartbeatMonitor():
  every 5m:
    if worker dead (no response in 10m):
      kill worker
      mark as "killed"
    else:
      send heartbeat ping
      update LastResponse timestamp
```

### Dead Detection

If a worker doesn't respond within 10 minutes, it's automatically killed:

```go
if time.Since(lastResponse) > deadTimeout {
    log.Printf("heartbeat: worker %s dead, killing", w.ID)
    w.cancel()  // cancels ACP session
}
```

## Usage Examples

### Parallel Feature Implementation

```
# Start 3 workers for different features
delegate_task_async(task="Implement auth module") → worker-1
delegate_task_async(task="Implement API layer") → worker-2
delegate_task_async(task="Write tests") → worker-3

# Check status periodically
worker_status(worker_id="worker-1") → running
worker_status(worker_id="worker-2") → running
worker_status(worker_id="worker-3") → done

# Get result when done
worker_status(worker_id="worker-3") → Result: Tests written...
```

### Kill Stuck Worker

```
# Worker taking too long
worker_status(worker_id="worker-1") → running (elapsed: 25m)

# Kill it
worker_kill(worker_id="worker-1") → killed

# Start fresh
delegate_task_async(task="Implement auth module (retry)") → worker-4
```

## Context Window Impact

| Metric | Blocking (delegate_task) | Async (delegate_task_async) |
|--------|--------------------------|----------------------------|
| Per-delegation tokens | ~2K (task + result) | ~100 (job_id only) |
| Blocking time | 5min per worker | 0ms |
| Concurrent workers | 1 | N (pool size) |
| Dead detection | None | 10min auto-restart |
| Result injection | Immediate | On-demand via worker_status |

## Pool Size

The ACP pool limits concurrent workers per bridge process:

- **Default**: 1 (serial execution)
- **With `-pool-max-total 3`**: 3 concurrent workers
- **Multiple plugins**: Each plugin has its own pool (recommended)

For maximum concurrency, declare multiple plugins in `reasonix.toml` rather than increasing pool size. This provides process isolation and avoids shared pool contention.

## Limitations

1. **ACP session state**: Workers maintain session state across calls. Heartbeat pings may interfere with worker's actual task (future: filter `__heartbeat__` prompts).

2. **Pool exhaustion**: Too many concurrent workers may exhaust system resources. Monitor RSS usage.

3. **No auto-restart**: Dead workers are killed but not restarted. Future: configurable restart policy.

## Implementation Details

### Files Modified

- `cmd/reasonix-plugin-acp-bridge/main.go` — Added async worker infrastructure

### Key Structures

```go
type asyncWorker struct {
    ID            string
    Task          string
    Cwd           string
    Status        string     // "running", "done", "failed", "killed"
    Result        string
    Error         string
    StartedAt     time.Time
    DoneAt        time.Time
    SessionID     string
    client        *acpClient
    cancel        context.CancelFunc
    mu            sync.Mutex // protects concurrent access
    LastHeartbeat time.Time
    LastResponse  time.Time
    HeartbeatOK   bool
}
```

### Configuration Defaults

```go
heartbeatInterval = 5 * time.Minute
deadTimeout       = 10 * time.Minute
```

## Future Work

1. **Configurable heartbeat/dead timeout** via `worker.toml` or CLI flags
2. **Auto-restart policy** for dead workers
3. **Worker output streaming** (real-time progress)
4. **Worker list tool** to see all active workers
5. **Heartbeat filtering** in worker to ignore `__heartbeat__` prompts
