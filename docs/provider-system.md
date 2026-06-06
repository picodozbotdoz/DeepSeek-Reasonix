# Provider System

The provider system is Reasonix's model-backend abstraction. It defines a `Provider` interface, a factory registry keyed by "kind", and concrete implementations for OpenAI-compatible and Anthropic-native endpoints. Adding a new model backend is a config edit (for OpenAI-compatible models) or a new subpackage plus one import (for native protocols).

## Core Interface

```go
// internal/provider/provider.go

type Provider interface {
    Name() string
    Stream(ctx context.Context, req Request) (<-chan Chunk, error)
}
```

- `Name()` returns the provider instance name (e.g. "deepseek", "mimo-pro").
- `Stream()` starts a streaming completion and returns a read-only channel of `Chunk` values. Cancelling the context aborts the underlying request; a closed channel marks the end of the completion.

## Factory Registry

```go
type Factory func(cfg Config) (Provider, error)

func Register(kind string, f Factory)  // called from init()
func New(kind string, cfg Config) (Provider, error)
```

Providers self-register at compile time via `init()`. For example, the OpenAI provider does:

```go
func init() {
    provider.Register("openai", NewOpenAI)
}
```

And `main.go` blank-imports the package to trigger registration:

```go
import _ "reasonix/internal/provider/openai"
```

## Configuration

```go
type Config struct {
    Name    string         // instance name, e.g. "deepseek"
    BaseURL string         // OpenAI-compatible endpoint
    Model   string         // model id
    APIKey  string         // resolved from api_key_env
    Extra   map[string]any // kind-specific options
}
```

A provider is a **vendor endpoint** (one `base_url` + `api_key_env`) that offers one or more models. An entry in `reasonix.toml` declares either:
- A single `model = "..."` (use when a model needs its own base_url/context_window/price), or
- A `models = ["...", "..."]` list with an optional `default` (picking a model reuses the same connection).

A **model reference** (`default_model`, `--model` flag, desktop switcher) resolves via `config.ResolveModel`, which accepts:
- A provider name → its default model
- A bare model name → search across all providers
- An explicit `provider/model`

DeepSeek and MiMo are not code — they are config instances of `kind = "openai"`, differing only in `base_url` / `model` / `api_key_env`.

## Message & Chunk Types

```go
type Message struct {
    Role               Role       `json:"role"`
    Content            string     `json:"content,omitempty"`
    ReasoningContent   string     `json:"reasoning_content,omitempty"`
    ReasoningSignature string     `json:"reasoning_signature,omitempty"`
    ToolCalls          []ToolCall `json:"tool_calls,omitempty"`
    ToolCallID         string     `json:"tool_call_id,omitempty"`
    Name               string     `json:"name,omitempty"`
}

type ChunkType int
const (
    ChunkText ChunkType = iota
    ChunkReasoning
    ChunkToolCallStart
    ChunkToolCall
    ChunkUsage
    ChunkDone
    ChunkError
)

type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    CacheHitTokens   int
    CacheMissTokens  int
    ReasoningTokens  int
    FinishReason     string
}
```

Key design decisions in the chunk stream:
- `ChunkToolCallStart` is emitted immediately (ID + Name, before arguments finish streaming) so the frontend can show a tool card early.
- `ChunkReasoning` carries thinking-mode chain-of-thought. Anthropic extended thinking includes a `Signature` that must be round-tripped verbatim on subsequent turns.
- `CacheHitTokens` / `CacheMissTokens` are normalized from both DeepSeek's `prompt_cache_hit_tokens` and OpenAI/MiMo's `prompt_tokens_details.cached_tokens`.

## OpenAI-Compatible Provider (`internal/provider/openai`)

The `openai` kind implements the standard `/chat/completions` streaming API. It handles:

- **Streaming tool calls** — accumulates partial function-call deltas by index; only complete `ToolCall`s are emitted to avoid downstream parsing issues.
- **Reasoning content** — extracts `reasoning_content` from the DeepSeek-specific response format.
- **Cache-awareness** — reads `prompt_cache_hit_tokens` and `prompt_cache_miss_tokens` from usage.
- **Effort levels** — supports DeepSeek's `effort` parameter (high/max) for controlling reasoning depth.
- **Model fetching** — `FetchModels()` discovers available models from the `/models` endpoint for the model switcher UI.
- **Bounded retry** — on 429/5xx errors, applies exponential backoff with jitter (configurable via `provider.WithRetryNotify`).

### Anthropic-Specific Notes

The `anthropic` kind speaks the Messages API directly (no OpenAI shim). Key differences:
- Extended thinking round-trips the signed reasoning block across tool calls.
- Temperature is not sent (current Claude models reject sampling params).
- Supports `effort` levels: low/medium/high/xhigh/max.
- The `thinking` config field enables adaptive thinking mode.

## Retry Logic

The provider package includes bounded retry logic (`internal/provider/retry.go`) for transient failures:

- Retries on HTTP 429 (rate limit) and 5xx (server error).
- Exponential backoff with jitter.
- Maximum retry count is configurable.
- Retry attempts are surfaced via `provider.WithRetryNotify` so the agent can emit `event.Retrying` events.

## Pricing & Cost Display

```go
type Pricing struct {
    CacheHit float64 `toml:"cache_hit"` // per 1M cached prompt tokens
    Input    float64 `toml:"input"`     // per 1M uncached prompt tokens
    Output   float64 `toml:"output"`    // per 1M completion tokens
    Currency string  `toml:"currency"`
}
```

The `Cost()` method estimates spend for a given `Usage` record. Frontends display this in the status line alongside cache hit rates.

## Auth Errors

The `AuthError` type provides actionable, user-facing messages for HTTP 401/403 responses. It names the provider and the `api_key_env` variable, so the user knows exactly which key to update:

```
authentication failed for provider "deepseek" (HTTP 401): DEEPSEEK_API_KEY is invalid or expired
```

## Tool Schema Canonicalization

Tool schemas are canonicalized once at registration time (`provider.CanonicalizeSchema`) — sorting object properties, normalizing whitespace, etc. — so they stay byte-stable across turns. This is critical for DeepSeek's prefix cache: if tool schemas changed between turns (e.g. from JSON re-serialization), the entire prefix would miss the cache.

## Tool Pairing Sanitization

The `SanitizeToolPairing` function repairs session histories so they satisfy the API contract: every assistant `tool_calls` entry must be answered by a following tool message for its ID. This handles edge cases like interrupted sessions where some tool calls never completed.

## Adding a New Provider Kind

1. Create `internal/provider/myprovider/` with a `Provider` implementation.
2. Register via `func init() { provider.Register("mykind", New) }`.
3. `main.go` blank-imports the package.
4. The provider is available from config with `kind = "mykind"`.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Agent Loop & Coordinator](agent-loop-coordinator.md)
- [Configuration Reference](configuration-reference.md)
