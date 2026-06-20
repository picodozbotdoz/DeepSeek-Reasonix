package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/fileutil"
)

// MissionStatus represents the overall state of a mission.
type MissionStatus string

const (
	MissionPlanning   MissionStatus = "planning"
	MissionInProgress MissionStatus = "in_progress"
	MissionCompleted  MissionStatus = "completed"
	MissionFailed     MissionStatus = "failed"
	MissionPaused     MissionStatus = "paused"
)

// TaskStatus represents the state of a single task within a mission.
type TaskStatus string

const (
	TaskPending    TaskStatus = "pending"
	TaskInProgress TaskStatus = "in_progress"
	TaskDone       TaskStatus = "done"
	TaskBlocked    TaskStatus = "blocked"
	TaskFailed     TaskStatus = "failed"
)

// MissionTask is one unit of work within a mission.
type MissionTask struct {
	ID          string     `json:"id" yaml:"id"`
	Title       string     `json:"title" yaml:"title"`
	Status      TaskStatus `json:"status" yaml:"status"`
	DependsOn   []string   `json:"depends_on" yaml:"depends_on"`
	DoneWhen    string     `json:"done_when" yaml:"done_when"`
	Worker      string     `json:"worker,omitempty" yaml:"worker,omitempty"`
	BlockedBy   string     `json:"blocked_by,omitempty" yaml:"blocked_by,omitempty"`
	Commit      string     `json:"committed,omitempty" yaml:"committed,omitempty"`
	Error       string     `json:"error,omitempty" yaml:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at" yaml:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty" yaml:"completed_at,omitempty"`
}

// MissionBudget tracks resource usage for a mission.
type MissionBudget struct {
	MaxTokens   int     `json:"max_tokens" yaml:"max_tokens"`
	SpentTokens int     `json:"spent_tokens" yaml:"spent_tokens"`
	MaxCostUSD  float64 `json:"max_cost_usd" yaml:"max_cost_usd"`
	SpentCostUSD float64 `json:"spent_cost_usd" yaml:"spent_cost_usd"`
}

// Mission is the top-level state for a multi-day SWE task.
type Mission struct {
	Name      string       `json:"mission" yaml:"mission"`
	CreatedAt time.Time    `json:"created" yaml:"created"`
	UpdatedAt time.Time    `json:"updated" yaml:"updated"`
	Status    MissionStatus `json:"status" yaml:"status"`
	Tasks     []MissionTask `json:"tasks" yaml:"tasks"`
	Budget    MissionBudget `json:"budget" yaml:"budget"`
}

// MissionManager provides load/query/update operations on a mission file.
type MissionManager struct {
	path string
	mu   sync.Mutex
}

// NewMissionManager creates a manager for a MISSION.toml file at the given path.
func NewMissionManager(path string) *MissionManager {
	return &MissionManager{path: path}
}

// Path returns the absolute path to the mission file.
func (m *MissionManager) Path() string { return m.path }

// Exists returns true if the mission file exists on disk.
func (m *MissionManager) Exists() bool {
	_, err := os.Stat(m.path)
	return err == nil
}

// Load reads the mission file from disk. Returns a zero-value Mission if the
// file doesn't exist (no error).
func (m *MissionManager) Load() (Mission, error) {
	var mission Mission
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return mission, nil
		}
		return mission, fmt.Errorf("read mission: %w", err)
	}
	if err := parseMissionTOML(data, &mission); err != nil {
		return mission, fmt.Errorf("parse mission: %w", err)
	}
	return mission, nil
}

