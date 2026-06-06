# Process Management Utilities

This document covers the cross-platform process management utilities in DeepSeek-Reasonix, including Windows console window suppression and platform-specific process tree termination.

**Source:** `internal/proc/hide_windows.go`, `internal/proc/hide_other.go`, `internal/proc/kill_windows.go`, `internal/proc/kill_other.go`

---

## Overview

Reasonix spawns child processes for various operations: bash/shell tool execution, clipboard image capture (PowerShell on Windows), plugin transport (stdio), and the CodeGraph daemon. The `internal/proc` package provides two capabilities that differ significantly between Windows and POSIX platforms:

1. **Console window suppression** — preventing GUI console windows from flashing on screen when a GUI application spawns console-mode children
2. **Process tree termination** — killing a process and all its descendants, which is straightforward on POSIX but requires special techniques on Windows

Both capabilities use Go's build-tag system to provide platform-specific implementations with a common API.

---

## Console Window Suppression

### The Problem

On Windows, when a GUI application (like the Reasonix desktop app built with Wails) spawns a console-mode child process (like `git`, `rg`, or `powershell`), Windows creates a new console window for the child. This window appears briefly as a black flash on the screen — a distracting visual artifact that makes the application feel unpolished. The problem is unique to Windows; on macOS and Linux, GUI applications can spawn terminal children without any visible side effects.

### HideWindow (Windows)

**Source:** `internal/proc/hide_windows.go`

`HideWindow` stops a child process from flashing a console window on Windows by setting two flags on the `SysProcAttr` structure:

- **`HideWindow = true`**: Sets the `wShowWindow` field in the `STARTUPINFO` structure to `SW_HIDE`, which tells the new process not to show its window
- **`CREATE_NO_WINDOW` (0x08000000)**: A process creation flag that prevents the system from creating a console window for the new process. This is the primary mechanism for suppressing the console flash.

The function first checks whether `cmd.SysProcAttr` is nil and creates it if needed, ensuring it doesn't panic when called on a command that hasn't been configured with custom process attributes.

### HideWindowDetached (Windows)

`HideWindowDetached` is a variant for short-lived desktop-only probes whose descendants have been observed to flash their own console windows. It uses the `DETACHED_PROCESS` (0x00000008) creation flag instead of `CREATE_NO_WINDOW`. A detached process runs without a controlling terminal, which prevents not only the direct child's console window but also any console windows created by the child's descendants.

**Important**: `HideWindowDetached` should NOT be used for tools that rely on console-program stdout/stderr capture (such as PowerShell commands), because detached processes may not properly inherit the parent's stdout/stderr pipes. It is specifically for fire-and-forget probes where output capture is not needed.

### POSIX Implementation

**Source:** `internal/proc/hide_other.go`

On non-Windows platforms, `HideWindow` and `HideWindowDetached` are no-ops (empty functions that accept `*exec.Cmd` and do nothing). This is because console window flashing is a Windows-specific issue; POSIX systems don't create visible windows for child processes. The no-op implementation ensures that callers don't need platform-specific conditional code.

---

## Process Tree Termination

### The Problem

