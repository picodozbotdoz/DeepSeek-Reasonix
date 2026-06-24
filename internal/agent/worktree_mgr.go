package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// MergeStrategy controls what happens when a merge has conflicts.
type MergeStrategy string

const (
	MergeStrategyAbort   MergeStrategy = "abort"   // abort merge, leave worktree intact
	MergeStrategyEscalate MergeStrategy = "escalate" // pause and notify manager
)

// WorktreeInfo describes a managed worktree.
type WorktreeInfo struct {
	TaskID   string
	Path     string
	Branch   string
	Active   bool
	Error    string
}

// WorktreeManager creates, manages, and merges git worktrees for mission tasks.
// Each task gets its own worktree on a feature branch; on completion the branch
// is merged back into the main branch in dependency order.
type WorktreeManager struct {
	mu       sync.Mutex
	workDir  string                // repo root (CWD for git commands)
	mainBranch string              // branch to merge into (default: current)
	worktrees map[string]*WorktreeInfo // taskID → worktree info
}

// NewWorktreeManager creates a manager rooted at workDir. If mainBranch is
// empty, the current branch at creation time is used.
func NewWorktreeManager(workDir, mainBranch string) *WorktreeManager {
	return &WorktreeManager{
		workDir:   workDir,
		mainBranch: mainBranch,
		worktrees:  make(map[string]*WorktreeInfo),
	}
}

// EnsureMainBranch detects the current branch if mainBranch was not set.
func (wm *WorktreeManager) EnsureMainBranch() error {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	if wm.mainBranch != "" {
		return nil
	}
	out, err := wm.git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return fmt.Errorf("detect current branch: %w", err)
	}
	wm.mainBranch = strings.TrimSpace(string(out))
	return nil
}

// MainBranch returns the resolved main branch name.
func (wm *WorktreeManager) MainBranch() string {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	return wm.mainBranch
}

// CreateWorktree creates a new git worktree for the given task. The branch
// name is derived from the task ID (e.g. "T1" → "feature/lrswe-T1").
func (wm *WorktreeManager) CreateWorktree(taskID string) (*WorktreeInfo, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.worktrees[taskID]; exists {
		return nil, fmt.Errorf("worktree for task %s already exists", taskID)
	}

	branch := "feature/lrswe-" + taskID
	worktreePath := filepath.Join(wm.workDir, ".worktrees", taskID)

	// Create parent dir
	if err := os.MkdirAll(filepath.Dir(worktreePath), 0o755); err != nil {
		return nil, fmt.Errorf("create worktree parent: %w", err)
	}

	// git worktree add <path> -b <branch> <start_point>
	if _, err := wm.git("worktree", "add", worktreePath, "-b", branch, wm.mainBranch); err != nil {
		return nil, fmt.Errorf("create worktree %s: %w", taskID, err)
	}

	info := &WorktreeInfo{
		TaskID: taskID,
		Path:   worktreePath,
		Branch: branch,
		Active: true,
	}
	wm.worktrees[taskID] = info
	return info, nil
}

// GetWorktree returns the worktree info for a task, or nil if not found.
func (wm *WorktreeManager) GetWorktree(taskID string) *WorktreeInfo {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	return wm.worktrees[taskID]
}

// ListWorktrees returns all managed worktrees.
func (wm *WorktreeManager) ListWorktrees() []*WorktreeInfo {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	var out []*WorktreeInfo
	for _, wt := range wm.worktrees {
		out = append(out, wt)
	}
	return out
}

