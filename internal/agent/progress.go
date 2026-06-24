package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/fileutil"
)

// ProgressFile manages a structured PROGRESS.md that bridges context windows
// across sub-agent sessions. It follows the "file-as-bus" pattern: the manager
// writes progress, sub-agents read it on startup.
type ProgressFile struct {
	path string // absolute path to PROGRESS.md
}

// NewProgressFile creates a ProgressFile at the given directory. The file is
// created on first Write; Read returns empty string if the file doesn't exist.
func NewProgressFile(dir string) *ProgressFile {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	return &ProgressFile{path: filepath.Join(dir, "PROGRESS.md")}
}

// ProgressEntry is one item in the progress file.
type ProgressEntry struct {
	Status    string // "done", "in_progress", "blocked"
	Task      string
	Detail    string // optional extra info
	Commit    string // optional git commit hash
	StartedAt time.Time
	UpdatedAt time.Time
}

// ProgressState is the full state captured in a PROGRESS.md file.
type ProgressState struct {
	Mission     string
	Completed   []ProgressEntry
	InProgress  []ProgressEntry
	Blocked     []ProgressEntry
	NextActions []string
	UpdatedAt   time.Time
}

// Write persists a ProgressState to the PROGRESS.md file. It uses atomic
// write (tmp + rename) to avoid corruption on crash.
func (pf *ProgressFile) Write(state ProgressState) error {
	if pf == nil {
		return nil
	}
	state.UpdatedAt = time.Now().UTC()

	var b strings.Builder
	b.WriteString("# Session Progress\n\n")

	b.WriteString("## Current Mission\n")
	if state.Mission != "" {
		b.WriteString(state.Mission + "\n\n")
	} else {
		b.WriteString("(no mission set)\n\n")
	}

	if len(state.Completed) > 0 {
		b.WriteString("## Completed\n")
		for _, e := range state.Completed {
			line := "- [x] " + e.Task
			if e.Detail != "" {
				line += " — " + e.Detail
			}
			if e.Commit != "" {
				line += " (committed: " + e.Commit + ")"
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(state.InProgress) > 0 {
		b.WriteString("## In Progress\n")
		for _, e := range state.InProgress {
			line := "- [ ] " + e.Task
			if e.Detail != "" {
				line += " — " + e.Detail
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(state.Blocked) > 0 {
		b.WriteString("## Blocked\n")
		for _, e := range state.Blocked {
			line := "- " + e.Task
			if e.Detail != "" {
				line += " — " + e.Detail
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(state.NextActions) > 0 {
		b.WriteString("## Next Actions\n")
		for i, a := range state.NextActions {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, a))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n")
	b.WriteString("Last updated: " + state.UpdatedAt.Format(time.RFC3339) + "\n")

	if err := os.MkdirAll(filepath.Dir(pf.path), 0o755); err != nil {
		return fmt.Errorf("create progress dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(pf.path), ".progress.*.tmp")
	if err != nil {
		return fmt.Errorf("create progress tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write progress: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return fileutil.ReplaceFile(tmpPath, pf.path)
}

// Read parses the PROGRESS.md file and returns a ProgressState. Returns an
// empty state (no error) if the file doesn't exist.
func (pf *ProgressFile) Read() (ProgressState, error) {
	if pf == nil {
		return ProgressState{}, nil
	}
	data, err := os.ReadFile(pf.path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProgressState{}, nil
		}
		return ProgressState{}, fmt.Errorf("read progress: %w", err)
	}
	return parseProgress(string(data)), nil
}

// ReadAsString returns the raw PROGRESS.md content. Returns empty string if
// the file doesn't exist. This is used for injection into system prompts.
func (pf *ProgressFile) ReadAsString() string {
	if pf == nil {
		return ""
	}
	data, err := os.ReadFile(pf.path)
	if err != nil {
		return ""
	}
	return string(data)
}

// Exists returns true if the progress file exists on disk.
func (pf *ProgressFile) Exists() bool {
	if pf == nil {
		return false
	}
	_, err := os.Stat(pf.path)
	return err == nil
}

// Path returns the absolute path to the progress file.
func (pf *ProgressFile) Path() string {
	if pf == nil {
		return ""
	}
	return pf.path
}

// parseEntry extracts task, detail, and commit from a task line like:
//
//	"Design auth schema — schema validated (committed: abc123)"
//	→ task="Design auth schema", detail="schema validated", commit="abc123"
func parseEntry(raw string) (task, detail, commit string) {
	// Extract commit: look for "(committed: ...)" at the end
	rest := raw
	if idx := strings.LastIndex(rest, "(committed: "); idx >= 0 {
		commit = strings.TrimSuffix(rest[idx+len("(committed: "):], ")")
		rest = strings.TrimSpace(rest[:idx])
	}

	// Extract detail: split on " — "
	if idx := strings.Index(rest, " — "); idx >= 0 {
		task = strings.TrimSpace(rest[:idx])
		detail = strings.TrimSpace(rest[idx+3:])
	} else {
		task = rest
	}
	return
}

// parseProgress is a simple markdown parser for PROGRESS.md. It handles the
// exact format written by Write and is intentionally lenient.
func parseProgress(content string) ProgressState {
	var state ProgressState
	var currentSection string

	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Section headers
		if strings.HasPrefix(trimmed, "## ") {
			currentSection = strings.TrimPrefix(trimmed, "## ")
			continue
		}

		// Parse timestamp
		if strings.HasPrefix(trimmed, "Last updated: ") {
			ts := strings.TrimPrefix(trimmed, "Last updated: ")
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				state.UpdatedAt = t
			}
			continue
		}

		// Skip separators
		if strings.HasPrefix(trimmed, "---") {
			continue
		}

		// Mission line
		if currentSection == "Current Mission" && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if state.Mission == "" && trimmed != "(no mission set)" {
				state.Mission = trimmed
			}
			continue
		}

		// Task entries
		if strings.HasPrefix(trimmed, "- [x] ") || strings.HasPrefix(trimmed, "- [X] ") {
			raw := strings.TrimPrefix(trimmed, "- [x] ")
			raw = strings.TrimPrefix(raw, "- [X] ")
			task, detail, commit := parseEntry(raw)
			state.Completed = append(state.Completed, ProgressEntry{
				Status: "done", Task: task, Detail: detail, Commit: commit,
			})
			continue
		}
		if strings.HasPrefix(trimmed, "- [ ] ") {
			raw := strings.TrimPrefix(trimmed, "- [ ] ")
			task, detail, _ := parseEntry(raw)
			state.InProgress = append(state.InProgress, ProgressEntry{
				Status: "in_progress", Task: task, Detail: detail,
			})
			continue
		}
		if strings.HasPrefix(trimmed, "- ") && currentSection == "Blocked" {
			raw := strings.TrimPrefix(trimmed, "- ")
			task, detail, _ := parseEntry(raw)
			state.Blocked = append(state.Blocked, ProgressEntry{
				Status: "blocked", Task: task, Detail: detail,
			})
			continue
		}

		// Numbered next actions
		if currentSection == "Next Actions" {
			for i := 1; i <= 20; i++ {
				prefix := fmt.Sprintf("%d. ", i)
				if strings.HasPrefix(trimmed, prefix) {
					state.NextActions = append(state.NextActions, strings.TrimPrefix(trimmed, prefix))
					break
				}
			}
		}
	}

	return state
}
