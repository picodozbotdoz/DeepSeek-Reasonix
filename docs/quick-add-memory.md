# Quick-Add Memory System

This document covers the `#` quick-add feature for Reasonix's memory system, which allows users to rapidly append one-line notes to the project memory file.

**Source:** `internal/memory/quickadd.go`

---

## Overview

The quick-add feature provides a lightweight mechanism for users to capture fleeting thoughts, observations, or reminders without leaving the conversation flow. By typing `# some note` in the chat composer, the user appends a bullet point under a dedicated `## Notes` section in the project memory file. The note is normalized to a single line so it can't corrupt the section structure, and it's placed in chronological order so later reorganization is straightforward.

---

## AppendDoc

`AppendDoc` is the primary function. It appends a one-line note as a bullet under the `## Notes` section in the doc-memory file at the given path. The function handles three cases:

### Case 1: New File

When the memory file doesn't exist (or is empty after trimming), `AppendDoc` creates a complete new file with the standard structure:

```markdown
# Project memory

## Notes

- the user's note goes here
```

This ensures the file always has a valid top-level heading and the `## Notes` section from the start, providing a consistent structure that the memory display system can parse reliably.

### Case 2: Existing File with `## Notes` Section

When the file exists and already contains the `## Notes` heading, `AppendDoc` uses `insertUnderHeading` to place the new bullet at the end of the Notes section, maintaining chronological order. The bullet is inserted just before the next heading (a line starting with `#`) or at the end of the file if no subsequent heading exists.

### Case 3: Existing File without `## Notes` Section

When the file exists but doesn't contain the `## Notes` heading, `AppendDoc` appends the section and the bullet after the existing content:

```markdown
(existing content)

## Notes

- the user's note goes here
```

This preserves any hand-written content in the memory file while adding the Notes section at the end.

---

## insertUnderHeading

The `insertUnderHeading` function handles the delicate task of inserting a bullet at the end of a section defined by a markdown heading. The algorithm works as follows:

1. **Find the heading**: Scan lines to find the `## Notes` heading (exact match after trimming)
2. **Find the section end**: Starting from the line after the heading, scan forward for the next line that starts with `#` (any heading level). This marks the start of the next section.
3. **Trim trailing blanks**: Walk backward from the section end to skip any blank lines within the section. This prevents accumulation of blank lines between bullets.
4. **Insert the bullet**: Insert the bullet line at the trimmed position, then append the remaining lines (including any blank lines and the next section's heading).

### Edge Case Handling

- If the heading isn't found (shouldn't happen, since the caller checks with `strings.Contains`), the function falls back to appending the bullet at the end of the file
- The section end defaults to `len(lines)` (end of file) if no subsequent heading is found
- Blank lines within the section are preserved in their original position; only trailing blank lines are skipped before the insertion point

---

## oneLine Normalization

The `oneLine` function (referenced but defined in the parent package) collapses a note to a single line. This is essential for the quick-add feature because:

1. **Section integrity**: A multiline note could contain markdown headings that would break the section structure, causing `insertUnderHeading` to misidentify section boundaries
2. **Consistent display**: The memory panel displays notes as single-line bullets; multiline content would create visual inconsistency
3. **Simple reorganization**: Users who want to expand a quick note into a longer entry can later edit the memory file by hand, moving the bullet into a more detailed section

---

## writeDocFile

`writeDocFile` is a helper used by `Set.WriteDoc` for the panel's in-place editor. It overwrites the file at the given path with the provided body content, creating the parent directory if necessary and ensuring a single trailing newline. The path validation (ensuring the path is within the memory directory) happens in the caller, keeping `writeDocFile` simple and focused.

### Single Trailing Newline

The function trims all trailing newlines from the body and then appends exactly one. This normalization ensures that:
- The file always ends cleanly (no missing newline at EOF)
- There are no extra blank lines at the end (which would accumulate with each save)
- The `AppendDoc` function can rely on consistent file endings when inserting new bullets

---

## Design Philosophy

The quick-add feature is designed around the principle of **low-friction capture with deferred organization**:

1. **Capture now**: The `#` prefix is the shortest possible trigger — one character — minimizing the effort to save a thought
2. **Don't scatter**: All quick-added notes accumulate under `## Notes` rather than being appended at the end of the file or inserted at a random position. This keeps hand-written content in its original form and order.
3. **Reorganize later**: The user can open the memory file in the panel editor and move notes from `## Notes` into more specific sections, add context, or delete obsolete entries. The system doesn't try to be smart about categorization; it just gets the note on paper.
4. **Chronological order**: Notes are appended in the order they're added, creating a timeline that's easy to scan. The most recent note is always at the bottom of the section.

This design mirrors the common practice of maintaining a "scratch" section in personal knowledge management systems, where quick captures go first and are filed properly later.
