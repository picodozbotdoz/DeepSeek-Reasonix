package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

// TestLRSWEEndToEnd exercises all 6 phases together in a realistic workflow:
// 1. Create shared directory and progress file
// 2. Create a mission with dependent tasks
// 3. Set up budget tracker
// 4. Create worktrees for tasks
// 5. Execute cycles with checkpoints
// 6. Detect patterns from tool call records
// 7. Generate skills from patterns
func TestLRSWEEndToEnd(t *testing.T) {
	// Setup: create project structure
	projectDir := t.TempDir()
	sharedDir := filepath.Join(projectDir, "_shared")
	os.MkdirAll(sharedDir, 0o755)

	// ═══════════════════════════════════════════════════════════════
	// Phase 1: Progress File
	// ═══════════════════════════════════════════════════════════════
	t.Log("Phase 1: Progress File")

	pf := NewProgressFile(sharedDir)
	if pf == nil {
		t.Fatal("ProgressFile should not be nil")
	}

	// Write initial progress
	state := ProgressState{
		Mission: "Implement user authentication",
		NextActions: []string{
			"Design auth schema",
			"Implement login endpoint",
		},
	}
	if err := pf.Write(state); err != nil {
		t.Fatalf("Write progress: %v", err)
	}

	// Verify progress file exists and is readable
	if !pf.Exists() {
		t.Fatal("progress file should exist")
	}
	content := pf.ReadAsString()
	if !strings.Contains(content, "Implement user authentication") {
		t.Errorf("progress should contain mission name: %s", content)
	}

	// Read back and verify
	loaded, err := pf.Read()
	if err != nil {
		t.Fatalf("Read progress: %v", err)
	}
	if loaded.Mission != "Implement user authentication" {
		t.Errorf("mission = %q, want 'Implement user authentication'", loaded.Mission)
	}
	t.Logf("Phase 1 OK: progress file created at %s", pf.Path())

	// ═══════════════════════════════════════════════════════════════
	// Phase 2: Mission Persistence
	// ═══════════════════════════════════════════════════════════════
	t.Log("Phase 2: Mission Persistence")

	missionPath := filepath.Join(sharedDir, "MISSION.toml")
	mm := NewMissionManager(missionPath)

	mission, err := mm.CreateMission("Implement user authentication", []MissionTask{
		{ID: "T1", Title: "Design auth schema", DoneWhen: "schema file exists and passes validation"},
		{ID: "T2", Title: "Implement login endpoint", DependsOn: []string{"T1"}, DoneWhen: "POST /auth/login returns 200"},
		{ID: "T3", Title: "Implement register endpoint", DependsOn: []string{"T1"}, DoneWhen: "POST /auth/register returns 201"},
		{ID: "T4", Title: "Write integration tests", DependsOn: []string{"T2", "T3"}, DoneWhen: "all tests pass"},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if mission.Name != "Implement user authentication" {
		t.Errorf("mission name = %q", mission.Name)
	}

	// Verify ready tasks (only T1 should be ready)
	ready, err := mm.ReadyTasks()
	if err != nil {
		t.Fatalf("ReadyTasks: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != "T1" {
		t.Errorf("expected only T1 ready, got %v", ready)
	}

	// Complete T1
	if err := mm.CompleteTask("T1", "abc123"); err != nil {
		t.Fatalf("CompleteTask T1: %v", err)
	}

	// Now T2 and T3 should be ready
	ready, err = mm.ReadyTasks()
	if err != nil {
		t.Fatalf("ReadyTasks: %v", err)
	}
	if len(ready) != 2 {
		t.Errorf("expected 2 ready tasks, got %d", len(ready))
	}

	// Verify mission summary
	loaded2, err := mm.Load()
	if err != nil {
		t.Fatalf("Load mission: %v", err)
	}
	summary := loaded2.Summary()
	if !strings.Contains(summary, "1 done") {
		t.Errorf("summary should show 1 done: %s", summary)
	}
	t.Logf("Phase 2 OK: mission with %d tasks, summary: %s", len(loaded2.Tasks), summary)

	// ═══════════════════════════════════════════════════════════════
	// Phase 3: Budget Enforcement
	// ═══════════════════════════════════════════════════════════════
	t.Log("Phase 3: Budget Enforcement")

	bt := NewBudgetTracker(BudgetLimits{
		MaxTokens:  10000,
		MaxCostUSD: 5.0,
		MaxTurns:   20,
	})

	// Simulate usage
	for i := 0; i < 5; i++ {
		bt.RecordUsage(&provider.Usage{
			PromptTokens:     100,
			CompletionTokens: 200,
			TotalTokens:      300,
		}, nil)
		bt.RecordTurn()
	}

	status := bt.Check()
	if status.TokensUsed != 1500 {
		t.Errorf("tokens = %d, want 1500", status.TokensUsed)
	}
	if status.TurnsUsed != 5 {
		t.Errorf("turns = %d, want 5", status.TurnsUsed)
	}
	if status.Exceeded() {
		t.Error("budget should not be exceeded yet")
	}

	// Push to limit
	for i := 0; i < 30; i++ {
		bt.RecordUsage(&provider.Usage{TotalTokens: 300}, nil)
		bt.RecordTurn()
	}

	status = bt.Check()
	if !status.Exceeded() {
		t.Error("budget should be exceeded after many turns")
	}
	t.Logf("Phase 3 OK: budget tracked %d tokens, %d turns, exceeded=%v",
		status.TokensUsed, status.TurnsUsed, status.Exceeded())

	// ═══════════════════════════════════════════════════════════════
	// Phase 4: Dependency-Ordered Merge (worktree isolation)
	// ═══════════════════════════════════════════════════════════════
	t.Log("Phase 4: Dependency-Ordered Merge")

	// Initialize a git repo for worktree testing
	repoDir := filepath.Join(projectDir, "repo")
	os.MkdirAll(repoDir, 0o755)
	e2eRunGit(t, repoDir, "init", "-b", "main")
	e2eRunGit(t, repoDir, "config", "user.email", "test@test.com")
	e2eRunGit(t, repoDir, "config", "user.name", "Test")
	os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test"), 0o644)
	e2eRunGit(t, repoDir, "add", "README.md")
	e2eRunGit(t, repoDir, "commit", "-m", "initial commit")

	wm := NewWorktreeManager(repoDir, "main")

	// Create worktrees for T2 and T3 (T1 is done)
	wt2, err := wm.CreateWorktree("T2")
	if err != nil {
		t.Fatalf("CreateWorktree T2: %v", err)
	}
	wt3, err := wm.CreateWorktree("T3")
	if err != nil {
		t.Fatalf("CreateWorktree T3: %v", err)
	}

	// Make changes in each worktree
	os.WriteFile(filepath.Join(wt2.Path, "login.go"), []byte("package auth\n// login handler"), 0o644)
	wm.CommitInWorktree("T2", "implement login")

	os.WriteFile(filepath.Join(wt3.Path, "register.go"), []byte("package auth\n// register handler"), 0o644)
	wm.CommitInWorktree("T3", "implement register")

	// Merge in dependency order (both depend on T1 which is done)
	e2eRunGit(t, repoDir, "checkout", "main")
	results := wm.MergeInDependencyOrder([]MissionTask{
		{ID: "T2", Status: TaskDone},
		{ID: "T3", Status: TaskDone},
	}, MergeStrategyAbort)

	for _, r := range results {
		if !r.Success {
			t.Errorf("merge %s failed: %s", r.TaskID, r.Error)
		}
	}

	// Verify both files exist on main
	if _, err := os.Stat(filepath.Join(repoDir, "login.go")); err != nil {
		t.Error("login.go should exist after merge")
	}
	if _, err := os.Stat(filepath.Join(repoDir, "register.go")); err != nil {
		t.Error("register.go should exist after merge")
	}
	t.Logf("Phase 4 OK: %d worktrees created, %d merged successfully",
		len(wm.ListWorktrees()), len(results))

	// ═══════════════════════════════════════════════════════════════
	// Phase 5: Durable Execution (checkpoints)
	// ═══════════════════════════════════════════════════════════════
	t.Log("Phase 5: Durable Execution")

	cpDir := filepath.Join(sharedDir, "checkpoints")
	cpStore := NewCheckpointStore(cpDir)

	// Create and save a cycle checkpoint
	cycle := NewCycle("T2", "auth-mission")
	cycle.MarkRunning()
	cp := Checkpoint{
		ID:      cycle.ID,
		CycleID: cycle.ID,
		TaskID:  cycle.TaskID,
		Status:  "running",
	}
	if err := cpStore.Save(cp); err != nil {
		t.Fatalf("Save checkpoint: %v", err)
	}

	// Simulate crash: find incomplete checkpoints
	incomplete, err := cpStore.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete: %v", err)
	}
	if len(incomplete) != 1 {
		t.Errorf("expected 1 incomplete checkpoint, got %d", len(incomplete))
	}
	if incomplete[0].TaskID != "T2" {
		t.Errorf("incomplete task = %q, want T2", incomplete[0].TaskID)
	}

	// Simulate recovery: complete the cycle
	cycle.MarkDone()
	cp2 := Checkpoint{
		ID:      cycle.ID,
		CycleID: cycle.ID,
		TaskID:  cycle.TaskID,
		Status:  "done",
	}
	cpStore.Save(cp2)

	// Verify no more incomplete checkpoints
	incomplete, _ = cpStore.Incomplete()
	if len(incomplete) != 0 {
		t.Errorf("expected 0 incomplete after recovery, got %d", len(incomplete))
	}
	t.Logf("Phase 5 OK: checkpoint saved, incomplete detected, recovery completed")

	// ═══════════════════════════════════════════════════════════════
	// Phase 6: Self-Evolving Skills (pattern detection)
	// ═══════════════════════════════════════════════════════════════
	t.Log("Phase 6: Self-Evolving Skills")

	// Simulate tool call records from multiple sessions
	records := []ToolCallRecord{
		// Session 1: read → grep → edit pattern
		{Name: "read_file", SessionID: "s1", TurnIndex: 0, Success: true, Duration: 100},
		{Name: "grep", SessionID: "s1", TurnIndex: 1, Success: true, Duration: 50},
		{Name: "edit_file", SessionID: "s1", TurnIndex: 2, Success: true, Duration: 200},
		// Session 2: same pattern
		{Name: "read_file", SessionID: "s2", TurnIndex: 0, Success: true, Duration: 120},
		{Name: "grep", SessionID: "s2", TurnIndex: 1, Success: true, Duration: 45},
		{Name: "edit_file", SessionID: "s2", TurnIndex: 2, Success: true, Duration: 180},
		// Session 3: same pattern
		{Name: "read_file", SessionID: "s3", TurnIndex: 0, Success: true, Duration: 110},
		{Name: "grep", SessionID: "s3", TurnIndex: 1, Success: true, Duration: 55},
		{Name: "edit_file", SessionID: "s3", TurnIndex: 2, Success: true, Duration: 190},
	}

	// Detect patterns
	pd := NewPatternDetector(2, 0.5, 5)
	patterns := pd.DetectPatterns(records)
	if len(patterns) == 0 {
		t.Fatal("expected at least one pattern")
	}

	// Find the read → grep → edit pattern
	var targetPattern *Pattern
	for _, p := range patterns {
		if len(p.Sequence) >= 3 &&
			p.Sequence[0] == "read_file" &&
			p.Sequence[1] == "grep" &&
			p.Sequence[2] == "edit_file" {
			targetPattern = &p
			break
		}
	}
	if targetPattern == nil {
		t.Fatal("expected to find read_file → grep → edit_file pattern")
	}

	// Generate skill from pattern
	sg := NewSkillGenerator("auto")
	skill := sg.Generate(targetPattern)
	if skill == nil {
		t.Fatal("expected non-nil skill")
	}
	if skill.Name == "" {
		t.Error("skill name should not be empty")
	}
	if skill.Body == "" {
		t.Error("skill body should not be empty")
	}

	// Verify skill has frontmatter
	frontmatter := skill.ToSkillFrontmatter()
	if !strings.Contains(frontmatter, "---") {
		t.Error("skill should have frontmatter delimiters")
	}
	if !strings.Contains(frontmatter, "auto-generated") {
		t.Error("skill frontmatter should contain source: auto-generated")
	}

	// Pattern analyzer (ephemeral session)
	analyzer := NewPatternAnalyzer(nil)
	analysis, err := analyzer.AnalyzePattern(context.Background(), targetPattern)
	if err != nil {
		t.Fatalf("AnalyzePattern: %v", err)
	}
	if analysis == "" {
		t.Error("analysis should not be empty")
	}

	analysisDisplay := analysis
	if len(analysisDisplay) > 50 {
		analysisDisplay = analysisDisplay[:50] + "..."
	}
	t.Logf("Phase 6 OK: %d patterns detected, skill '%s' generated, analysis: %s",
		len(patterns), skill.Name, analysisDisplay)

	// ═══════════════════════════════════════════════════════════════
	// Integration: Update progress and verify full cycle
	// ═══════════════════════════════════════════════════════════════
	t.Log("Integration: Full cycle verification")

	// Update progress with completion
	state.Completed = []ProgressEntry{
		{Status: "done", Task: "Design auth schema", Commit: "abc123"},
		{Status: "done", Task: "Implement login endpoint", Commit: "def456"},
		{Status: "done", Task: "Implement register endpoint", Commit: "ghi789"},
	}
	state.InProgress = nil
	state.NextActions = []string{"Write integration tests"}
	pf.Write(state)

	// Complete remaining tasks in mission
	mm.CompleteTask("T2", "def456")
	mm.CompleteTask("T3", "ghi789")

	// Final mission state
	finalMission, _ := mm.Load()
	finalSummary := finalMission.Summary()
	if !strings.Contains(finalSummary, "3 done") {
		t.Errorf("final summary should show 3 done: %s", finalSummary)
	}

	// Final progress
	finalProgress, _ := pf.Read()
	if len(finalProgress.Completed) != 3 {
		t.Errorf("progress should show 3 completed, got %d", len(finalProgress.Completed))
	}

	t.Log("═══════════════════════════════════════════════════════════")
	t.Log("END-TO-END TEST PASSED")
	t.Log("All 6 phases integrated and working together:")
	t.Log("  Phase 1: Progress file created and updated")
	t.Log("  Phase 2: Mission with 4 tasks, dependency resolution")
	t.Log("  Phase 3: Budget tracking with token/turn limits")
	t.Log("  Phase 4: Worktree isolation and dependency-ordered merge")
	t.Log("  Phase 5: Checkpoint persistence and crash recovery")
	t.Log("  Phase 6: Pattern detection and skill generation")
	t.Log("═══════════════════════════════════════════════════════════")
}

// e2eRunGit runs a git command in the given directory for E2E tests.
func e2eRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
