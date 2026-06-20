package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCycleStatusTransitions(t *testing.T) {
	cycle := NewCycle("T1", "mission-1")

	if cycle.Status != CyclePending {
		t.Errorf("initial status = %q, want pending", cycle.Status)
	}
	if cycle.IsTerminal() {
		t.Error("pending should not be terminal")
	}

	cycle.MarkRunning()
	if cycle.Status != CycleRunning {
		t.Errorf("after MarkRunning: %q, want running", cycle.Status)
	}

	cycle.MarkVerifying()
	if cycle.Status != CycleVerifying {
		t.Errorf("after MarkVerifying: %q, want verifying", cycle.Status)
	}

	cycle.MarkCommitting()
	if cycle.Status != CycleCommitting {
		t.Errorf("after MarkCommitting: %q, want committing", cycle.Status)
	}

	cycle.MarkDone()
	if cycle.Status != CycleDone {
		t.Errorf("after MarkDone: %q, want done", cycle.Status)
	}
	if !cycle.IsTerminal() {
		t.Error("done should be terminal")
	}
	if cycle.EndedAt == nil {
		t.Error("ended_at should be set")
	}
}

func TestCycleMarkFailed(t *testing.T) {
	cycle := NewCycle("T1", "mission-1")
	cycle.MarkRunning()

	err := &testError{msg: "something went wrong"}
	cycle.MarkFailed(err)

	if cycle.Status != CycleFailed {
		t.Errorf("status = %q, want failed", cycle.Status)
	}
	if cycle.Error != "something went wrong" {
		t.Errorf("error = %q, want 'something went wrong'", cycle.Error)
	}
	if !cycle.IsTerminal() {
		t.Error("failed should be terminal")
	}
}

func TestCycleMarkAborted(t *testing.T) {
	cycle := NewCycle("T1", "mission-1")
	cycle.MarkAborted()

	if cycle.Status != CycleAborted {
		t.Errorf("status = %q, want aborted", cycle.Status)
	}
	if !cycle.IsTerminal() {
		t.Error("aborted should be terminal")
	}
}

func TestCycleDuration(t *testing.T) {
	cycle := NewCycle("T1", "mission-1")
	cycle.StartedAt = time.Now().Add(-time.Second)

	d := cycle.Duration()
	if d < time.Second {
		t.Errorf("duration = %v, want >= 1s", d)
	}

	now := time.Now().UTC()
	cycle.EndedAt = &now
	d = cycle.Duration()
	if d < time.Second {
		t.Errorf("ended duration = %v, want >= 1s", d)
	}
}

func TestCheckpointStoreSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	cp := Checkpoint{
		ID:      "cp-1",
		CycleID: "cycle-1",
		TaskID:  "T1",
		Status:  "running",
	}

	if err := store.Save(cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("cycle-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.TaskID != "T1" {
		t.Errorf("task_id = %q, want T1", loaded.TaskID)
	}
	if loaded.Status != "running" {
		t.Errorf("status = %q, want running", loaded.Status)
	}
}

func TestCheckpointStoreLatest(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	// Save two checkpoints
	store.Save(Checkpoint{ID: "cp-1", CycleID: "cycle-1", TaskID: "T1", Status: "running"})
	store.Save(Checkpoint{ID: "cp-2", CycleID: "cycle-2", TaskID: "T2", Status: "done"})

	latest, err := store.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.CycleID != "cycle-2" {
		t.Errorf("latest cycle = %q, want cycle-2", latest.CycleID)
	}
}

func TestCheckpointStoreLatestEmpty(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	latest, err := store.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.ID != "" {
		t.Errorf("expected empty checkpoint, got %v", latest)
	}
}

func TestCheckpointStoreIncomplete(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	store.Save(Checkpoint{ID: "cp-1", CycleID: "cycle-1", TaskID: "T1", Status: "running"})
	store.Save(Checkpoint{ID: "cp-2", CycleID: "cycle-2", TaskID: "T2", Status: "done"})
	store.Save(Checkpoint{ID: "cp-3", CycleID: "cycle-3", TaskID: "T3", Status: "failed"})
	store.Save(Checkpoint{ID: "cp-4", CycleID: "cycle-4", TaskID: "T4", Status: "aborted"})

	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete: %v", err)
	}
	if len(incomplete) != 2 {
		t.Errorf("incomplete count = %d, want 2", len(incomplete))
	}
}

func TestCheckpointStoreDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	store.Save(Checkpoint{ID: "cp-1", CycleID: "cycle-1", TaskID: "T1", Status: "running"})

	if err := store.Delete("cycle-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	loaded, err := store.Load("cycle-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != "" {
		t.Error("checkpoint should be deleted")
	}
}

func TestCheckpointStoreNonExistentDir(t *testing.T) {
	store := NewCheckpointStore("/nonexistent/path")

	latest, err := store.Latest()
	if err != nil {
		t.Fatalf("Latest on nonexistent: %v", err)
	}
	if latest.ID != "" {
		t.Error("expected empty checkpoint")
	}

	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete on nonexistent: %v", err)
	}
	if len(incomplete) != 0 {
		t.Error("expected no incomplete checkpoints")
	}
}

func TestCheckpointSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	store.Save(Checkpoint{ID: "cp-1", CycleID: "cycle-1", TaskID: "T1", Status: "running"})

	// Verify no temp files remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name()[0] == '.' {
			t.Errorf("temp file should be cleaned up: %s", e.Name())
		}
	}
}

func TestCheckpointMultipleTasks(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	store.Save(Checkpoint{ID: "cp-1", CycleID: "cycle-T1", TaskID: "T1", Status: "done"})
	store.Save(Checkpoint{ID: "cp-2", CycleID: "cycle-T2", TaskID: "T2", Status: "running"})
	store.Save(Checkpoint{ID: "cp-3", CycleID: "cycle-T3", TaskID: "T3", Status: "pending"})

	// Load specific task
	cp, err := store.Load("cycle-T2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cp.TaskID != "T2" {
		t.Errorf("task_id = %q, want T2", cp.TaskID)
	}
}

func TestCheckpointStoreConcurrent(t *testing.T) {
	dir := t.TempDir()
	store := NewCheckpointStore(dir)

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func(idx int) {
			for j := 0; j < 10; j++ {
				store.Save(Checkpoint{
					ID:      filepath.Base(dir) + "-" + string(rune('A'+idx)),
					CycleID: "cycle-" + string(rune('A'+idx)) + "-" + string(rune('0'+j)),
					TaskID:  "T" + string(rune('A'+idx)),
					Status:  "running",
				})
			}
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string { return e.msg }
