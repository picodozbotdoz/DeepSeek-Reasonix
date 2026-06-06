# Configuration Reference

Reasonix uses TOML configuration files with a resolution hierarchy that keeps secrets out of files and lets project-level settings override user-level ones.

## Resolution Order

**Flag > `./reasonix.toml` (project) > `~/.config/reasonix/config.toml` (user) > built-in defaults**

Secrets come from the environment via `api_key_env` and are never stored in config files. A `.env` in the working directory is loaded if present.

## Full Schema

### Top-Level

```toml
default_model = "deepseek-flash"   # provider name or "provider/model"
language      = "zh"               # ui language; empty = auto-detect from $LANG / $REASONIX_LANG
```

### `[ui]`

```toml
[ui]
theme       = "auto"       # auto|dark|light; REASONIX_THEME can override per run
theme_style = "graphite"   # graphite|ember|aurora|midnight|sandstone|porcelain|linen|glacier
```

### `[agent]`

```toml
[agent]
system_prompt      = """You are Reasonix, a coding agent..."""  # or system_prompt_file = "prompts/system.md"
max_steps          = 25          # max tool-call rounds (0 = unlimited)
temperature        = 0.0         # sampling temperature
auto_plan          = "off"       # off|on; automatic plan mode for complex tasks
auto_plan_classifier = "deepseek-flash"  # optional; cheap model for borderline classification
soft_compact_ratio  = 0.5        # notice when prompt reaches this fraction
compact_ratio       = 0.8        # try compacting at this fraction
compact_force_ratio = 0.9        # force compacting at this fraction
planner_model       = "mimo-pro" # optional: enable two-model collaboration
subagent_model      = "deepseek-pro"  # default model for runAs=subagent skills
output_style        = "explanatory"   # explanatory|learning|concise, or custom .reasonix/output-styles/<name>.md
```

#### `subagent_models`

Per-skill model overrides:

```toml
[agent]
subagent_models = { review = "deepseek-pro", security_review = "deepseek-pro" }
```

### `[[providers]]`

Each provider entry declares a vendor endpoint:

```toml
[[providers]]
name           = "deepseek"            # unique instance name
kind           = "openai"              # provider kind: openai|anthropic
base_url       = "https://api.deepseek.com"
models         = ["deepseek-v4-flash", "deepseek-v4-pro"]  # or model = "single-model"
default        = "deepseek-v4-flash"   # optional; defaults to models[0]
api_key_env    = "DEEPSEEK_API_KEY"    # environment variable holding the key
context_window = 1000000               # tokens; 0 disables compaction
price          = { cache_hit = 0.02, input = 1, output = 2, currency = "¥" }
effort         = "high"                # reasoning effort: high|max (DeepSeek); low|medium|high|xhigh|max (Anthropic)
thinking       = "adaptive"            # Anthropic only; enables extended thinking
```

#### Multi-Model Providers

A provider with `models = [...]` exposes several models under one `base_url` + `api_key_env`. Switching models reuses the same connection. Models that need distinct `context_window` or `price` should be separate single-`model` entries.

#### Model References

A model reference resolves via:
- Provider name → its default model (`"deepseek"` → `"deepseek-v4-flash"`)
- Bare model name → search across providers
- Explicit `"provider/model"` → exact match

### `[tools]`

```toml
[tools]
enabled = []   # empty = all built-ins; list specific names to restrict
```

### `[codegraph]`

```toml
[codegraph]
enabled      = false       # enable CodeGraph code intelligence
auto_install = true        # fetch runtime when missing
path         = ""          # empty = cache, then PATH, then bundle beside reasonix
tier         = "lazy"      # lazy|background|eager
```

### `[skills]`

```toml
[skills]
paths            = ["~/my-skills", "../shared/skills"]  # extra custom skill roots
disabled_skills  = ["review"]                           # hide specific skills
```

### `[permissions]`

```toml
[permissions]
mode  = "ask"                                # writer fallback: ask|allow|deny
deny  = ["bash(rm -rf*)", "bash(git push*)"] # hard-blocked in every mode
allow = ["bash(go test*)"]                   # never prompted
ask   = []                                   # force a prompt even if otherwise allowed
```

### `[sandbox]`

```toml
[sandbox]
# workspace_root = ""          # file-writers confined here; empty = cwd
# allow_write    = ["/tmp"]    # extra dirs write_file/edit_file/multi_edit may touch
# bash           = "enforce"   # macOS: enforce Seatbelt; empty/off = unconfined
# network        = true        # allow network egress from sandboxed bash
```

### `[[plugins]]`

```toml
[[plugins]]
name    = "example"           # unique server name
type    = "stdio"             # stdio|http|sse
command = "reasonix-plugin-example"  # for stdio: executable to launch
args    = []                  # for stdio: arguments
env     = { FOO = "bar" }    # for stdio: environment variables
url     = ""                  # for http: server URL
headers = {}                  # for http: static headers (${VAR} expansion)
tier    = "lazy"              # eager|lazy|background
```

#### Tier Guidance

| Tier | Behavior |
|------|----------|
| `eager` | Handshake at boot; agent waits. Use when tools must be in the first turn's system prompt. |
| `lazy` | Placeholder tools (from cached schema) until the model uses them, then handshake on-demand. **Default.** |
| `background` | Placeholder + spawn at boot in a goroutine so an idle session warms up without blocking. |

### `[statusline]`

```toml
[statusline]
command = "my-statusline.sh"  # custom status line command; receives JSON on stdin
```

### `.mcp.json` (Alternative Plugin Source)

A project-root `.mcp.json` using Claude Code's exact schema is read and merged into `[[plugins]]`. On name collision, `reasonix.toml` wins.

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/path"]
    },
    "stripe": {
      "type": "http",
      "url": "https://mcp.stripe.com",
      "headers": { "Authorization": "Bearer ${STRIPE_KEY}" }
    }
  }
}
```

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `DEEPSEEK_API_KEY` | DeepSeek API key |
| `MIMO_API_KEY` | MiMo API key |
| `ANTHROPIC_API_KEY` | Anthropic API key |
| `REASONIX_LANG` | Override UI language |
| `REASONIX_THEME` | Override theme per run |
| `REASONIX_CODEGRAPH_BIN` | Path to CodeGraph binary |
| `REASONIX_CODEGRAPH_E2E` | Enable CodeGraph e2e test mode |

Custom `api_key_env` values can be declared per provider.

## `.env` Loading

A `.env` file in the working directory is loaded at startup. This is the recommended way to set API keys locally without exporting them to the shell environment.

## Config Migration

The config system automatically migrates settings from older versions. On first launch, it imports from the legacy v0.x `~/.reasonix/config.json` (non-destructive — the old file is left untouched).

## Settings.json (Hooks)

Hooks are configured separately in `settings.json` (not `reasonix.toml`):
- Global: `~/.reasonix/settings.json`
- Project: `<project>/.reasonix/settings.json` (only after trust)

See [Hooks System](hooks-system.md) for details.

## Setup Wizard

`reasonix setup` provides an interactive configuration wizard that creates a minimal `reasonix.toml` with the selected provider and API key environment variable. It keeps first-run friction low.

## See Also

- [Architecture Overview](architecture-overview.md)
- [Provider System](provider-system.md)
- [MCP Plugin System](mcp-plugin-system.md)
- [Permission & Sandbox](permission-sandbox.md)
