# Host Verify Checks

This document covers the host verify checks system in DeepSeek-Reasonix, which allows project memory files to declare hard-gated verification commands that the host runtime enforces before proceeding with certain operations. This is a critical safety mechanism that elevates specific project instructions from advisory guidance to mandatory gates.

**Source:** `internal/instruction/instruction.go`

---

## Overview

DeepSeek-Reasonix uses project memory files (`.reasonix/memory/`) to store instructions, conventions, and guidelines that shape the agent's behavior. Most of these instructions are **advisory** — they guide the agent but don't block it. The host verify checks system introduces a special category of instructions that become **hard gates**: commands the host runtime must execute and verify before proceeding.

This distinction is essential for safety-critical workflows where certain checks must pass before the agent can make changes — for example, ensuring that a build succeeds, that tests pass, or that a linter is clean before submitting code.

---

## VerifyCheck Struct

The `VerifyCheck` struct represents a single verification command extracted from project memory:

| Field | Type | Description |
|---|---|---|
| `Command` | `string` | The shell command to execute for verification. |
| `SourcePath` | `string` | The file path of the memory document that declared this check. |
| `Line` | `int` | The 1-based line number within the source file where the check was defined. |

`VerifyCheck` is a **runtime-only** type — it is never serialized into prompts sent to the model. This design ensures that the model cannot inspect, modify, or bypass the checks; they are enforced entirely by the host runtime.

---

## Structured Convention: "Reasonix host checks"

Host checks are declared using a specific structured section in project memory files. The convention is:

```markdown
## Reasonix host checks

- verify: cargo test --all
- verify: cargo clippy -- -D warnings
- verify: npm run lint
```

### Parsing Rules

The `ExtractHostChecks` function scans all project memory documents and extracts verify checks using the following rules:

1. **Heading detection**: Lines matching the pattern `## Reasonix host checks` (case-insensitive via `strings.EqualFold`) activate check extraction. The heading can use any number of `#` prefixes (e.g., `### Reasonix host checks`), as long as it's a valid Markdown heading.

2. **Section scoping**: Only lines that appear **after** the heading and **before** the next heading of any level are considered part of the verify checks section. Once a new heading is encountered, the section ends regardless of heading level.

3. **Bullet parsing**: Each line starting with `- verify:` or `* verify:` (case-insensitive for the `verify:` prefix) is parsed as a verification command. The command text is everything after `verify:`, trimmed of leading and trailing whitespace.

4. **Deduplication**: Commands are deduplicated by their text content. If the same command appears in multiple memory files or multiple times in the same file, only the first occurrence is kept. This prevents redundant checks from slowing down the verification process.

---

## ExtractHostChecks

`ExtractHostChecks(docs []memory.Source) []VerifyCheck` is the main extraction function. It processes each memory document in order:

1. Iterates through each line of the document body.
2. Detects Markdown headings via `markdownHeading(line)`.
3. When a heading matching "Reasonix host checks" is found (case-insensitive), enters the section.
4. Any subsequent heading exits the section.
5. Within the section, parses verify bullets via `verifyBullet(line)`.
6. Deduplicates by command text using a `seen` map.
7. Records the `SourcePath` and `Line` number for each extracted check.

The function returns a slice of `VerifyCheck` values. An empty or nil slice means no checks were found.

---

## Context Injection Pattern

Extracted checks are propagated through the system using Go's `context.Context`:

### WithChecks

`WithChecks(ctx, checks)` stores a defensive copy of the checks slice in the context. If the checks slice is empty, the original context is returned unchanged. The copy is made via `append([]VerifyCheck(nil), checks...)` to prevent aliasing.

### FromContext

`FromContext(ctx)` retrieves the checks from the context. If no checks are stored (or the value is not of the expected type), it returns `nil`. Like `WithChecks`, it returns a defensive copy to prevent the caller from mutating the stored slice.

This pattern ensures that checks flow through the call stack without global state, making the system testable and goroutine-safe.

---

## Markdown Heading Parser

`markdownHeading(line)` parses a single line as a Markdown heading:

1. The line is trimmed of whitespace.
2. It must start with one or more `#` characters.
3. There must be a space after the last `#` (rejecting `###no-space` patterns).
4. The heading text is everything after the `# ` prefix, with trailing `#` characters stripped and the result trimmed again.
5. Empty headings (e.g., `## `) return `("", false)`.

This parser handles common Markdown variants, including ATX headings with trailing `#` characters (e.g., `## Heading ##`).

---

## Verify Bullet Parser

`verifyBullet(line)` parses a single line as a verify check bullet:

1. The line is trimmed of whitespace.
2. It must start with `- ` or `* ` (the two standard Markdown unordered list markers).
3. The body after the marker must start with `verify:` (case-insensitive).
4. The command is everything after `verify:`, trimmed of whitespace.
5. Empty commands (e.g., `- verify:`) return `("", false)`.

Both `- verify:` and `* verify:` are supported to accommodate different Markdown style preferences.

---

## Distinction from Ordinary Instructions

It is critical to understand the difference between ordinary project instructions and host verify checks:

| Aspect | Ordinary Instructions | Host Verify Checks |
|---|---|---|
| **Location** | Any section in memory files | Only under `## Reasonix host checks` |
| **Enforcement** | Advisory — the model is guided but not blocked | Hard gate — the host runtime enforces them |
| **Serialization** | Included in prompts sent to the model | Never serialized into prompts |
| **Bypass** | The model may choose to ignore them | Cannot be bypassed by the model |
| **Syntax** | Free-form text | Structured `- verify: <command>` bullets |

This separation ensures that safety-critical checks are enforced at the runtime level, not at the model level. The model cannot reason its way out of a failed verify check — the host simply blocks the operation until the check passes.

---

## Example Usage

A project might define the following in `.reasonix/memory/project.md`:

```markdown
# Project Conventions

Always run tests before committing. Use conventional commits format.

## Reasonix host checks

- verify: go test ./...
- verify: go vet ./...
- verify: golint ./...
```

In this example:

- The first paragraph is ordinary guidance — the model is told to run tests and use conventional commits, but it can choose to ignore this advice.
- The three verify checks are hard gates — the host runtime will execute `go test ./...`, `go vet ./...`, and `golint ./...` and block the operation if any of them fail.

This gives project maintainers a powerful tool for enforcing quality standards without relying on the model's compliance with free-form instructions.
