package agent

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMissionCreateAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	mission, err := mm.CreateMission("Test Mission", []MissionTask{
		{ID: "T1", Title: "Task 1", DependsOn: []string{}},
		{ID: "T2", Title: "Task 2", DependsOn: []string{"T1"}},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	if mission.Name != "Test Mission" {
		t.Errorf("name = %q, want %q", mission.Name, "Test Mission")
	}
	if mission.Status != MissionPlanning {
		t.Errorf("status = %q, want %q", mission.Status, MissionPlanning)
	}
	if len(mission.Tasks) != 2 {
		t.Fatalf("tasks count = %d, want 2", len(mission.Tasks))
	}

	// Load back
	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Name != "Test Mission" {
		t.Errorf("loaded name = %q, want %q", loaded.Name, "Test Mission")
	}
	if len(loaded.Tasks) != 2 {
		t.Errorf("loaded tasks count = %d, want 2", len(loaded.Tasks))
	}
}

func TestMissionReadyTasks(t *testing.T) {
	mission := Mission{
		Tasks: []MissionTask{
			{ID: "T1", Status: TaskDone},
			{ID: "T2", Status: TaskPending, DependsOn: []string{"T1"}},
			{ID: "T3", Status: TaskPending, DependsOn: []string{"T1", "T2"}},
			{ID: "T4", Status: TaskPending, DependsOn: []string{}},
		},
	}

	ready := mission.ReadyTasks()
	if len(ready) != 2 {
		t.Fatalf("ready count = %d, want 2", len(ready))
	}

	readyIDs := map[string]bool{}
	for _, r := range ready {
		readyIDs[r.ID] = true
	}
	if !readyIDs["T2"] {
		t.Error("T2 should be ready (T1 done)")
	}
	if !readyIDs["T4"] {
		t.Error("T4 should be ready (no deps)")
	}
	if readyIDs["T3"] {
		t.Error("T3 should not be ready (T2 not done)")
	}
}

func TestMissionTaskByID(t *testing.T) {
	mission := Mission{
		Tasks: []MissionTask{
			{ID: "T1", Title: "First"},
			{ID: "T2", Title: "Second"},
		},
	}

	task := mission.TaskByID("T2")
	if task == nil {
		t.Fatal("TaskByID returned nil for T2")
	}
	if task.Title != "Second" {
		t.Errorf("title = %q, want %q", task.Title, "Second")
	}

	if mission.TaskByID("T3") != nil {
		t.Error("TaskByID should return nil for non-existent task")
	}
}

func TestMissionSummary(t *testing.T) {
	mission := Mission{
		Status: MissionInProgress,
		Tasks: []MissionTask{
			{ID: "T1", Status: TaskDone},
			{ID: "T2", Status: TaskInProgress},
			{ID: "T3", Status: TaskBlocked},
			{ID: "T4", Status: TaskPending},
		},
	}

	summary := mission.Summary()
	if summary != "[in_progress] 4 tasks: 1 done, 1 in progress, 1 blocked, 0 failed, 1 pending" {
		t.Errorf("unexpected summary: %s", summary)
	}
}

func TestMissionFormatMission(t *testing.T) {
	now := time.Now().UTC()
	mission := Mission{
		Name:      "Test Mission",
		Status:    MissionInProgress,
		CreatedAt: now,
		UpdatedAt: now,
		Tasks: []MissionTask{
			{ID: "T1", Title: "First task", Status: TaskDone, DoneWhen: "tests pass"},
			{ID: "T2", Title: "Second task", Status: TaskInProgress, Worker: "worker-1"},
		},
	}

	formatted := mission.FormatMission()
	if formatted == "" {
		t.Error("FormatMission returned empty string")
	}
	// Should contain key info
	for _, want := range []string{"Test Mission", "T1", "First task", "T2", "worker-1", "tests pass"} {
		if !containsString(formatted, want) {
			t.Errorf("FormatMission missing %q", want)
		}
	}
}

func TestMissionCompleteTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	_, err := mm.CreateMission("Test", []MissionTask{
		{ID: "T1", Title: "Task 1"},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	if err := mm.CompleteTask("T1", "abc123"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}

	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := loaded.TaskByID("T1")
	if task == nil {
		t.Fatal("task T1 not found")
	}
	if task.Status != TaskDone {
		t.Errorf("status = %q, want %q", task.Status, TaskDone)
	}
	if task.Commit != "abc123" {
		t.Errorf("commit = %q, want %q", task.Commit, "abc123")
	}
	if task.CompletedAt == nil {
		t.Error("completed_at should be set")
	}
}

func TestMissionStartTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	_, err := mm.CreateMission("Test", []MissionTask{
		{ID: "T1", Title: "Task 1"},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	if err := mm.StartTask("T1", "worker-1"); err != nil {
		t.Fatalf("StartTask: %v", err)
	}

	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := loaded.TaskByID("T1")
	if task == nil {
		t.Fatal("task T1 not found")
	}
	if task.Status != TaskInProgress {
		t.Errorf("status = %q, want %q", task.Status, TaskInProgress)
	}
	if task.Worker != "worker-1" {
		t.Errorf("worker = %q, want %q", task.Worker, "worker-1")
	}
	if task.StartedAt == nil {
		t.Error("started_at should be set")
	}
}

func TestMissionFailTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	_, err := mm.CreateMission("Test", []MissionTask{
		{ID: "T1", Title: "Task 1"},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	if err := mm.FailTask("T1", "build failed"); err != nil {
		t.Fatalf("FailTask: %v", err)
	}

	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := loaded.TaskByID("T1")
	if task == nil {
		t.Fatal("task T1 not found")
	}
	if task.Status != TaskFailed {
		t.Errorf("status = %q, want %q", task.Status, TaskFailed)
	}
	if task.Error != "build failed" {
		t.Errorf("error = %q, want %q", task.Error, "build failed")
	}
}

func TestMissionBlockTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	_, err := mm.CreateMission("Test", []MissionTask{
		{ID: "T1", Title: "Task 1"},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	if err := mm.BlockTask("T1", "waiting for dependency"); err != nil {
		t.Fatalf("BlockTask: %v", err)
	}

	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	task := loaded.TaskByID("T1")
	if task == nil {
		t.Fatal("task T1 not found")
	}
	if task.Status != TaskBlocked {
		t.Errorf("status = %q, want %q", task.Status, TaskBlocked)
	}
	if task.BlockedBy != "waiting for dependency" {
		t.Errorf("blocked_by = %q, want %q", task.BlockedBy, "waiting for dependency")
	}
}

func TestMissionStartAndComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	_, err := mm.CreateMission("Test", []MissionTask{
		{ID: "T1", Title: "Task 1"},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	if err := mm.StartMission(); err != nil {
		t.Fatalf("StartMission: %v", err)
	}

	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Status != MissionInProgress {
		t.Errorf("status = %q, want %q", loaded.Status, MissionInProgress)
	}
}

func TestMissionNonExistent(t *testing.T) {
	mm := NewMissionManager("/nonexistent/path/MISSION.toml")
	mission, err := mm.Load()
	if err != nil {
		t.Fatalf("Load on nonexistent: %v", err)
	}
	if mission.Name != "" {
		t.Errorf("expected empty name, got %q", mission.Name)
	}
	if mm.Exists() {
		t.Error("Exists should return false for nonexistent file")
	}
}

func TestMissionAddTask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	_, err := mm.CreateMission("Test", []MissionTask{
		{ID: "T1", Title: "Task 1"},
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	if err := mm.AddTask(MissionTask{ID: "T2", Title: "Task 2"}); err != nil {
		t.Fatalf("AddTask: %v", err)
	}

	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Tasks) != 2 {
		t.Fatalf("tasks count = %d, want 2", len(loaded.Tasks))
	}
	if loaded.Tasks[1].ID != "T2" {
		t.Errorf("second task ID = %q, want %q", loaded.Tasks[1].ID, "T2")
	}
}

func TestMissionUpdateTaskNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	_, err := mm.CreateMission("Test", nil)
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	err = mm.UpdateTask("NONEXISTENT", func(t *MissionTask) {})
	if err == nil {
		t.Error("expected error for non-existent task")
	}
}

func TestMissionBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "MISSION.toml")
	mm := NewMissionManager(path)

	mission, err := mm.CreateMission("Test", nil)
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}

	mission.Budget = MissionBudget{
		MaxTokens:    1000000,
		MaxCostUSD:   50.0,
		SpentTokens:  250000,
		SpentCostUSD: 12.5,
	}
	if err := mm.Save(mission); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := mm.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Budget.MaxTokens != 1000000 {
		t.Errorf("max_tokens = %d, want 1000000", loaded.Budget.MaxTokens)
	}
	if loaded.Budget.SpentCostUSD != 12.5 {
		t.Errorf("spent_cost_usd = %f, want 12.5", loaded.Budget.SpentCostUSD)
	}
}

func TestMissionTomlRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	started := now.Add(time.Minute)
	completed := now.Add(time.Hour)

	original := Mission{
		Name:      "Round Trip Test",
		CreatedAt: now,
		UpdatedAt: now,
		Status:    MissionInProgress,
		Tasks: []MissionTask{
			{
				ID:          "T1",
				Title:       "First task",
				Status:      TaskDone,
				DependsOn:   []string{},
				DoneWhen:    "tests pass",
				Commit:      "abc123",
				CreatedAt:   now,
				CompletedAt: &completed,
			},
			{
				ID:        "T2",
				Title:     "Second task",
				Status:    TaskInProgress,
				DependsOn: []string{"T1"},
				DoneWhen:  "lint passes",
				Worker:    "worker-1",
				CreatedAt: now,
				StartedAt: &started,
			},
		},
		Budget: MissionBudget{
			MaxTokens:   1000000,
			SpentTokens: 500000,
			MaxCostUSD:  25.0,
			SpentCostUSD: 12.5,
		},
	}

	data, err := marshalMissionTOML(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed Mission
	if err := parseMissionTOML(data, &parsed); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if parsed.Name != original.Name {
		t.Errorf("name = %q, want %q", parsed.Name, original.Name)
	}
	if parsed.Status != original.Status {
		t.Errorf("status = %q, want %q", parsed.Status, original.Status)
	}
	if len(parsed.Tasks) != 2 {
		t.Fatalf("tasks count = %d, want 2", len(parsed.Tasks))
	}
	if parsed.Tasks[0].ID != "T1" {
		t.Errorf("task[0].id = %q, want %q", parsed.Tasks[0].ID, "T1")
	}
	if parsed.Tasks[0].Status != TaskDone {
		t.Errorf("task[0].status = %q, want %q", parsed.Tasks[0].Status, TaskDone)
	}
	if parsed.Tasks[0].Commit != "abc123" {
		t.Errorf("task[0].commit = %q, want %q", parsed.Tasks[0].Commit, "abc123")
	}
	if parsed.Tasks[1].Worker != "worker-1" {
		t.Errorf("task[1].worker = %q, want %q", parsed.Tasks[1].Worker, "worker-1")
	}
	if parsed.Budget.MaxTokens != 1000000 {
		t.Errorf("budget.max_tokens = %d, want 1000000", parsed.Budget.MaxTokens)
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