// Save writes the mission state to disk atomically.
func (m *MissionManager) Save(mission Mission) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	mission.UpdatedAt = time.Now().UTC()
	data, err := marshalMissionTOML(mission)
	if err != nil {
		return fmt.Errorf("marshal mission: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("create mission dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".mission.*.tmp")
	if err != nil {
		return fmt.Errorf("create mission tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write mission: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return fileutil.ReplaceFile(tmpPath, m.path)
}

// CreateMission initializes a new mission with the given name and tasks.
func (m *MissionManager) CreateMission(name string, tasks []MissionTask) (Mission, error) {
	now := time.Now().UTC()
	mission := Mission{
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    MissionPlanning,
		Tasks:     tasks,
		Budget:    MissionBudget{},
	}
	for i := range mission.Tasks {
		mission.Tasks[i].Status = TaskPending
		mission.Tasks[i].CreatedAt = now
	}
	if err := m.Save(mission); err != nil {
		return Mission{}, err
	}
	return mission, nil
}

// StartMission transitions the mission to in_progress.
func (m *MissionManager) StartMission() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mission, err := m.loadLocked()
	if err != nil {
		return err
	}
	mission.Status = MissionInProgress
	return m.saveLocked(mission)
}

// AddTask appends a new task to the mission.
func (m *MissionManager) AddTask(task MissionTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mission, err := m.loadLocked()
	if err != nil {
		return err
	}
	task.Status = TaskPending
	task.CreatedAt = time.Now().UTC()
	mission.Tasks = append(mission.Tasks, task)
	return m.saveLocked(mission)
}

// UpdateTask updates a specific task by ID.
func (m *MissionManager) UpdateTask(id string, update func(*MissionTask)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mission, err := m.loadLocked()
	if err != nil {
		return err
	}
	for i := range mission.Tasks {
		if mission.Tasks[i].ID == id {
			update(&mission.Tasks[i])
			return m.saveLocked(mission)
		}
	}
	return fmt.Errorf("task %q not found", id)
}

// CompleteTask marks a task as done with an optional commit hash.
func (m *MissionManager) CompleteTask(id, commit string) error {
	return m.UpdateTask(id, func(t *MissionTask) {
		now := time.Now().UTC()
		t.Status = TaskDone
		t.Commit = commit
		t.CompletedAt = &now
	})
}

// FailTask marks a task as failed with an error message.
func (m *MissionManager) FailTask(id, errMsg string) error {
	return m.UpdateTask(id, func(t *MissionTask) {
		t.Status = TaskFailed
		t.Error = errMsg
	})
}

// BlockTask marks a task as blocked with a reason.
func (m *MissionManager) BlockTask(id, reason string) error {
	return m.UpdateTask(id, func(t *MissionTask) {
		t.Status = TaskBlocked
		t.BlockedBy = reason
	})
}

// StartTask marks a task as in_progress and assigns a worker.
func (m *MissionManager) StartTask(id, worker string) error {
	return m.UpdateTask(id, func(t *MissionTask) {
		now := time.Now().UTC()
		t.Status = TaskInProgress
		t.Worker = worker
		t.StartedAt = &now
	})
}

// ReadyTasks returns tasks whose dependencies are all done and status is pending.
func (m *MissionManager) ReadyTasks() ([]MissionTask, error) {
	mission, err := m.Load()
	if err != nil {
		return nil, err
	}
	return mission.ReadyTasks(), nil
}

// ReadyTasks returns tasks whose dependencies are all done and status is pending.
func (mission *Mission) ReadyTasks() []MissionTask {
	doneSet := map[string]bool{}
	for _, t := range mission.Tasks {
		if t.Status == TaskDone {
			doneSet[t.ID] = true
		}
	}
	var ready []MissionTask
	for _, t := range mission.Tasks {
		if t.Status != TaskPending {
			continue
		}
		allDone := true
		for _, dep := range t.DependsOn {
			if !doneSet[dep] {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, t)
		}
	}
	return ready
}

// TaskByID returns the task with the given ID, or nil if not found.
func (mission *Mission) TaskByID(id string) *MissionTask {
	for i := range mission.Tasks {
		if mission.Tasks[i].ID == id {
			return &mission.Tasks[i]
		}
	}
	return nil
}

// Summary returns a one-line status summary.
func (mission *Mission) Summary() string {
	var done, inProgress, blocked, failed, pending int
	for _, t := range mission.Tasks {
		switch t.Status {
		case TaskDone:
			done++
		case TaskInProgress:
			inProgress++
		case TaskBlocked:
			blocked++
		case TaskFailed:
			failed++
		default:
			pending++
		}
	}
	return fmt.Sprintf("[%s] %d tasks: %d done, %d in progress, %d blocked, %d failed, %d pending",
		mission.Status, len(mission.Tasks), done, inProgress, blocked, failed, pending)
}

// FormatMission returns a human-readable mission status report.
func (mission *Mission) FormatMission() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Mission: %s\n", mission.Name)
	fmt.Fprintf(&b, "Status: %s\n", mission.Status)
	fmt.Fprintf(&b, "Created: %s\n", mission.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "Updated: %s\n\n", mission.UpdatedAt.Format(time.RFC3339))

	if len(mission.Tasks) == 0 {
		b.WriteString("No tasks defined.\n")
		return b.String()
	}

	// Sort tasks by ID for stable output
	sorted := make([]MissionTask, len(mission.Tasks))
	copy(sorted, mission.Tasks)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	b.WriteString("## Tasks\n\n")
	for _, t := range sorted {
		icon := taskIcon(t.Status)
		line := fmt.Sprintf("%s %s — %s", icon, t.ID, t.Title)
		if t.DoneWhen != "" {
			line += fmt.Sprintf("\n   done_when: %s", t.DoneWhen)
		}
		if len(t.DependsOn) > 0 {
			line += fmt.Sprintf("\n   depends_on: [%s]", strings.Join(t.DependsOn, ", "))
		}
		if t.Worker != "" {
			line += fmt.Sprintf("\n   worker: %s", t.Worker)
		}
		if t.Commit != "" {
			line += fmt.Sprintf("\n   committed: %s", t.Commit)
		}
		if t.Error != "" {
			line += fmt.Sprintf("\n   error: %s", t.Error)
		}
		if t.BlockedBy != "" {
			line += fmt.Sprintf("\n   blocked_by: %s", t.BlockedBy)
		}
		b.WriteString(line + "\n\n")
	}

	if mission.Budget.MaxTokens > 0 || mission.Budget.MaxCostUSD > 0 {
		b.WriteString("## Budget\n\n")
		if mission.Budget.MaxTokens > 0 {
			fmt.Fprintf(&b, "Tokens: %d / %d\n", mission.Budget.SpentTokens, mission.Budget.MaxTokens)
		}
		if mission.Budget.MaxCostUSD > 0 {
			fmt.Fprintf(&b, "Cost: $%.2f / $%.2f\n", mission.Budget.SpentCostUSD, mission.Budget.MaxCostUSD)
		}
	}

	return b.String()
}

func taskIcon(status TaskStatus) string {
	switch status {
	case TaskDone:
		return "✅"
	case TaskInProgress:
		return "🔄"
	case TaskBlocked:
		return "🚫"
	case TaskFailed:
		return "❌"
	default:
		return "⬜"
	}
}

// loadLocked and saveLocked are internal helpers that assume the mutex is held.
func (m *MissionManager) loadLocked() (Mission, error) {
	var mission Mission
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return mission, nil
		}
		return mission, fmt.Errorf("read mission: %w", err)
	}
	if err := parseMissionTOML(data, &mission); err != nil {
		return mission, fmt.Errorf("parse mission: %w", err)
	}
	return mission, nil
}

func (m *MissionManager) saveLocked(mission Mission) error {
	mission.UpdatedAt = time.Now().UTC()
	data, err := marshalMissionTOML(mission)
	if err != nil {
		return fmt.Errorf("marshal mission: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return fmt.Errorf("create mission dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(m.path), ".mission.*.tmp")
	if err != nil {
		return fmt.Errorf("create mission tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write mission: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return fileutil.ReplaceFile(tmpPath, m.path)
}
