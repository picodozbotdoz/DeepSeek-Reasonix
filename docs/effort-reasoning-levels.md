# Effort & Reasoning Levels

This document covers the reasoning effort control system in DeepSeek-Reasonix, which allows users to adjust the depth of reasoning that models apply to their requests. The system supports per-provider effort levels, runtime switching via the `/effort` command, and automatic configuration normalization.

**Source:** `internal/config/effort.go`, `internal/serve/serve.go` (for `applyEffortEdit` and `switchEffort`)

---

## Overview

Different LLM providers expose different mechanisms for controlling reasoning depth. DeepSeek uses a three-level system (auto/high/max), Anthropic uses a six-level system (auto/low/medium/high/xhigh/max), and custom providers can define their own levels via configuration. DeepSeek-Reasonix unifies these into a single `/effort` command that maps user input to the appropriate provider-specific setting.

The effort system affects how much "thinking" the model does before responding. Higher effort levels produce more thorough reasoning at the cost of increased latency and token usage. Lower effort levels produce faster responses suitable for simple tasks.

---

## EffortCapability Struct

`EffortCapability` describes the effort levels available for a specific provider:

| Field | Type | Description |
|---|---|---|
| `Supported` | `bool` | Whether this provider supports effort control at all. |
| `Levels` | `[]string` | The user-facing effort levels (always includes `"auto"` as the first entry). |
| `Default` | `string` | The default effort level when the user hasn't explicitly set one. |

### EffortCapabilityForEntry

`EffortCapabilityForEntry(e)` resolves the capability for a given provider entry using the following logic:

1. **Custom providers with `supported_efforts`**: If the provider entry has a `SupportedEfforts` list, those levels (plus `"auto"`) are used. The default is the entry's `DefaultEffort` if it's in the supported list, otherwise the first supported level.

2. **DeepSeek providers**: Detected by `isDeepSeekEntry(e)`, which checks that the kind is `"openai"` and the base URL hostname is `api.deepseek.com` or a subdomain of `deepseek.com`. Returns levels `["auto", "high", "max"]` with default `"high"`.

3. **Anthropic providers**: Detected by `e.Kind == "anthropic"`. Returns levels `["auto", "low", "medium", "high", "xhigh", "max"]` with default `"auto"`.

4. **Other providers**: Returns an empty capability (not supported).

---

## Provider-Specific Behavior

### DeepSeek

DeepSeek's reasoning models support three effort levels:

| User Input | Stored Value | Behavior |
|---|---|---|
| `auto` | `""` (empty) | Provider default (equivalent to "high") |
| `low` | `"high"` | Mapped up to "high" — DeepSeek doesn't support "low" |
| `medium` | `"high"` | Mapped up to "high" — DeepSeek doesn't support "medium" |
| `high` | `"high"` | Standard reasoning depth |
| `xhigh` | `"max"` | Mapped to "max" — DeepSeek doesn't support "xhigh" |
| `max` | `"max"` | Maximum reasoning depth |

The mapping ensures that user input is never silently dropped. When a user requests "low" effort, the system stores "high" (the minimum DeepSeek supports) rather than returning an error. Similarly, "xhigh" is mapped to "max" since DeepSeek doesn't have an intermediate level between "high" and "max."

### Anthropic

Anthropic's models support six effort levels through the adaptive thinking feature:

| User Input | Stored Value | Behavior |
|---|---|---|
| `auto` | `""` (empty) | Provider default (Anthropic decides reasoning depth) |
| `low` | `"low"` | Minimal reasoning |
| `medium` | `"medium"` | Moderate reasoning |
| `high` | `"high"` | Standard reasoning |
| `xhigh` | `"xhigh"` | Extended reasoning |
| `max` | `"max"` | Maximum reasoning |

When effort is explicitly set (any value other than `"auto"`), the system automatically enables Anthropic's **adaptive thinking** feature by setting `thinking: "adaptive"` in the provider configuration. This is required for the effort knob to actually engage — without adaptive thinking, Anthropic models don't adjust their reasoning depth based on the effort parameter.

### Custom Providers

Custom providers can define their own effort levels through the configuration file:

```toml
[[providers]]
name = "my-provider"
kind = "openai"
base_url = "https://my-llm.example.com/v1"
supported_efforts = ["low", "medium", "high", "max"]
default_effort = "medium"
```

The `supported_efforts` list defines the valid levels (excluding `"auto"`, which is always available). The `default_effort` specifies the runtime default when no explicit effort is set.

---

## /effort Command: NormalizeEffort

`NormalizeEffort(e, raw)` maps user input to the value stored in configuration:

