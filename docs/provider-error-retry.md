# Provider Error Handling & Retry System

This document covers the provider error handling subsystem in DeepSeek-Reasonix, encompassing the retry loop with exponential backoff, the streaming think-tag splitter for providers that inline chain-of-thought, JSON Schema canonicalization for cache stability, and error message localization for actionable user-facing diagnostics.

---

## Retry System

**Source:** `internal/provider/retry.go`

The retry system is the backbone of Reasonix's resilience against transient provider failures. It wraps every outgoing streaming HTTP request with a configurable retry loop that handles connection drops, rate limits, and server errors — all without user intervention.

### SendWithRetry

`SendWithRetry` is the central function that POSTs a streaming request built by a `newReq` closure and returns the OK response. It retries the connection + header phase up to `MaxRetries = 10` times on transient errors, using capped exponential backoff with jitter. The function is designed so that retries cover **only the header phase** — once the response body starts streaming (i.e., the model has already emitted tokens), mid-stream failures are **not** retried. This is a deliberate design choice: replaying a partially consumed stream would duplicate tokens the user has already seen, leading to garbled output.

The function accepts a `newReq` closure rather than a pre-built `*http.Request` so that each retry attempt constructs a fresh request with a new context deadline and body reader. This avoids the common pitfall of trying to replay an already-consumed request body.

### Backoff Strategy

The backoff delay follows the formula:

```
delay = 500ms * 2^(attempt-1) + random[0, 250ms)
```

This produces the following sequence of base delays (before jitter):

| Attempt | Base Delay |
|---------|-----------|
| 1       | 500ms     |
| 2       | 1s        |
| 3       | 2s        |
| 4       | 4s        |
| 5       | 8s        |
| 6+      | 15s (cap) |

The maximum backoff is capped at 15 seconds. The random jitter of up to 250ms prevents thundering-herd scenarios when multiple Reasonix instances retry simultaneously against the same provider endpoint.

When the provider returns a `Retry-After` header, the backoff honors it instead of computing the exponential delay. The `parseRetryAfter` function interprets the header value as an integer number of seconds. If the `Retry-After` value exceeds the 15-second cap, the cap still applies.

### Error Classification

The retry system classifies errors into three categories:

#### AuthError (401/403)

HTTP 401 Unauthorized and 403 Forbidden responses are immediately wrapped in an `*AuthError` struct and returned without retrying. The `AuthError` carries the `KeyEnv` field — the name of the environment variable holding the API key (e.g., `DEEPSEEK_API_KEY`) — so that downstream error message localization can produce actionable guidance like "check your DEEPSEEK_API_KEY environment variable" rather than a cryptic "401 Unauthorized".

#### APIError (other non-OK)

Any non-200 HTTP status that is not an auth failure becomes an `*APIError`. It carries the provider name, HTTP status code, and a trimmed snippet of the response body (up to 4 KiB). APIErrors for retryable statuses are retried; all others are returned immediately.

#### Transient (connection-level)

Connection-level errors — network timeouts, DNS failures, TLS handshake errors — are treated as transient and always retried. The `transientErr` function returns true for any non-nil error that isn't `context.Canceled` or `context.DeadlineExceeded`, since those two represent intentional cancellation rather than recoverable failures.

### RetryableStatus

The `RetryableStatus` function determines whether an HTTP status code is worth retrying:

