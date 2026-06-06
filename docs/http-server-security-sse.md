# HTTP Server: Security & SSE

This document covers the security mechanisms and Server-Sent Events (SSE) features of the DeepSeek-Reasonix HTTP server, which exposes the controller over HTTP for browser and desktop clients.

**Source:** `internal/serve/serve.go`

---

## Overview

The HTTP server is a second frontend alongside the CLI TUI — proof that the controller is transport-agnostic, and the basis for browser and desktop clients. One server drives one session; multiple browser tabs share it. The server is designed with a security-first approach: CSRF protection, CORS restrictions, and ETag-based caching ensure that the unauthenticated agent endpoints are not exploitable by malicious websites.

---

## CSRF Protection: csrfGuard

### The Threat

The HTTP server's command endpoints (`/submit`, `/cancel`, `/approve`, etc.) have no authentication and bind to localhost. This means a website the user visits could potentially drive these endpoints with a simple cross-origin POST using `text/plain` content type — which does not trigger a CORS preflight. A malicious page could submit prompts, auto-approve tool calls, or cancel operations without the user's knowledge.

### The Defense

`csrfGuard` is an HTTP middleware that rejects any POST request that doesn't carry the `Content-Type: application/json` header. The logic is:

1. For every request with method `POST`:
   - Extract the `Content-Type` header.
   - Strip any parameters after `;` (e.g., `application/json; charset=utf-8` → `application/json`).
   - Trim whitespace.
   - If the result is not exactly `"application/json"`, return HTTP 415 (Unsupported Media Type) with the message `"Content-Type must be application/json"`.

2. All other HTTP methods (GET, OPTIONS, etc.) pass through without restriction.

### Why This Works

A cross-origin `fetch()` or `XMLHttpRequest` with `Content-Type: application/json` triggers a CORS preflight (an `OPTIONS` request). The DeepSeek-Reasonix server does **not** answer CORS preflights in production mode (no `Access-Control-Allow-Methods` header), so the browser blocks the request before it ever reaches the server.

The same-origin frontend (served by the server itself at `GET /`) always sends JSON, so it is completely unaffected by this guard.

### Threat Model Summary

| Attack Vector | Blocked By |
|---|---|
| Cross-origin `<form>` POST (default `application/x-www-form-urlencoded`) | csrfGuard: wrong Content-Type |
| Cross-origin `fetch()` with `Content-Type: application/json` | CORS preflight: server doesn't answer OPTIONS |
| Cross-origin `fetch()` with `Content-Type: text/plain` | csrfGuard: wrong Content-Type |
| Same-origin JavaScript (XSS) | Out of scope (XSS on localhost implies full control) |

---

## SSE Keepalive

### The Problem

Most reverse proxies (nginx, AWS ALB, Cloudflare) close idle upstream connections after 30–60 seconds. A long quiet turn — where the agent is thinking or the model is generating a single long response — can easily hit that idle window. When the proxy closes the connection, the next SSE event arrives on a half-closed stream, and the client sees a disconnection.

### The Solution

The `/events` endpoint emits a `: ping` SSE comment every 15 seconds (`sseKeepaliveInterval = 15 * time.Second`). SSE comment lines start with `:` and are ignored by the `EventSource` client — they're a no-op for the consumer while keeping the TCP socket warm for the proxy.

### Implementation

```go
keepalive := time.NewTicker(sseKeepaliveInterval)
defer keepalive.Stop()

for {
    select {
    case data, ok := <-ch:
        // Real events
        fmt.Fprintf(w, "data: %s\n\n", data)
        flusher.Flush()
    case <-keepalive.C:
        // Keepalive ping
        fmt.Fprint(w, ": ping\n\n")
        flusher.Flush()
    case <-r.Context().Done():
        return
    }
}
```

The `: ping` comment is a single byte of meaningful content on the wire (the colon), making it essentially free in terms of bandwidth. The 15-second interval is well within the idle timeout of common proxies (30–60 seconds), providing a comfortable safety margin.

### SSE Connection Setup

When a client connects to `GET /events`, the server:

1. Checks that the response writer supports flushing (required for SSE).
2. Sets headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `Connection: keep-alive`.
3. Subscribes to the broadcaster's event channel.
4. Emits `: connected\n\n` immediately to open the stream.
5. Enters the event loop described above.

---

## ETag-Based Caching

### Purpose