// CommitInWorktree stages all changes and commits with the given message
// inside the specified worktree.
func (wm *WorktreeManager) CommitInWorktree(taskID, message string) error {
	wm.mu.Lock()
	wt, ok := wm.worktrees[taskID]
	wm.mu.Unlock()
	if !ok {
		return fmt.Errorf("worktree for task %s not found", taskID)
	}

	if _, err := wm.gitIn(wt.Path, "add", "-A"); err != nil {
		return fmt.Errorf("stage changes: %w", err)
	}

	// Check if there's anything to commit (diff --quiet returns 0 if no diff)
	if _, err := wm.gitIn(wt.Path, "diff", "--cached", "--quiet"); err == nil {
		return nil // nothing to commit
	}

	if _, err := wm.gitIn(wt.Path, "commit", "-m", message); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// MergeResult is the outcome of a merge attempt.
type MergeResult struct {
	TaskID  string
	Success bool
	Commit  string // merge commit hash on success
	Error   string // error message on failure
	Conflicts []string // conflicting files
}

// MergeWorktree merges a task's feature branch into the main branch. If there
// are conflicts, the strategy determines the outcome.
func (wm *WorktreeManager) MergeWorktree(taskID string, strategy MergeStrategy) (*MergeResult, error) {
	wm.mu.Lock()
	wt, ok := wm.worktrees[taskID]
	wm.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("worktree for task %s not found", taskID)
	}

	// Checkout main branch
	if _, err := wm.git("checkout", wm.mainBranch); err != nil {
		return &MergeResult{TaskID: taskID, Error: fmt.Sprintf("checkout main: %v", err)}, nil
	}

	// Attempt merge
	out, err := wm.git("merge", wt.Branch, "--no-edit")
	if err == nil {
		// Success
		commit, _ := wm.git("rev-parse", "HEAD")
		return &MergeResult{
			TaskID:  taskID,
			Success: true,
			Commit:  strings.TrimSpace(string(commit)),
		}, nil
	}

	// Conflict — get conflicting files
	conflicts := wm.conflictingFiles()

	switch strategy {
	case MergeStrategyAbort:
		wm.git("merge", "--abort")
		return &MergeResult{
			TaskID:    taskID,
			Success:   false,
			Error:     strings.TrimSpace(string(out)),
			Conflicts: conflicts,
		}, nil

	case MergeStrategyEscalate:
		// Leave the merge in progress for manual resolution
		return &MergeResult{
			TaskID:    taskID,
			Success:   false,
			Error:     strings.TrimSpace(string(out)),
			Conflicts: conflicts,
		}, nil

	default:
		wm.git("merge", "--abort")
		return &MergeResult{
			TaskID:  taskID,
			Success: false,
			Error:   fmt.Sprintf("unknown strategy %q", strategy),
		}, nil
	}
}

// MergeInDependencyOrder merges completed tasks in dependency order. tasks
// must be ordered with dependencies before dependents. Returns results for
// each merge attempt.
func (wm *WorktreeManager) MergeInDependencyOrder(tasks []MissionTask, strategy MergeStrategy) []*MergeResult {
	var results []*MergeResult
	for _, task := range tasks {
		if task.Status != TaskDone {
			continue
		}
		wt := wm.GetWorktree(task.ID)
		if wt == nil {
			continue
		}
		result, err := wm.MergeWorktree(task.ID, strategy)
		if err != nil {
			result = &MergeResult{TaskID: task.ID, Error: err.Error()}
		}
		results = append(results, result)
		if !result.Success {
			break // stop on first conflict
		}
	}
	return results
}

// DeleteWorktree removes a worktree and its branch.
func (wm *WorktreeManager) DeleteWorktree(taskID string) error {
	wm.mu.Lock()
	wt, ok := wm.worktrees[taskID]
	wm.mu.Unlock()
	if !ok {
		return nil // already gone
	}

	// Remove worktree
	wm.git("worktree", "remove", wt.Path, "--force")
	// Delete branch (may fail if already merged — that's fine)
	wm.git("branch", "-D", wt.Branch)

	wm.mu.Lock()
	delete(wm.worktrees, taskID)
	wm.mu.Unlock()
	return nil
}

// Cleanup removes all managed worktrees.
func (wm *WorktreeManager) Cleanup() {
	wm.mu.Lock()
	ids := make([]string, 0, len(wm.worktrees))
	for id := range wm.worktrees {
		ids = append(ids, id)
	}
	wm.mu.Unlock()

	for _, id := range ids {
		wm.DeleteWorktree(id)
	}
}

// git runs a git command in the workDir root.
func (wm *WorktreeManager) git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = wm.workDir
	return cmd.CombinedOutput()
}

// gitIn runs a git command in the specified directory.
func (wm *WorktreeManager) gitIn(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

// conflictingFiles returns the list of files with merge conflicts.
func (wm *WorktreeManager) conflictingFiles() []string {
	out, err := wm.git("diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var files []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			files = append(files, l)
		}
	}
	return files
}

// VerifyMerge checks that the working tree is clean and tests pass after merge.
func (wm *WorktreeManager) VerifyMerge(testCmd string) (bool, string, error) {
	// Check clean working tree
	out, err := wm.git("status", "--porcelain")
	if err != nil {
		return false, "", fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return false, "working tree not clean after merge", nil
	}

	// Run verification command if provided
	if testCmd != "" {
		parts := strings.Fields(testCmd)
		cmd := exec.Command(parts[0], parts[1:]...)
		cmd.Dir = wm.workDir
		output, err := cmd.CombinedOutput()
		if err != nil {
			return false, fmt.Sprintf("verification failed: %s\n%s", err, string(output)), nil
		}
	}

	return true, "", nil
}
