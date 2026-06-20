package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a bare git repo with an initial commit for testing.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Init repo with explicit branch name
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.email", "test@test.com")
	runGit(t, dir, "config", "user.name", "Test")

	// Initial commit
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestWorktreeManagerCreateAndList(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "")

	if err := wm.EnsureMainBranch(); err != nil {
		t.Fatalf("EnsureMainBranch: %v", err)
	}
	if wm.MainBranch() != "main" && wm.MainBranch() != "master" {
		t.Errorf("main branch = %q, want main or master", wm.MainBranch())
	}

	wt, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}
	if wt.TaskID != "T1" {
		t.Errorf("taskID = %q, want T1", wt.TaskID)
	}
	if wt.Branch != "feature/lrswe-T1" {
		t.Errorf("branch = %q, want feature/lrswe-T1", wt.Branch)
	}
	if !wt.Active {
		t.Error("worktree should be active")
	}

	list := wm.ListWorktrees()
	if len(list) != 1 {
		t.Fatalf("list count = %d, want 1", len(list))
	}
}

func TestWorktreeManagerDuplicate(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	_, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	_, err = wm.CreateWorktree("T1")
	if err == nil {
		t.Error("expected error for duplicate worktree")
	}
}

func TestWorktreeManagerCommitInWorktree(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	wt, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Write a file in the worktree
	if err := os.WriteFile(filepath.Join(wt.Path, "newfile.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := wm.CommitInWorktree("T1", "add newfile"); err != nil {
		t.Fatalf("CommitInWorktree: %v", err)
	}

	// Verify commit exists
	out, err := wm.gitIn(wt.Path, "log", "--oneline", "-1")
	if err != nil {
		t.Fatal(err)
	}
	if !containsStr(string(out), "add newfile") {
		t.Errorf("commit message not found: %s", out)
	}
}

func TestWorktreeManagerCommitNothingToCommit(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	_, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Commit with no changes should not error
	if err := wm.CommitInWorktree("T1", "nothing"); err != nil {
		t.Fatalf("CommitInWorktree with no changes: %v", err)
	}
}

func TestWorktreeManagerDelete(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	_, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	if err := wm.DeleteWorktree("T1"); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}

	wt := wm.GetWorktree("T1")
	if wt != nil {
		t.Error("worktree should be nil after delete")
	}

	if len(wm.ListWorktrees()) != 0 {
		t.Error("worktree list should be empty after delete")
	}
}

func TestWorktreeManagerMergeSuccess(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	wt, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	// Make a change in the worktree
	if err := os.WriteFile(filepath.Join(wt.Path, "feature.txt"), []byte("new feature"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wm.CommitInWorktree("T1", "add feature"); err != nil {
		t.Fatalf("CommitInWorktree: %v", err)
	}

	// Switch back to main
	runGit(t, dir, "checkout", "main")

	// Merge
	result, err := wm.MergeWorktree("T1", MergeStrategyAbort)
	if err != nil {
		t.Fatalf("MergeWorktree: %v", err)
	}
	if !result.Success {
		t.Errorf("merge should succeed: %s", result.Error)
	}
	if result.Commit == "" {
		t.Error("merge commit hash should not be empty")
	}

	// Verify file exists on main
	if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err != nil {
		t.Error("feature.txt should exist on main after merge")
	}
}

func TestWorktreeManagerMergeConflictAbort(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	// Create two worktrees from the same starting point
	wt1, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree T1: %v", err)
	}
	wt2, err := wm.CreateWorktree("T2")
	if err != nil {
		t.Fatalf("CreateWorktree T2: %v", err)
	}

	// Both modify the same file
	os.WriteFile(filepath.Join(wt1.Path, "shared.txt"), []byte("from T1"), 0o644)
	wm.CommitInWorktree("T1", "T1 change")

	os.WriteFile(filepath.Join(wt2.Path, "shared.txt"), []byte("from T2"), 0o644)
	wm.CommitInWorktree("T2", "T2 change")

	// Merge T1 first (should succeed)
	runGit(t, dir, "checkout", "main")
	r1, _ := wm.MergeWorktree("T1", MergeStrategyAbort)
	if !r1.Success {
		t.Fatalf("first merge should succeed: %s", r1.Error)
	}

	// Merge T2 (should conflict)
	r2, _ := wm.MergeWorktree("T2", MergeStrategyAbort)
	if r2.Success {
		t.Error("second merge should fail with conflict")
	}
	if len(r2.Conflicts) == 0 {
		t.Error("should report conflicting files")
	}
}

func TestWorktreeManagerMergeInDependencyOrder(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	// Create two independent worktrees
	wt1, err := wm.CreateWorktree("T1")
	if err != nil {
		t.Fatalf("CreateWorktree T1: %v", err)
	}
	wt2, err := wm.CreateWorktree("T2")
	if err != nil {
		t.Fatalf("CreateWorktree T2: %v", err)
	}

	os.WriteFile(filepath.Join(wt1.Path, "t1.txt"), []byte("T1"), 0o644)
	wm.CommitInWorktree("T1", "T1 work")

	os.WriteFile(filepath.Join(wt2.Path, "t2.txt"), []byte("T2"), 0o644)
	wm.CommitInWorktree("T2", "T2 work")

	runGit(t, dir, "checkout", "main")

	tasks := []MissionTask{
		{ID: "T1", Status: TaskDone},
		{ID: "T2", Status: TaskDone},
	}

	results := wm.MergeInDependencyOrder(tasks, MergeStrategyAbort)
	if len(results) != 2 {
		t.Fatalf("results count = %d, want 2", len(results))
	}
	for _, r := range results {
		if !r.Success {
			t.Errorf("task %s merge should succeed: %s", r.TaskID, r.Error)
		}
	}
}

func TestWorktreeManagerCleanup(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	wm.CreateWorktree("T1")
	wm.CreateWorktree("T2")

	wm.Cleanup()

	if len(wm.ListWorktrees()) != 0 {
		t.Error("worktrees should be empty after cleanup")
	}
}

func TestWorktreeManagerGetWorktreeNotFound(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	wt := wm.GetWorktree("NONEXISTENT")
	if wt != nil {
		t.Error("should return nil for non-existent worktree")
	}
}

func TestWorktreeManagerDeleteNonExistent(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	// Should not error
	if err := wm.DeleteWorktree("NONEXISTENT"); err != nil {
		t.Fatalf("DeleteWorktree on non-existent: %v", err)
	}
}

func TestWorktreeManagerCommitNonExistent(t *testing.T) {
	dir := initTestRepo(t)
	wm := NewWorktreeManager(dir, "main")

	err := wm.CommitInWorktree("NONEXISTENT", "msg")
	if err == nil {
		t.Error("expected error for non-existent worktree")
	}
}

func TestMergeStrategyValues(t *testing.T) {
	if MergeStrategyAbort != "abort" {
		t.Errorf("MergeStrategyAbort = %q, want abort", MergeStrategyAbort)
	}
	if MergeStrategyEscalate != "escalate" {
		t.Errorf("MergeStrategyEscalate = %q, want escalate", MergeStrategyEscalate)
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