When Reasonix needs to stop a running tool (e.g., a bash command that's taking too long, or a user-cancelled operation), it must kill not just the direct child process but the entire process tree. This is critical because many Reasonix tools spawn nested processes:

- **Bash tool**: `bash` → `python script.py` → the Python interpreter and its children
- **Plugin transport**: A launcher script (e.g., `cmd.exe → node.exe`) where killing only `cmd.exe` leaves `node.exe` alive, holding the inherited stdout/stderr pipes and preventing `cmd.Wait()` from returning
- **CodeGraph daemon**: A Node.js process that re-parents itself away from its launcher, making it invisible to simple tree-walk termination

### POSIX: kill_other.go

On non-Windows platforms, `KillTree` simply calls `cmd.Process.Kill()`, which sends `SIGKILL` to the direct child. The operating system then handles cleanup: orphaned children are reparented to init (PID 1), which reaps them. The `Wait()` call returns once the direct child exits, and the orphaned descendants eventually clean themselves up.

`TrackTree` and `KillTracked` are no-ops on POSIX (returning 0 and delegating to `KillTree` respectively), because the platform's process lifecycle management makes job-object-style tracking unnecessary.

### Windows: kill_windows.go

On Windows, process termination is significantly more complex for several reasons:

1. **`Process.Kill` only signals the direct child**: Unlike POSIX `kill -9`, which causes the kernel to clean up the process's children, Windows `TerminateProcess` only affects the specified process. Children remain alive and inherit no new parent.

2. **Pipe inheritance**: Child processes inherit the parent's stdout/stderr pipes. If a child is killed but its descendants remain alive holding those pipes, `cmd.Wait()` blocks forever waiting for the pipe to close.

#### KillTree

`KillTree` uses `taskkill /F /T /PID {pid}` to walk the live process tree and kill every descendant. The `/T` flag terminates all child processes along with the parent, and `/F` forces termination. After `taskkill` completes, `cmd.Process.Kill()` is called as a safety measure to ensure the direct child is terminated even if `taskkill` had a partial failure.

The `taskkill` command itself is hidden via `HideWindow(kill)` to prevent its console window from flashing.

#### TrackTree and KillTracked

`TrackTree` assigns the child process to a Windows **Job Object** configured with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. This flag tells the kernel to terminate all processes in the job when the job handle is closed. This mechanism catches a critical edge case that `taskkill /T` misses: **detached grandchildren**.

Some processes (like the CodeGraph Node.js daemon) re-parent themselves away from their launcher using `DETACHED_PROCESS` or by calling `CreateProcess` without inheriting the parent's handle. These "escaped" processes don't appear in the live process tree that `taskkill /T` walks, but they remain in the Job Object because job membership is set at process creation time and is inherited by all child processes regardless of detachment.

The lifecycle works as follows:

1. `TrackTree(cmd)` creates a Job Object, configures it with `KillOnJobClose`, opens a handle to the child process, and assigns it to the job. Returns the job handle.
2. During normal operation, the child and all its descendants run inside the job.
3. When termination is needed, `KillTracked(cmd, job)` calls `TerminateJobObject` (which kills all processes in the job) and closes the handle, then falls through to `KillTree` as a safety net.
4. If Reasonix crashes or is force-killed, the OS closes the job handle, which triggers `KillOnJobClose` and terminates all processes in the job — including detached grandchildren that `taskkill /T` would miss.

This dual-termination strategy (`KillTree` for the live tree + Job Object for escaped processes) provides robust cleanup even in the face of process detachment and application crashes.

### Error Handling

All three kill functions (`KillTree`, `TrackTree`, `KillTracked`) handle nil inputs gracefully — if `cmd` is nil or `cmd.Process` is nil (the command hasn't started), they return immediately without panicking. `TrackTree` returns 0 on any failure (job creation, process assignment), and the caller falls back to using `KillTree` alone.

---

## Usage Across Reasonix

The process management utilities are used in several subsystems:

| Subsystem | Usage |
|-----------|-------|
| **Bash tool** (`internal/tool/builtin/bash.go`) | `HideWindow` on Windows to suppress console flashes; `KillTree`/`KillTracked` for command cancellation |
| **Clipboard capture** (`internal/control/attachments.go`) | `HideWindow` on Windows PowerShell commands to suppress console flashes during image paste |
| **Plugin transport** (`internal/plugin/transport_stdio.go`) | `KillTree` for shutting down misbehaving MCP server processes; `TrackTree` for ensuring cleanup on crash |
| **CodeGraph** (`internal/codegraph/`) | `TrackTree` for the CodeGraph daemon, which re-parents itself away from its launcher |
| **Shell kill** (`internal/control/shell_kill_*.go`) | Platform-specific shell process termination using the proc utilities |

The consistent use of `HideWindow` across all Windows subprocess invocations ensures that the desktop app never shows distracting console flashes, while the dual termination strategy (`KillTree` + Job Object) ensures reliable process cleanup even during crashes or forced shutdowns.
