package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// OrchestratorConfig configures the mission orchestrator.
type OrchestratorConfig struct {
	CycleConfig CycleConfig
	MaxRetries  int // max retries per task before marking as failed (default: 3)
}

// MissionOrchestrator reads a mission, dispatches cycles for ready tasks,
// and manages the overall execution lifecycle. It is designed to run
// continuously until the mission is complete or failed.
type MissionOrchestrator struct {
	mission     *MissionManager
	worktree    *WorktreeManager
	checkpoint  *CheckpointStore
	runner      *CycleRunner
	prov        provider.Provider
	tools       *tool.Registry
	sess        *Session
	sink        event.Sink
	config      OrchestratorConfig
	mu          sync.Mutex
	cancelled   bool
}

// NewMissionOrchestrator creates an orchestrator that manages mission execution.
func NewMissionOrchestrator(
	mission *MissionManager,
	worktree *WorktreeManager,
	cpStore *CheckpointStore,
	prov provider.Provider,
	tools *tool.Registry,
	sess *Session,
	sink event.Sink,
	cfg OrchestratorConfig,
) *MissionOrchestrator {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}

	return &MissionOrchestrator{
		mission:    mission,
		worktree:   worktree,
		checkpoint: cpStore,
		prov:       prov,
		tools:      tools,
		sess:       sess,
		sink:       sink,
		config:     cfg,
	}
}

// Run executes the mission loop: repeatedly find ready tasks, dispatch cycles,
// and update mission state until all tasks are done or the mission is cancelled.
func (mo *MissionOrchestrator) Run(ctx context.Context) error {
	slog.Info("orchestrator: starting mission loop")

	for {
		if mo.isCancelled() {
			slog.Info("orchestrator: cancelled")
			return nil
		}

		// Load current mission state
		mission, err := mo.mission.Load()
		if err != nil {
			return fmt.Errorf("load mission: %w", err)
		}
		if mission.Name == "" {
			return fmt.Errorf("no mission configured")
		}

		// Check if mission is complete
		if mo.isMissionComplete(mission) {
			mission.Status = MissionCompleted
			if err := mo.mission.Save(mission); err != nil {
				return fmt.Errorf("save completed mission: %w", err)
			}
			slog.Info("orchestrator: mission complete", "name", mission.Name)
			return nil
		}

		// Find ready tasks
		ready := mission.ReadyTasks()
		if len(ready) == 0 {
			// Check if there are running tasks (wait for them)
			if mo.hasRunningTasks(mission) {
				slog.Info("orchestrator: waiting for running tasks")
				if err := mo.waitForChange(ctx); err != nil {
					return err
				}
				continue
			}
			// Check if there are blocked tasks that might unblock
			if mo.hasBlockedTasks(mission) {
				slog.Info("orchestrator: all remaining tasks blocked")
				return nil
			}
			// No ready, no running, no blocked — something is wrong
			return fmt.Errorf("no ready tasks and no running tasks — mission stuck")
		}

		// Dispatch cycles for ready tasks
		for _, task := range ready {
			if mo.isCancelled() {
				return nil
			}
			if err := mo.dispatchTask(ctx, &task, &mission); err != nil {
				slog.Error("orchestrator: task dispatch failed", "task", task.ID, "err", err)
				_ = mo.mission.FailTask(task.ID, err.Error())
			}
		}
	}
}

// dispatchTask runs a single task through the full cycle: create worktree,
// run agent, verify, commit, merge, update mission.
func (mo *MissionOrchestrator) dispatchTask(ctx context.Context, task *MissionTask, mission *Mission) error {
	slog.Info("orchestrator: dispatching task", "task", task.ID, "title", task.Title)

	// Mark task as in progress
	if err := mo.mission.StartTask(task.ID, "orchestrator"); err != nil {
		return fmt.Errorf("start task: %w", err)
	}

	// Create worktree if configured
	if mo.worktree != nil && mo.config.CycleConfig.WorkDir != "" {
		wt, err := mo.worktree.CreateWorktree(task.ID)
		if err != nil {
			return fmt.Errorf("create worktree: %w", err)
		}
		mo.config.CycleConfig.WorkDir = wt.Path
	}

	// Build the prompt for this task
	prompt := mo.buildPrompt(task, mission)

	// Create and run cycle
	cycle := NewCycle(task.ID, mission.Name)
	runner := NewCycleRunner(mo.prov, mo.tools, mo.sess, mo.sink, mo.checkpoint, mo.config.CycleConfig)
	cycle = runner.RunCycle(ctx, cycle, prompt)

	// Update mission based on cycle result
	switch cycle.Status {
	case CycleDone:
		if err := mo.mission.CompleteTask(task.ID, cycle.Worktree); err != nil {
			return fmt.Errorf("complete task: %w", err)
		}
	case CycleFailed:
		if task.Attempts >= mo.config.MaxRetries {
			if err := mo.mission.FailTask(task.ID, fmt.Sprintf("failed after %d attempts: %s", task.Attempts, cycle.Error)); err != nil {
				return fmt.Errorf("fail task: %w", err)
			}
		} else {
			// Retry: mark as pending again
			if err := mo.mission.UpdateTask(task.ID, func(t *MissionTask) {
				t.Status = TaskPending
				t.Attempts++
			}); err != nil {
				return fmt.Errorf("reset task for retry: %w", err)
			}
		}
	}

	return nil
}

// buildPrompt creates the prompt for a task, including context from
// completed predecessor tasks.
func (mo *MissionOrchestrator) buildPrompt(task *MissionTask, mission *Mission) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Task: %s — %s\n\n", task.ID, task.Title))

	if task.DoneWhen != "" {
		b.WriteString(fmt.Sprintf("**Done when:** %s\n\n", task.DoneWhen))
	}

	// Add context from completed dependencies
	if len(task.DependsOn) > 0 {
		b.WriteString("**Completed dependencies:**\n")
		for _, depID := range task.DependsOn {
			dep := mission.TaskByID(depID)
			if dep != nil && dep.Status == TaskDone {
				b.WriteString(fmt.Sprintf("- %s: %s", dep.ID, dep.Title))
				if dep.Commit != "" {
					b.WriteString(fmt.Sprintf(" (commit: %s)", dep.Commit))
				}
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("Execute this task. When done, verify your work and commit the changes.")
	return b.String()
}

// Cancel stops the orchestrator after the current cycle completes.
func (mo *MissionOrchestrator) Cancel() {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	mo.cancelled = true
}

func (mo *MissionOrchestrator) isCancelled() bool {
	mo.mu.Lock()
	defer mo.mu.Unlock()
	return mo.cancelled
}

// isMissionComplete checks if all tasks are done.
func (mo *MissionOrchestrator) isMissionComplete(m Mission) bool {
	for _, t := range m.Tasks {
		if t.Status != TaskDone {
			return false
		}
	}
	return len(m.Tasks) > 0
}

// hasRunningTasks checks if any task is currently in progress.
func (mo *MissionOrchestrator) hasRunningTasks(m Mission) bool {
	for _, t := range m.Tasks {
		if t.Status == TaskInProgress {
			return true
		}
	}
	return false
}

// hasBlockedTasks checks if any task is blocked.
func (mo *MissionOrchestrator) hasBlockedTasks(m Mission) bool {
	for _, t := range m.Tasks {
		if t.Status == TaskBlocked {
			return true
		}
	}
	return false
}

// waitForChange blocks until the context is cancelled. In a production
// implementation this would watch the mission file for changes.
func (mo *MissionOrchestrator) waitForChange(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	}
}