- **Retryable:** 408 (Request Timeout), 429 (Too Many Requests), and all 5xx codes (500–599, including Anthropic's overloaded 529)
- **Not retryable:** 400 (Bad Request), 401 (Unauthorized), 402 (Payment Required), 422 (Unprocessable Entity), and all other 4xx codes

The distinction is crucial: 4xx errors other than 408/429 represent caller or configuration problems that retrying cannot fix. Re-sending the same malformed request would simply produce the same error, wasting time and tokens.

### IsConnReset

`IsConnReset` detects connection-level drops that occur when a proxy or the remote server idle-closes the long-lived SSE connection. This is a common failure mode with local proxies like **v2rayN** and **sing-box**, which aggressively close idle connections. During a reasoner model's first-token latency gap (which can last tens of seconds), the proxy sees no data flowing and tears down the connection.

The function checks for the following sentinel errors:

- `io.ErrUnexpectedEOF` — the connection was closed mid-stream
- `io.EOF` — clean EOF from the remote end
- `net.ErrClosed` — the socket was explicitly closed
- `syscall.ECONNRESET` — connection reset by peer
- `syscall.ECONNABORTED` — connection aborted locally

It also matches any `net.Error` via `errors.As`, catching platform-specific network error types. Context cancellation errors (`context.Canceled`, `context.DeadlineExceeded`) are explicitly excluded — they represent intentional shutdown, not a recoverable network issue.

### RetryNotify Callback

The `RetryNotify` callback mechanism allows the agent loop to surface transient "retrying (n/m)" status messages to the user. It is injected into the context via `WithRetryNotify` and retrieved by `SendWithRetry` before each backoff sleep. The `RetryInfo` struct includes the 1-based attempt number, the maximum number of retries, the computed delay, and the error that triggered the retry. This gives the UI layer everything it needs to display an informative status line like "Connection lost, retrying (3/10)…".

### parseRetryAfter

The `parseRetryAfter` function extracts the `Retry-After` header value from the HTTP response. It interprets the value as an integer number of seconds (the most common format). HTTP-date format is not currently supported, as no major provider uses it. A value of 0 is returned for missing or unparseable headers, causing the exponential backoff to apply instead.

---

## Think Splitter

**Source:** `internal/provider/openai/think.go`

The think splitter is a streaming content splitter for providers that inline their chain-of-thought reasoning directly in the `content` field rather than populating a separate `reasoning_content` field. The primary use case is **MiniMax-M3**, which emits `<think>...</think>` blocks at the start of its response content.

### Design Rationale

Some providers — notably MiniMax-M3 — do not support the OpenAI-style `reasoning_content` field for structured chain-of-thought. Instead, they wrap their reasoning in `<think>...</think>` tags within the normal content stream. The think splitter peels these leading reasoning blocks out of the content and routes them to the reasoning text display, so the user sees the model's thought process in the appropriate UI section rather than mixed in with the final answer.

### Activation Guard

The splitter **only activates** when the `<think>` tag appears at the very start of the turn (after trimming leading whitespace). This is critical: if the model's answer merely *mentions* the `<think>` tag — for example, in a code sample or documentation — the splitter must not hijack that content. The `thinkProbe` state accumulates bytes until it can determine whether the stream begins with `<think>` or with ordinary text.

### State Machine

The splitter operates as a three-state machine:

1. **thinkProbe** — The initial state. Bytes are buffered while the splitter checks whether the stream starts with `<think>`. If the accumulated buffer is too short to decide, it returns empty strings for both reasoning and text, waiting for more data. If the buffer clearly doesn't start with `<think>`, it transitions to `thinkPassthrough`.

2. **thinkInside** — Active after `<think>` has been detected. All subsequent bytes are scanned for the closing `</think>` tag. Content before the closing tag is routed as reasoning text. Once `</think>` is found, the splitter transitions to `thinkPassthrough`.

3. **thinkPassthrough** — All bytes are passed through as regular text content. No further `<think>` tags are processed within the same turn.

### markerSuffixLen

The `markerSuffixLen` function is a subtle but essential piece of the streaming splitter. It computes the length of the longest proper suffix of the current buffer that is also a prefix of the closing tag `</think>`. This tail is held back from being emitted as reasoning text, because the rest of the tag might arrive in the next streaming delta. Without this, a tag split across two SSE chunks (e.g., `</thi` then `nk>`) would be missed, and the closing portion would leak into the reasoning output.

### Flush Behavior

When the stream ends, `flush` emits whatever remains in the buffer. An unterminated `<think>` block (one that was never closed) is treated as reasoning text. Any buffered content in the `thinkProbe` or `thinkPassthrough` states is treated as regular text.

---

## Schema Canonicalization

**Source:** `internal/provider/schema_canonicalize.go`

`CanonicalizeSchema` recursively stabilizes a JSON Schema so that semantically identical schemas with different key ordering always produce the same byte representation. This is essential for **prompt-cache stability**.

### Why Canonicalization Matters

Reasonix uses prompt caching to reduce cost and latency on supported providers. The cache key (called the PrefixShape hash) includes the serialized tool schemas. If two functionally identical schemas produce different serialized forms — because their `required` arrays list fields in different orders, for example — they generate different cache keys, causing cache misses that cost real money.

### What Gets Canonicalized

The function targets "set-like" arrays within JSON Schema — specifically the `required` and `dependentRequired` fields. These are semantically unordered sets, but JSON represents them as arrays with a fixed order. `CanonicalizeSchema` sorts these arrays alphabetically so that:

```json
{"required": ["name", "age", "email"]}
```

and:

```json
{"required": ["email", "age", "name"]}
```

produce identical byte representations after canonicalization.

For `dependentRequired`, which is a map of string → string array, the function also sorts the inner arrays alphabetically. The recursion applies to every nested object and array within the schema, ensuring deeply nested `required` fields are also stabilized.

### Fallback Behavior

If the input is not valid JSON, or if marshaling the canonicalized value fails, the original raw bytes are returned unchanged. This ensures that malformed schemas don't cause crashes — they simply bypass canonicalization.

---

## Error Message Localization

**Source:** `internal/control/errmsg.go`

The `explainError` function maps provider HTTP failures to actionable, localized messages so the turn-done error shown in the UI is never a bare status code or silent failure.

### APIError Handling

For `*APIError` values, `explainError` calls `i18n.M.ProviderStatusMessage(status)` to get a localized, human-readable description of the status code (e.g., "Rate limit exceeded, please try again" for 429). For request-shaped 4xx errors (status 400 and 422 specifically), it additionally extracts the provider's reason from the response body using `providerBodyReason`, appending it as a second line. This gives the user both the category ("Bad Request") and the specific cause ("context_length exceeded" or "unpaired tool_calls").

### AuthError Handling

For `*AuthError` values, the localized message is augmented with the `KeyEnv` field — the name of the environment variable that should contain the API key. This produces messages like "Authentication failed (DEEPSEEK_API_KEY)" that immediately tell the user which variable to check, rather than a generic "401 Unauthorized".

### providerBodyReason

The `providerBodyReason` function extracts the human-readable reason from provider error JSON. Both OpenAI and Anthropic use the shape `{"error":{"message":"..."}}` for their error responses. The function parses this structure and returns the inner message. If parsing fails, it falls back to the trimmed raw body. The result is always clamped to 800 runes with `clampRunes` to prevent excessively long error messages from overwhelming the UI.

### clampRunes

The `clampRunes` helper truncates a string to a maximum number of runes and appends an ellipsis character (…) when truncation occurs. It operates on runes rather than bytes to avoid splitting multi-byte UTF-8 characters, which is important for CJK error messages from Chinese providers.
