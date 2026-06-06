# Memory & Skills

Reasonix has two complementary systems for extending the agent's knowledge and capabilities: **Memory** (persistent context loaded into every session) and **Skills** (invokable playbooks the model can use on demand).

## Memory System

The memory system (`internal/memory/`) provides persistent context that the agent carries across sessions. It is designed to be deterministic and cache-friendly — the same files produce the same memory block, which keeps the prompt prefix stable for DeepSeek's prefix cache.

### Hierarchical Docs

Memory is loaded from a hierarchy of Markdown files, with later entries taking precedence:

| Scope | File | Git-ignored | Purpose |
|-------|------|-------------|---------|
| User-global | `~/.config/reasonix/REASONIX.md` | No | Personal standing instructions |
| Ancestor | `<ancestor>/REASONIX.md` | No | Shared project context |
| Project | `./REASONIX.md` | No | Project-specific instructions |
| Local | `./REASONIX.local.md` | Yes | Personal project notes |

`AGENTS.md` and `CLAUDE.md` are accepted as fallback names, providing compatibility with other agent tools.

### The Memory Set

```go
type Set struct {
    Docs    []Source   // REASONIX.md / AGENTS.md, ascending precedence
    Store   Store      // auto-memory store
    Index   string     // MEMORY.md contents at load time
    CWD     string     // project working dir
    UserDir string     // user config root
}
```

`Load(opts)` discovers all memory for a session. It is best-effort and never errors — missing files just mean less memory.

### Memory Block

`Set.Block()` renders the memory as a single Markdown section. It is deterministic given the same files, which is what keeps it a stable cache prefix across sessions that don't change their memory.

```go
func Compose(base string, s *Set) string
```

`Compose` folds the memory block onto the base system prompt. Base stays first (it is the most stable text), memory follows. With no memory, base is returned unchanged.

### Auto-Memory Store

The auto-memory store (`Store`) manages durable facts saved by the `remember` tool:
- Facts are stored as frontmatter Markdown files under `~/.config/reasonix/memory/`.
- A `MEMORY.md` index file lists all saved memories with links.
- The `remember` tool saves a named fact with optional scope (project/local).
- The `forget` tool deletes a memory by name.
- The `quickadd` tool appends a one-liner to the project doc.

### Queue Integration

The controller implements `memory.Queue` so the `remember`/`forget` tools can fold a turn-tail note about a just-made memory change into the next turn — without touching the cache-stable prefix. The memory applies this session via the turn tail and joins the prefix naturally on the next session.

### WriteDoc / AppendDoc

The `Set.WriteDoc()` and `Set.AppendDoc()` methods allow frontends (desktop memory panel) to edit memory files in-place. The write lands on disk immediately but does NOT mutate the cache-stable system prefix — the edit folds into the prefix on the next session.

### Allowed Paths

Only recognized memory files may be written — the `allowedDocPaths()` method bounds frontend-driven writes to the canonical file for each writable scope plus every doc already discovered this session.

## Skills System

The skills system (`internal/skill/`) loads invokable playbooks from Markdown files. A skill is a named, described prompt body the model can invoke via the `run_skill` tool or the user via `/<name>`.

### Skill Structure

```go
type Skill struct {
    Name         string   // canonical identifier
    Description  string   // one-liner shown in the pinned index
    Body         string   // full markdown body (post-frontmatter)
    Scope        Scope    // where it came from
    Path         string   // absolute path to the SKILL.md / <name>.md
    AllowedTools []string // scoped tools for subagent skills
    RunAs        RunAs    // inline | subagent
    Model        string   // optional model override for subagent
}
```

### Run Modes

| Mode | Description |
|------|-------------|
| `inline` | Folds the body into the parent turn as a tool result |
| `subagent` | Spawns an isolated child loop and returns only the final answer |

Subagent skills run in a separate session with their own tool registry (optionally scoped via `AllowedTools`). Their tool calls and reasoning never enter the parent context.

### Discovery

Skills are discovered from multiple roots, highest priority first:

1. **Project scope** — `.reasonix/skills/`, `.agents/skills/`, `.agent/skills/`, `.claude/skills/` under the project root
2. **Custom scope** — paths from `[skills].paths` config
3. **Global scope** — the same convention dirs under the home directory
4. **Built-in scope** — shipped skills (explore, research, review, security_review, test)

On a name collision, the higher-priority root wins. Only names and descriptions enter the cache-stable system-prompt index — bodies load on demand.

### Skill File Format

Skills are Markdown files with optional frontmatter:

```markdown
---
name: my-skill
description: One-liner — what does this skill do?
runas: subagent
allowed-tools: read_file, grep, bash
model: deepseek-pro
---

# My Skill

Instructions for the model to follow when this skill is invoked.
```

Two layouts are supported:
- **Flat** — `<name>.md` in a skills directory
- **Directory** — `<name>/SKILL.md` with an optional `<name>/references/*.md` for depth material

Symlinks are followed, so a linked skill directory or flat `.md` is picked up like a real one.

### Built-in Skills

| Skill | Description |
|-------|-------------|
| `explore` | Explore and understand a codebase |
| `research` | Research a topic and produce findings |
| `review` | Review code for issues |
| `security_review` | Security-focused code review |
| `test` | Write and run tests |

### Skill Index

Only the name + description index enters the cache-stable system prompt (so adding a skill body doesn't bust the prefix). The `skill/index.go` module builds this compact index, sorted by name for stability.

### Skill Tools

Two tools surface skills to the model:
- `run_skill` — invoke a skill by name with arguments
- `explore` / `research` / `review` / `security_review` / `test` — direct shortcuts for built-ins

### Managing Skills

The `/skill` slash command provides:
- `list` — show all discoverable skills
- `show <name>` — display a skill's body
- `enable <name>` — un-hide a disabled skill
- `disable <name>` — hide a skill
- `new <name>` — scaffold a new skill stub
- `paths` — show discovery roots with status

### Disabled Skills

`[skills].disabled_skills` in config hides skills from the model's index, slash invocation, and skill tools — without deleting the files.

### Creating a New Skill

`Store.Create(name, scope)` scaffolds a new skill stub with minimal frontmatter plus guidance. It refuses to clobber existing files (uses `O_EXCL`).

## See Also

- [Architecture Overview](architecture-overview.md)
- [Controller & Frontends](controller-frontends.md)
- [Configuration Reference](configuration-reference.md)
