# Frontmatter Parser

This document covers the minimal, dependency-free YAML-like frontmatter parser used throughout DeepSeek-Reasonix.

**Source:** `internal/frontmatter/frontmatter.go`

---

## Overview

Reasonix uses `---`-fenced "key: value" blocks to prefix skill definitions, custom slash commands, memory files, and output style templates. The frontmatter parser extracts these key-value pairs and the remaining body content without pulling in a full YAML library, keeping Reasonix's single-(TOML)-dependency promise. This means the entire Reasonix binary depends on only one heavy serialization library (BurntSushi/toml for configuration), with the frontmatter parser implementing just enough YAML-like parsing to cover its use cases.

---

## Split Function

The `Split` function is the sole public API. It takes a raw string input and returns:

1. A `map[string]string` of parsed frontmatter keys (all lowercased)
2. The remaining body string after the closing `---` fence

### Fence Detection

The parser looks for an opening `---` on the first line (after trimming). If the first line is not exactly `---`, the entire input is treated as body with an empty frontmatter map. This "all or nothing" approach means you can't accidentally parse a file that happens to contain `---` in the middle of its content.

### Closing Fence

The parser scans forward from the opening fence to find a matching closing `---`. Everything between the two fences is parsed as frontmatter; everything after is the body. If the opening fence is never closed, the **entire input is treated as body** — no partial parse occurs. This is a critical safety measure: a malformed file with an unclosed fence should not silently lose its content to a partially parsed frontmatter map.

### Value Processing

Values undergo the following transformations:

1. **Whitespace trimming**: Leading and trailing whitespace is stripped from both key and value
2. **Quote stripping**: Outer double quotes (`"`) or single quotes (`'`) are removed from the value, supporting both `key: "value with spaces"` and `key: 'value with spaces'`
3. **Key lowercasing**: All keys are lowercased so that `Name:` and `name:` produce the same entry. This avoids case-sensitivity bugs in downstream consumers.
4. **Last-write-wins**: If a key appears multiple times, the last occurrence wins. This allows later entries to override earlier ones, which is useful when composing frontmatter from multiple sources.

---

## Empty Values and Section Headers

A key with an empty value (e.g., `metadata:`) serves as a section header. The parser handles two patterns for content under section headers:

### YAML List Pattern

If the lines following an empty-value key start with `- ` (YAML list syntax), they are collected as list items and joined comma-separated. For example:

```yaml
allowed-tools:
- read_file
- grep
- glob
```

Produces: `fm["allowed-tools"] = "read_file, grep, glob"`

This is particularly important for skills authored for other agent tools (like Claude Code or Cursor) that use YAML list syntax for the `allowed-tools` key. By joining them comma-separated, Reasonix can consume these skills without modification.

### Nested Key-Value Flattening

If the lines following an empty-value key contain `key: value` pairs (indented or not), they are left for the outer loop to parse as independent entries. For example:

```yaml
metadata:
  type: skill
  version: 2
```

Produces: `fm["type"] = "skill"`, `fm["version"] = "2"` (the `metadata` key itself is not set, since its value is empty and no list items follow)

This flattening behavior means nested YAML structures are collapsed to a single level, which is sufficient for Reasonix's frontmatter needs where keys are unique across the entire document.

---

## List Item Parsing

List items (`- item`) are parsed with the following rules:

- The `-` prefix must be the first non-whitespace character on the line
- The item text is trimmed of whitespace and outer quotes (same as regular values)
- Collection stops at the first line that doesn't start with `- ` (allowing the outer loop to handle it as a regular key-value or skip it)
- The `j` index advances past consumed list items so they aren't re-parsed by the outer loop

---

## Use Cases

The frontmatter parser is used in four subsystems:

### 1. Skills (`internal/skill/`)

Skill definition files start with frontmatter specifying the skill name, description, allowed tools, and other metadata. The body contains the skill's prompt template. Example:

```yaml
---
name: git-commit
description: Generate a git commit message from staged changes
allowed-tools:
- read_file
- bash
---
Generate a concise, descriptive commit message for the currently staged changes...
```

### 2. Custom Slash Commands (`internal/command/`)

User-defined slash commands use frontmatter for the command name, description, and configuration. The body is the prompt template that replaces the slash command invocation.

### 3. Memory Files (`internal/memory/`)

Memory files can include frontmatter for metadata like creation date or category tags. The body contains the memory content that gets injected into the system prompt.

### 4. Output Styles (`internal/outputstyle/`)

Output style templates use frontmatter for the style name and formatting options. The body contains the template that controls how the model's output is rendered.

---

## Zero Dependencies

The frontmatter parser imports only the standard library's `strings` package. This is a deliberate architectural choice: Reasonix's only serialization dependency is BurntSushi's TOML parser (needed for `reasonix.toml` configuration). Adding a YAML library like `gopkg.in/yaml.v3` would more than double the serialization dependency footprint for a feature that needs only a tiny subset of YAML syntax.

The "just enough YAML" approach means some full-YAML features are not supported:

- **Multiline values** (using `|` or `>` block scalars) are not parsed; the value is the rest of the line after the colon
- **Anchors and aliases** (`&anchor` / `*alias`) are not supported
- **Typed values** (numbers, booleans, null) are always strings; `true` is the string `"true"`, not a boolean
- **Nested objects** are flattened to a single level
- **Flow collections** (`[a, b, c]` or `{key: value}`) are not parsed

These limitations are acceptable because Reasonix's frontmatter schemas are designed to be flat key-value pairs plus optional comma-separated lists, which the parser handles correctly.