The `/history` and `/context` endpoints support ETag-based conditional requests, allowing reconnecting clients to avoid re-fetching unchanged data. This is particularly important for the browser client, which may reconnect frequently due to SSE disconnections.

### Implementation: writeJSONCached

`writeJSONCached(w, r, v)` encodes a value as JSON and implements ETag caching:

1. Marshals the value to JSON bytes.
2. Computes a SHA-256 hash of the body and formats it as a weak ETag: `"<hex-digest>"`.
3. Compares the ETag against the request's `If-None-Match` header.
4. If they match, returns `304 Not Modified` with no body — saving bandwidth.
5. If they don't match, sets response headers and writes the full body:
   - `Content-Type: application/json`
   - `ETag: "<hex-digest>"`
   - `Cache-Control: private, max-age=0, must-revalidate`

The `Cache-Control` header ensures that:
- The response is private (not cached by shared proxies).
- `max-age=0` means the client must revalidate on every request.
- `must-revalidate` prevents the client from using a stale cache entry.

### Endpoints Using ETags

| Endpoint | Data | ETag Basis |
|---|---|---|
| `GET /history` | Session message log (`{role, content}` pairs) | SHA-256 of the JSON-encoded message array |
| `GET /context` | Prompt-vs-window gauge (`{used, window}`) | SHA-256 of the JSON-encoded gauge object |

---

## CORS Configuration

### Production Mode: No CORS

By default, the server does not add any CORS headers. Same-origin policy protects the unauthenticated agent endpoints — only pages served by the server itself (at `GET /`) can make requests to it. This is the correct configuration for production use.

### Development Mode: HandlerWithCORS

`HandlerWithCORS(origin)` adds permissive CORS headers for a specific allowed origin. This is intended **only** for local development, where a Vite dev server on a different port (e.g., `http://localhost:5173`) needs to reach the Reasonix server.

The CORS middleware:

1. If `origin` is empty, skips CORS entirely (equivalent to production mode).
2. Sets `Access-Control-Allow-Origin` to the specified origin.
3. Sets `Access-Control-Allow-Methods` to `GET, POST, OPTIONS`.
4. Sets `Access-Control-Allow-Headers` to `Content-Type, Authorization`.
5. Answers `OPTIONS` preflight requests with `204 No Content`.

**Warning**: Do NOT use `HandlerWithCORS` in production. The server has no authentication, so broad CORS would allow any website to drive the agent.

---

## HTTP Endpoints

### GET Endpoints

| Endpoint | Description |
|---|---|
| `GET /` | Serves the minimal browser client (`index.html`, embedded at build time). |
| `GET /events` | SSE stream of controller events. Includes keepalive pings every 15 seconds. |
| `GET /history` | Session message log as `{role, content}` pairs. ETag-cached. |
| `GET /context` | Prompt-vs-window gauge (`{used, window}`). ETag-cached. |
| `GET /checkpoints` | Session checkpoint list for the rewind picker: `{turn, prompt, files}`. |
| `GET /branches` | Branch list and tree text for the branch picker. |
| `GET /status` | Combined status snapshot: label, running state, plan/bypass mode, context usage, cache stats, last usage, balance, jobs. |
| `GET /sessions` | Saved session files with LLM-generated titles and turn counts. |
| `GET /skills` | Discoverable skills list. |

### POST Endpoints

| Endpoint | Description | Request Body |
|---|---|---|
| `POST /submit` | Submits raw user input as a turn. Intercepts `/model` and `/effort` for runtime switching. Returns 202. | `{"input": "..."}` |
| `POST /cancel` | Cancels the current turn. Returns 204. | — |
| `POST /approve` | Responds to an interactive approval request. | `{"id": "...", "allow": bool, "session": bool, "persist": bool}` |
| `POST /plan` | Toggles plan mode. | `{"on": bool}` |
| `POST /compact` | Compacts the session context. Also snapshots to disk after compaction. | — |
| `POST /new` | Starts a new session. | — |
| `POST /rewind` | Rewinds the session to a checkpoint. | `{"turn": int, "scope": "code"\|"conversation"\|"both"}` |
| `POST /fork` | Creates a new branch at a checkpoint. | `{"turn": int, "name": "..."}` |
| `POST /summarize` | Summarizes from or up to a turn. | `{"turn": int, "mode": "from"\|"upto"}` |
| `POST /bypass` | Toggles YOLO/bypass mode. | `{"on": bool}` |
| `POST /answer` | Responds to an ask_request. | `{"id": "...", "answers": [...]}` |
| `POST /resume` | Loads a previous session from a JSONL file. Snapshots the current session first. | `{"path": "..."}` |
| `POST /forget` | Deletes a saved memory by name. | `{"name": "..."}` |

