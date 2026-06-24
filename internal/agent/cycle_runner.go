package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// CycleRunner executes a single cycle: run model → verify → commit → checkpoint.
// It is designed to be called from the MissionOrchestrator or directly.
type CycleRunner struct {
	prov       provider.Provider
	tools      *tool.Registry
	session    *Session
	sink       event.Sink
	checkpoint *CheckpointStore
	config     CycleConfig
}

// NewCycleRunner creates a runner that executes cycles within the given context.
func NewCycleRunner(prov provider.Provider, tools *tool.Registry, sess *Session, sink event.Sink, cpStore *CheckpointStore, cfg CycleConfig) *CycleRunner {
	return &CycleRunner{
		prov:       prov,
		tools:      tools,
		session:    sess,
		sink:       sink,
		checkpoint: cpStore,
		config:     cfg,
	}
}

// RunCycle executes one complete cycle: run the agent on the task prompt,
// verify results, commit changes, and checkpoint state. Returns the cycle
// with its final status.
func (cr *CycleRunner) RunCycle(ctx context.Context, cycle *Cycle, prompt string) *Cycle {
	cycle.Prompt = prompt
	cycle.MarkRunning()

	// Checkpoint: cycle started
	cr.saveCheckpoint(cycle, "running")

	// Step 1: Run the agent
	cr.emitNotice(fmt.Sprintf("Cycle %s: executing task %s", cycle.ID, cycle.TaskID))
	if err := cr.runAgent(ctx, cycle, prompt); err != nil {
		cycle.MarkFailed(err)
		cr.saveCheckpoint(cycle, "failed")
		return cycle
	}

	// Step 2: Verify
	cycle.MarkVerifying()
	cr.saveCheckpoint(cycle, "verifying")
	if err := cr.verify(cycle); err != nil {
		cycle.MarkFailed(err)
		cr.saveCheckpoint(cycle, "failed")
		return cycle
	}

	// Step 3: Commit
	cycle.MarkCommitting()
	cr.saveCheckpoint(cycle, "committing")
	if err := cr.commit(cycle); err != nil {
		cycle.MarkFailed(err)
		cr.saveCheckpoint(cycle, "failed")
		return cycle
	}

	// Done
	cycle.MarkDone()
	cr.saveCheckpoint(cycle, "done")
	cr.emitNotice(fmt.Sprintf("Cycle %s: completed task %s in %s", cycle.ID, cycle.TaskID, cycle.Duration()))
	return cycle
}

// runAgent executes the agent loop for the cycle. It uses a bounded number
// of turns to prevent runaway execution.
func (cr *CycleRunner) runAgent(ctx context.Context, cycle *Cycle, prompt string) error {
	// Create a fresh agent for this cycle's work
	agent := New(cr.prov, cr.tools, cr.session, Options{
		MaxSteps:    cr.config.MaxTurns,
		UsageSource: event.UsageSourceSubagent,
	}, cr.sink)

	if err := agent.Run(ctx, prompt); err != nil {
		return fmt.Errorf("agent run: %w", err)
	}
	return nil
}

// verify runs the verification command (if configured) to check that the
// changes are correct.
func (cr *CycleRunner) verify(cycle *Cycle) error {
	if cr.config.TestCommand == "" {
		return nil // no verification configured
	}

	parts := strings.Fields(cr.config.TestCommand)
	cmd := exec.Command(parts[0], parts[1:]...)
	cmd.Dir = cr.config.WorkDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("verification failed: %w\n%s", err, string(output))
	}
	slog.Info("cycle: verification passed", "cycle", cycle.ID, "task", cycle.TaskID)
	return nil
}

// commit stages and commits all changes in the worktree.
func (cr *CycleRunner) commit(cycle *Cycle) error {
	if cr.config.WorkDir == "" {
		return nil
	}

	// git add -A
	if _, err := runGitCmd(cr.config.WorkDir, "add", "-A"); err != nil {
		return fmt.Errorf("stage changes: %w", err)
	}

	// Check if there's anything to commit
	if _, err := runGitCmd(cr.config.WorkDir, "diff", "--cached", "--quiet"); err == nil {
		return nil // nothing to commit
	}

	msg := fmt.Sprintf("lrswe: complete task %s (cycle %s)", cycle.TaskID, cycle.ID)
	if _, err := runGitCmd(cr.config.WorkDir, "commit", "-m", msg); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	// Get commit hash
	out, err := runGitCmd(cr.config.WorkDir, "rev-parse", "HEAD")
	if err == nil {
		cycle.Worktree = strings.TrimSpace(string(out))
	}
	return nil
}

// saveCheckpoint persists the cycle state to the checkpoint store.
func (cr *CycleRunner) saveCheckpoint(cycle *Cycle, status string) {
	if cr.checkpoint == nil {
		return
	}
	cp := Checkpoint{
		ID:        cycle.ID,
		CycleID:   cycle.ID,
		TaskID:    cycle.TaskID,
		MissionID: cycle.MissionID,
		Status:    status,
		Worktree:  cycle.Worktree,
		Error:     cycle.Error,
	}
	if err := cr.checkpoint.Save(cp); err != nil {
		slog.Warn("cycle: failed to save checkpoint", "cycle", cycle.ID, "err", err)
	}
}

// emitNotice sends a notice event to the sink.
func (cr *CycleRunner) emitNotice(text string) {
	if cr.sink != nil {
		cr.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo, Text: text})
	}
}

// runGitCmd runs a git command in the given directory.
func runGitCmd(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}
