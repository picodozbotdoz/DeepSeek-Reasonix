package agent

import (
	"fmt"
	"time"
)

// CycleStatus represents the state of a single execution cycle.
type CycleStatus string

const (
	CyclePending    CycleStatus = "pending"
	CycleRunning    CycleStatus = "running"
	CycleVerifying  CycleStatus = "verifying"
	CycleCommitting CycleStatus = "committing"
	CycleDone       CycleStatus = "done"
	CycleFailed     CycleStatus = "failed"
	CycleAborted    CycleStatus = "aborted"
)

// Cycle is one unit of work: read task → execute → verify → commit → checkpoint.
// Each cycle is deliberately small so max loss from crash is one unfinished cycle.
type Cycle struct {
	ID        string     `json:"id"`
	TaskID    string     `json:"task_id"`
	MissionID string     `json:"mission_id"`
	Status    CycleStatus `json:"status"`
	Worktree  string     `json:"worktree,omitempty"`
	Error     string     `json:"error,omitempty"`
	Prompt    string     `json:"prompt,omitempty"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	Attempts  int        `json:"attempts"`
}

// CycleConfig configures how a cycle executes.
type CycleConfig struct {
	MaxTurns     int    // max LLM turns per cycle (0 = use agent default)
	TestCommand  string // verification command to run after execution
	WorkDir      string // working directory for git operations
	MainBranch   string // branch to merge into
	MissionPath  string // path to MISSION.toml
}

// NewCycle creates a new cycle for the given task.
func NewCycle(taskID, missionID string) *Cycle {
	return &Cycle{
		ID:        fmt.Sprintf("cycle-%s-%d", taskID, time.Now().UnixMilli()),
		TaskID:    taskID,
		MissionID: missionID,
		Status:    CyclePending,
		StartedAt: time.Now().UTC(),
		Attempts:  1,
	}
}

// MarkRunning transitions the cycle to running state.
func (c *Cycle) MarkRunning() {
	c.Status = CycleRunning
}

// MarkVerifying transitions the cycle to verifying state.
func (c *Cycle) MarkVerifying() {
	c.Status = CycleVerifying
}

// MarkCommitting transitions the cycle to committing state.
func (c *Cycle) MarkCommitting() {
	c.Status = CycleCommitting
}

// MarkDone completes the cycle.
func (c *Cycle) MarkDone() {
	now := time.Now().UTC()
	c.Status = CycleDone
	c.EndedAt = &now
}

// MarkFailed records a failure.
func (c *Cycle) MarkFailed(err error) {
	now := time.Now().UTC()
	c.Status = CycleFailed
	c.EndedAt = &now
	if err != nil {
		c.Error = err.Error()
	}
}

// MarkAborted stops the cycle without failure.
func (c *Cycle) MarkAborted() {
	now := time.Now().UTC()
	c.Status = CycleAborted
	c.EndedAt = &now
}

// IsTerminal returns true if the cycle is in a final state.
func (c *Cycle) IsTerminal() bool {
	return c.Status == CycleDone || c.Status == CycleFailed || c.Status == CycleAborted
}

// Duration returns how long the cycle has been running.
func (c *Cycle) Duration() time.Duration {
	if c.EndedAt != nil {
		return c.EndedAt.Sub(c.StartedAt)
	}
	return time.Since(c.StartedAt)
}