All POST endpoints require `Content-Type: application/json` (enforced by `csrfGuard`).

---

## Model and Effort Switching

### /model Switching via /submit

When the `/submit` endpoint receives input starting with `/model `, it intercepts the command and calls `switchModel` instead of submitting it as a regular turn:

1. `switchModel(ctx, ref)` acquires the write lock.
2. Snapshots the current session.
3. Extracts the conversation history from the current controller.
4. Builds a new controller via `boot.Build` with the specified model reference.
5. Resumes the new controller with the carried-over history, keeping the session in its existing file.
6. Replaces the controller under the write lock.
7. Closes the old controller.

The write lock is held across the entire rebuild, ensuring that concurrent requests never read a half-swapped controller and two switches can't run at once. If a turn is currently running, the switch is rejected.

### /effort Switching via /submit

When the `/submit` endpoint receives input starting with `/effort `, it intercepts the command and calls `switchEffort`:

1. Validates that the provider supports effort control.
2. Normalizes the effort level via `NormalizeEffort`.
3. Loads the user configuration file and applies the effort edit:
   - Upserts the provider entry if it doesn't exist in the config file.
   - For Anthropic providers, enables adaptive thinking automatically.
   - Sets the effort level on the provider entry.
4. Saves the configuration to disk.
5. Rebuilds the controller via `switchModel` with the same model reference (which picks up the new effort setting from the saved config).

---

## Interactive Approval

The server enables interactive approval on startup via `ctrl.EnableInteractiveApproval()`. This means that when the controller encounters an "ask" decision (e.g., a tool that requires user confirmation), it emits an `approval_request` event on the SSE stream instead of auto-approving or auto-denying. The client responds via `POST /approve` with the approval decision.

This is essential for the HTTP server because, unlike the CLI TUI (which can prompt the user directly), the server has no built-in mechanism for synchronous user input — approval must be handled asynchronously through the event/command channel.

---

## Graceful Shutdown

`RunGraceful(ctx, addr)` serves with graceful shutdown:

1. Starts the HTTP server in a background goroutine.
2. Listens for context cancellation (triggered by SIGINT/SIGTERM in the main function).
3. On cancellation, calls `srv.Shutdown(shutdownCtx)` with a 10-second timeout.
4. The shutdown drains active connections (including in-flight SSE streams) before returning.
5. If the 10-second timeout expires, any remaining connections are forcibly closed.

The server also sets reasonable timeouts:
- `ReadHeaderTimeout: 10s` — prevents slowloris attacks.
- `IdleTimeout: 120s` — allows SSE connections to stay open for up to 2 minutes of inactivity (supplemented by keepalive pings).

---

## LLM-Generated Session Titles

The server uses a lightweight flash provider (DeepSeek Flash) to generate short session titles for the `/sessions` endpoint. The title generation system:

### initTitleProvider

On startup, the server attempts to resolve a `"deepseek-flash"` model from the configuration. If found, it creates a provider instance with `effort: "off"` (to minimize reasoning overhead for this trivial task). If the model isn't available, title generation is silently disabled — the server works fine without it.

### generateTitle

`generateTitle(ctx, firstMsg)` produces a 3–5 word title from the user's first message:

1. Truncates the first message to 300 runes to keep the prompt small.
2. Calls the flash model with temperature 0 and max 20 tokens.
3. Strips surrounding quotes from the response (the model sometimes wraps titles in quotes).
4. Returns the trimmed title.

### titleCache

Session titles are cached in a `titleCache` keyed by filename and mtime. When the `/sessions` endpoint is called, it checks the cache first. If the mtime has changed (the session file was modified), the title is regenerated and the cache is updated. This ensures that:
- Titles are not regenerated on every `/sessions` call.
- Titles are updated when the session content changes.
- The cache is invalidated when the underlying file is modified.

---

## Request Logging

`logMiddleware` logs every request's method, path, status code, and duration. This is useful for debugging and monitoring, especially when the server is accessed by multiple browser tabs or automated tools. The middleware wraps the response writer in a `responseWriter` struct that captures the status code, and it preserves the `Flush()` method required for SSE streaming.
