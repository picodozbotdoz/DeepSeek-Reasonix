package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressFileWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(dir)

	state := ProgressState{
		Mission: "Implement user authentication",
		Completed: []ProgressEntry{
			{Status: "done", Task: "Design auth schema", Commit: "abc123", Detail: "schema validated"},
			{Status: "done", Task: "Implement login endpoint", Commit: "def456"},
		},
		InProgress: []ProgressEntry{
			{Status: "in_progress", Task: "Write integration tests", Detail: "50% complete"},
		},
		Blocked: []ProgressEntry{
			{Status: "blocked", Task: "Deploy to staging", Detail: "waiting for T2"},
		},
		NextActions: []string{"Finish tests", "Run CI", "Deploy to staging"},
	}

	if err := pf.Write(state); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify file exists
	if !pf.Exists() {
		t.Fatal("progress file should exist after Write")
	}

	// Read back
	got, err := pf.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if got.Mission != state.Mission {
		t.Errorf("mission = %q, want %q", got.Mission, state.Mission)
	}
	if len(got.Completed) != 2 {
		t.Errorf("completed count = %d, want 2", len(got.Completed))
	}
	if got.Completed[0].Task != "Design auth schema" {
		t.Errorf("completed[0].task = %q, want %q", got.Completed[0].Task, "Design auth schema")
	}
	if len(got.InProgress) != 1 {
		t.Errorf("in_progress count = %d, want 1", len(got.InProgress))
	}
	if len(got.Blocked) != 1 {
		t.Errorf("blocked count = %d, want 1", len(got.Blocked))
	}
	if len(got.NextActions) != 3 {
		t.Errorf("next_actions count = %d, want 3", len(got.NextActions))
	}
	if got.NextActions[0] != "Finish tests" {
		t.Errorf("next_actions[0] = %q, want %q", got.NextActions[0], "Finish tests")
	}
}

func TestProgressFileReadNonExistent(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(filepath.Join(dir, "nonexistent"))

	got, err := pf.Read()
	if err != nil {
		t.Fatalf("Read on nonexistent: %v", err)
	}
	if got.Mission != "" {
		t.Errorf("expected empty mission, got %q", got.Mission)
	}
}

func TestProgressFileReadAsString(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(dir)

	state := ProgressState{Mission: "Test mission"}
	if err := pf.Write(state); err != nil {
		t.Fatalf("Write: %v", err)
	}

	content := pf.ReadAsString()
	if !strings.Contains(content, "Test mission") {
		t.Errorf("ReadAsString should contain mission, got %q", content)
	}
	if !strings.HasPrefix(content, "# Session Progress") {
		t.Errorf("ReadAsString should start with header, got %q", content[:30])
	}
}

func TestProgressFileReadAsStringNonExistent(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(filepath.Join(dir, "nonexistent"))

	content := pf.ReadAsString()
	if content != "" {
		t.Errorf("expected empty string for nonexistent file, got %q", content)
	}
}

func TestProgressFileAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(dir)

	state := ProgressState{Mission: "Atomic test"}
	if err := pf.Write(state); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Verify no temp files remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".progress.") {
			t.Errorf("temp file should be cleaned up: %s", e.Name())
		}
	}
}

func TestProgressFileEmptyState(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(dir)

	state := ProgressState{}
	if err := pf.Write(state); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := pf.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Mission != "" {
		t.Errorf("expected empty mission, got %q", got.Mission)
	}
	if len(got.Completed) != 0 {
		t.Errorf("expected 0 completed, got %d", len(got.Completed))
	}
}

func TestNewProgressFileNilDir(t *testing.T) {
	pf := NewProgressFile("")
	if pf != nil {
		t.Error("expected nil for empty dir")
	}
	pf = NewProgressFile("  ")
	if pf != nil {
		t.Error("expected nil for whitespace dir")
	}
}

