# Custom Slash Commands

Custom slash commands are user-defined prompt templates loaded from Markdown files. They allow teams and individuals to create reusable workflows — code reviews, commit message generation, refactoring checklists, and more — without modifying Reasonix's code. Commands are discovered from specific directories, support argument substitution, and can be namespaced by directory structure.

## Command Format

A custom command is a Markdown file with an optional YAML-like frontmatter block:

```markdown
---
description: Review a file for bugs
argument-hint: [path]
---
Read $1 and list any correctness bugs or risky patterns, with file:line references, most important first. Focus: $ARGUMENTS.
```

### Frontmatter Fields

| Field | Required | Description |
|-------|----------|-------------|
| `description` | No | One-line description shown in the slash command menu |
| `argument-hint` | No | Hint for argument usage (e.g., `[path]`, `<file> <pattern>`) |

If no frontmatter is present, the entire file content is the command body.

### Frontmatter Parsing

The frontmatter parser (`internal/frontmatter/`) is intentionally minimal:

- Uses `---` fences (standard Markdown frontmatter convention)
- Parses `key: value` lines (lowercased keys, trimmed/quoted values)
- Supports section headers (e.g., `metadata:`) whose nested `key: value` lines flatten
- Supports YAML-style lists (`- item`) joined comma-separated for compatibility with other agent tools
- No YAML dependency — keeps Reasonix's single-(TOML)-dependency promise

## Argument Substitution

The command body supports several substitution tokens:

| Token | Replaced With |
|-------|--------------|
| `$ARGUMENTS` | All arguments joined by spaces |
| `$1`, `$2`, ..., `$N` | Positional arguments (empty when absent) |
| `$$` | Literal `$` character |

### Substitution Implementation

```go
var substRe = regexp.MustCompile(`\$(\$|ARGUMENTS|[0-9]+)`)
```

The substitution uses a regular expression that matches `$ARGUMENTS`, `$1`..`$9`, and `$$`. Positional arguments that exceed the actual argument count resolve to empty strings, so templates gracefully handle missing arguments.

### Example

Given a command file `review.md`:

```markdown
---
description: Review a file for bugs
argument-hint: [path]
---
Read $1 and list any correctness bugs or risky patterns, with file:line references, most important first. Focus: $ARGUMENTS.
```

Invoking `/review src/auth/login.go security issues` produces:

```
Read src/auth/login.go and list any correctness bugs or risky patterns, with file:line references, most important first. Focus: security issues.
```

## Command Discovery

### Directory Layout

Commands are loaded from multiple directories, allowing project-level and user-level commands:

```
~/.reasonix/commands/         ← user-level commands
.project/.reasonix/commands/  ← project-level commands
```

The `Load` function processes directories in order, so a later directory overrides an earlier one on name clash. This means project-level commands take precedence over user-level commands.

### Name Derivation

Command names are derived from the file path:

- `review.md` → `/review`
- `git/commit.md` → `/git:commit`
- `security/audit.md` → `/security:audit`

Directory separators become colon (`:`) separators in the command name, providing a natural namespace hierarchy.

### Loading Process

```go
func Load(dirs ...string) ([]Command, error)
```

1. For each directory, scan for `*.md` files
2. Parse frontmatter and extract the body
3. Derive the command name from the file path
4. Later directories override earlier ones on name clash
5. Individual file failures are collected into the returned error but don't prevent other commands from loading
6. The result is sorted by name

## Built-in Command

Reasonix ships one built-in custom command:

### `/review`

```markdown
---
description: Review a file for bugs
argument-hint: [path]
---
Read $1 and list any correctness bugs or risky patterns, with file:line references, most important first. Focus: $ARGUMENTS.
```

Located at `.reasonix/commands/review.md`, this command provides a quick code review workflow.

## Slash Tool Integration

The `slashtool.go` file in the `command` package integrates custom commands with the slash command system:

- Custom commands appear in the `/help` listing
- They're completable in the slash command menu
- Arguments are passed through the substitution system
- The rendered template is sent as a user turn to the controller

## Creating Custom Commands

### Step-by-Step

1. Create a `.md` file in `~/.reasonix/commands/` (user-level) or `.reasonix/commands/` (project-level)
2. Add optional frontmatter with `description` and `argument-hint`
3. Write the prompt template using `$ARGUMENTS` and `$1`..`$N` for substitution
4. The command appears immediately in the slash menu (no restart needed)

### Best Practices

- **Keep prompts focused**: A single command should do one thing well
- **Use argument hints**: Help users know what arguments to provide
- **Leverage positional args**: `$1`, `$2` are more precise than `$ARGUMENTS` for multi-argument commands
- **Namespace with directories**: Group related commands (e.g., `git/commit.md`, `git/rebase.md`) for clean organization
- **Project-level vs user-level**: Put project-specific commands in the project's `.reasonix/commands/` directory; put personal commands in `~/.reasonix/commands/`

### Example Commands

**Git Commit Message** (`git/commit.md`):
```markdown
---
description: Generate a git commit message
argument-hint: [scope]
---
Look at the staged changes and generate a concise, conventional commit message. Scope: $ARGUMENTS. Format: type(scope): description
```

**Refactor Checklist** (`refactor/checklist.md`):
```markdown
---
description: Generate a refactoring checklist
argument-hint: <file>
---
Read $1 and generate a refactoring checklist covering: code smell detection, naming improvements, extraction opportunities, and test coverage gaps.
```

**Documentation Generator** (`docs/generate.md`):
```markdown
---
description: Generate documentation for a module
argument-hint: <path>
---
Read all files in $1 and generate comprehensive package documentation including: overview, API reference, usage examples, and integration notes.
```