1. **Normalize**: The raw input is lowercased and trimmed via `normalizeEffortLevel`.
2. **"auto"**: Returns `""` (empty string), meaning "use provider default." This is the default state when no effort has been explicitly set.
3. **Custom providers**: If the provider has a `SupportedEfforts` list, the input must exactly match one of the supported levels. Otherwise, an error is returned with the valid levels listed (e.g., `"usage: /effort auto|low|medium|high|max"`).
4. **DeepSeek**: Applies the mapping table described above.
5. **Anthropic**: Accepts `low`, `medium`, `high`, `xhigh`, or `max` directly.
6. **Unsupported providers**: Returns an error: `"effort is not configurable for <name>"`.

---

## EffectiveEffort: Runtime Resolution

`EffectiveEffort(e)` resolves the actual effort value used at runtime, following a priority chain:

1. **Explicit effort**: If `e.Effort` is set (non-empty after normalization), it wins. This is the value the user explicitly chose via `/effort`.
2. **Configured default**: If `e.DefaultEffort` is set and is in the `SupportedEfforts` list, it's used. This allows configuration files to set a project-level or user-level default.
3. **First supported level**: If there's a `SupportedEfforts` list but no default, the first level is used.
4. **No supported efforts**: Returns `""` (provider default).

This resolution ensures that explicit user choices always take precedence, followed by configuration defaults, followed by the provider's own default behavior.

---

## EffortDisplay: User-Facing Display

`EffortDisplay(e)` returns the effort level shown to the user in status bars, tab badges, and UI elements:

- If no effort is set (`e.Effort` is empty), displays `"auto"`.
- Otherwise, displays the normalized effort level (e.g., `"high"`, `"max"`).

This gives users a clear view of the current reasoning depth at all times.

---

## Configuration Normalization

### normalizeEffortConfig

`normalizeEffortConfig(c)` is called when the configuration is loaded. It normalizes all provider effort fields:

1. Iterates through every provider entry.
2. Calls `normalizeProviderEffortFields` on each entry.

### normalizeProviderEffortFields

`normalizeProviderEffortFields(e)` normalizes the effort-related fields of a single provider entry:

1. **Effort**: Normalized via `normalizeEffortLevel` (lowercase + trim). If the result is `"off"`, it's set to `""` (empty), as "off" is a legacy value meaning "use provider default."
2. **DefaultEffort**: Normalized the same way.
3. **SupportedEfforts**: Deduplicated and normalized via `normalizedSupportedEfforts`.

### normalizedSupportedEfforts

`normalizedSupportedEfforts(e)` processes the `SupportedEfforts` list:

1. Returns `nil` if the list is empty or the entry is nil.
2. Each level is lowercased and trimmed.
3. Empty strings and `"auto"` are excluded (auto is always available implicitly).
4. Duplicate levels are removed.
5. The order is preserved from the original list.

---

## Runtime Switching: switchEffort

The `/effort` command triggers `switchEffort(ctx, level)` in the HTTP server, which performs the following steps:

1. **Check running state**: If a turn is currently running, the switch is rejected — changing effort mid-turn would produce inconsistent behavior.

2. **Load configuration**: Reads the current configuration to resolve the active provider entry.

3. **Validate capability**: Checks that the provider supports effort control via `EffortCapabilityForEntry`. If not, returns an error.

4. **Normalize input**: Calls `NormalizeEffort` to map the user's input to the stored value.

5. **Apply edit**: Loads the user configuration file into an editable form, then calls `applyEffortEdit`:
   - If the provider doesn't have a block in the config file yet, it's upserted (created) from the resolved entry.
   - For Anthropic providers with a non-empty effort and no existing thinking mode, adaptive thinking is enabled automatically (`thinking: "adaptive"`).
   - The effort level is set on the provider entry.

6. **Save configuration**: Writes the edited configuration back to the user config file.

7. **Rebuild controller**: Calls `switchModel` with the same model reference, which rebuilds the controller with the updated configuration. The conversation history is carried over to the new controller so the user doesn't lose context.

This full rebuild ensures that all provider-specific settings (API parameters, thinking mode, etc.) are correctly applied based on the new effort level.

---

## Summary: Effort Level Flow

```
User types /effort high
        │
        ▼
  NormalizeEffort(entry, "high")
        │
        ▼
  Validate against supported levels
        │
        ▼
  applyEffortEdit: upsert provider, enable adaptive thinking (Anthropic)
        │
        ▼
  Save config to disk
        │
        ▼
  switchModel: rebuild controller with new config
        │
        ▼
  EffortDisplay: show "high" in UI
```

The entire flow is atomic — the configuration is written before the controller is rebuilt, so a crash at any point leaves the system in a consistent state: either the old configuration with the old controller, or the new configuration with the new controller.
