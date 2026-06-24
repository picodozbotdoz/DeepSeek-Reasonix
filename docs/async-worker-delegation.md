# Async Worker Delegation

Fire-and-forget delegation for multi-worker scenarios in Reasonix.

## Quick Start for Manager

### 1. Configure Workers in reasonix.toml

```toml
[[plugins]]
name = "worker-1"
command = "reasonix-plugin-acp-bridge"
args = ["-learner-dir", "./workers/worker-1", "-pool-max-total", "2"]

[[plugins]]
name = "worker-2"
command = "reasonix-plugin-acp-bridge"
args = ["-learner-dir", "./workers/worker-2", "-pool-max-total", "2"]

[[plugins]]
name = "worker-3"
command = "reasonix-plugin-acp-bridge"
args = ["-learner-dir", "./workers/worker-3", "-pool-max-total", "2"]
```

### 2. Create Worker Config (Optional)

Create `<learnerDir>/worker.toml` for each worker:

```toml
# workers/worker-1/worker.toml
description = "Backend implementation worker"
heartbeat_interval = "2m"
dead_timeout = "5m"
```

### 3. Use the Tools

```
# Start workers without blocking
delegate_task_async(task="Implement auth module") → worker-1
delegate_task_async(task="Implement API layer") → worker-2
delegate_task_async(task="Write tests") → worker-3

# Check status later
worker_status(worker_id="worker-1") → running/done/failed

# List all workers
worker_list → shows all active workers

# Kill stuck worker
worker_kill(worker_id="worker-1") → killed
```

## Tools Reference

### delegate_task_async

Start a worker task without blocking. Returns a worker ID immediately.

```json
{
  "name": "delegate_task_async",
  "arguments": {
    "task": "Implement feature X",
    "cwd": "/path/to/worker/workspace"  // optional
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

### worker_list

List all background workers and their status.

```json
{
  "name": "worker_list",
  "arguments": {}
}
```

**Response:**
```
Background workers (3):

[worker-1] running
  Task: Implement auth module
  Elapsed: 32s
  Heartbeat: OK (last: 12s ago)
[worker-2] done
  Task: Implement API layer
  Duration: 45s
[worker-3] running
  Task: Write tests
  Elapsed: 15s
  Heartbeat: OK (last: 8s ago)
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
  -task-timeout 30m            # Per-task timeout
```

### reasonix.toml (Multiple Workers)

```toml
[[plugins]]
name = "worker-1"
command = "reasonix-plugin-acp-bridge"
args = ["-learner-dir", "./workers/worker-1", "-pool-max-total", "2"]
```

### worker.toml (Per-Worker Config)

Create `<learnerDir>/worker.toml` to customize behavior:

```toml
description = "Code implementation worker"
heartbeat_interval = "2m"
dead_timeout = "5m"
auto_restart = true
max_restarts = 3
restart_backoff = "30s"
```

| Field | Default | Description |
|-------|---------|-------------|
| `description` | (none) | Custom tool description for this worker |
| `heartbeat_interval` | `5m` | How often to ping running workers |
| `dead_timeout` | `10m` | Kill worker if no response in this time |
| `auto_restart` | `false` | Automatically restart dead workers |
| `max_restarts` | `3` | Maximum restart attempts |
| `restart_backoff` | `30s` | Base backoff between restarts (exponential) |

Set `heartbeat_interval = "0"` to disable heartbeat monitoring.

## Manager Workflow

### Parallel Feature Implementation

```
# 1. Start multiple workers
delegate_task_async(task="Implement auth module") → worker-1
delegate_task_async(task="Implement API layer") → worker-2
delegate_task_async(task="Write tests") → worker-3

# 2. Continue with other work (no blocking)

# 3. Check status periodically
worker_status(worker_id="worker-1") → running
worker_status(worker_id="worker-2") → running
worker_status(worker_id="worker-3") → done

# 4. Get result when done
worker_status(worker_id="worker-3") → Result: Tests written...

# 5. Collect all results
worker_list → see all workers and their status
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

### Monitor Long-Running Tasks

```
# Start a complex task
delegate_task_async(task="Refactor entire codebase") → worker-1

# Check periodically
worker_status(worker_id="worker-1") → running (elapsed: 5m)
worker_status(worker_id="worker-1") → running (elapsed: 10m)
worker_status(worker_id="worker-1") → done (duration: 12m)

# Get result
worker_status(worker_id="worker-1") → Result: Refactoring complete...
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

## Heartbeat Monitoring

Every `heartbeat_interval` (default 5m), the bridge pings each running worker:

```
heartbeatMonitor():
  every heartbeat_interval:
    if worker dead (no response in dead_timeout):
      kill worker
      mark as "killed"
    else:
      send heartbeat ping
      update LastResponse timestamp
```

## Limitations

1. **ACP session state**: Workers maintain session state across calls. Heartbeat pings may interfere with worker's actual task.

2. **Pool exhaustion**: Too many concurrent workers may exhaust system resources. Monitor RSS usage.

3. **No auto-restart**: Dead workers are killed but not restarted. Future: configurable restart policy.

## Future Work

1. **Auto-restart policy** for dead workers with exponential backoff
2. **Worker output streaming** (real-time progress)
3. **Heartbeat filtering** in worker to ignore `__heartbeat__` prompts
4. **Manager integration** with agent-level tools for delegation workflow