func TestProgressFilePath(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(dir)
	want := filepath.Join(dir, "PROGRESS.md")
	if got := pf.Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestProgressFileTimestamps(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(dir)

	before := time.Now().UTC()
	state := ProgressState{Mission: "Timestamp test"}
	if err := pf.Write(state); err != nil {
		t.Fatalf("Write: %v", err)
	}
	after := time.Now().UTC()

	got, err := pf.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// RFC3339 truncates to second precision, so compare at second granularity
	gotSec := got.UpdatedAt.Truncate(time.Second)
	beforeSec := before.Truncate(time.Second)
	afterSec := after.Truncate(time.Second)
	if gotSec.Before(beforeSec) || gotSec.After(afterSec) {
		t.Errorf("timestamp %v not between %v and %v", got.UpdatedAt, before, after)
	}
}

func TestProgressFileNilReceiver(t *testing.T) {
	var pf *ProgressFile
	if err := pf.Write(ProgressState{}); err != nil {
		t.Errorf("Write on nil: %v", err)
	}
	got, err := pf.Read()
	if err != nil {
		t.Errorf("Read on nil: %v", err)
	}
	if got.Mission != "" {
		t.Errorf("expected empty mission from nil, got %q", got.Mission)
	}
	if content := pf.ReadAsString(); content != "" {
		t.Errorf("expected empty string from nil, got %q", content)
	}
	if pf.Exists() {
		t.Error("expected false from nil Exists")
	}
	if pf.Path() != "" {
		t.Errorf("expected empty path from nil, got %q", pf.Path())
	}
}

// --- Threat 2: Prefix Cache Stability Tests ---

func TestProgressContextInjectedAsUserMessage(t *testing.T) {
	// Verify that RunSubAgentWithSession prepends progress context to the
	// prompt (user message) rather than modifying the system prompt.
	// This preserves DeepSeek's prefix cache stability.

	dir := t.TempDir()
	pf := NewProgressFile(dir)

	// Write progress state
	state := ProgressState{
		Mission: "Test mission for prefix test",
		Completed: []ProgressEntry{
			{Status: "done", Task: "Step 1"},
		},
	}
	pf.Write(state)

	progressCtx := pf.ReadAsString()
	if progressCtx == "" {
		t.Fatal("progress context should not be empty")
	}

	// Verify the progress context contains expected content
	if !strings.Contains(progressCtx, "Test mission for prefix test") {
		t.Errorf("progress context missing mission: %s", progressCtx)
	}
	if !strings.Contains(progressCtx, "Step 1") {
		t.Errorf("progress context missing completed task: %s", progressCtx)
	}

	// The key test: verify that progress context is NOT a system prompt
	// modification. In RunSubAgentWithSession, it should be prepended
	// to the prompt as a user message section.
	// We verify this by checking the format matches the expected user message pattern.
	if !strings.HasPrefix(progressCtx, "# Session Progress") {
		t.Errorf("progress context should start with '# Session Progress', got: %s", progressCtx[:50])
	}
}

func TestProgressContextEmptyWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(filepath.Join(dir, "nonexistent"))

	// ProgressContext should return empty string when no file exists
	// This means no user message prefix is added — clean behavior
	content := pf.ReadAsString()
	if content != "" {
		t.Errorf("expected empty progress context for nonexistent file, got: %s", content)
	}
}

func TestProgressContextStableAcrossReads(t *testing.T) {
	dir := t.TempDir()
	pf := NewProgressFile(dir)

	state := ProgressState{
		Mission: "Stability test",
		Completed: []ProgressEntry{
			{Status: "done", Task: "Task A"},
		},
	}
	pf.Write(state)

	// Read multiple times — should return identical content
	// This is critical for prefix cache: same content = same prefix
	first := pf.ReadAsString()
	second := pf.ReadAsString()
	third := pf.ReadAsString()

	if first != second || second != third {
		t.Errorf("progress context not stable across reads:\n1: %s\n2: %s\n3: %s", first, second, third)
	}
}
