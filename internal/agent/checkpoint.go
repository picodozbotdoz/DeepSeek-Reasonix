package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/fileutil"
)

// Checkpoint captures the state at a cycle boundary for crash recovery.
type Checkpoint struct {
	ID        string    `json:"id"`
	CycleID   string    `json:"cycle_id"`
	TaskID    string    `json:"task_id"`
	MissionID string    `json:"mission_id"`
	Status    string    `json:"status"` // cycle status at checkpoint
	Worktree  string    `json:"worktree,omitempty"`
	Error     string    `json:"error,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// CheckpointStore persists checkpoints to disk for crash recovery.
type CheckpointStore struct {
	dir string
	mu  sync.Mutex
}

// NewCheckpointStore creates a store at the given directory.
func NewCheckpointStore(dir string) *CheckpointStore {
	return &CheckpointStore{dir: dir}
}

// Save persists a checkpoint to disk atomically.
func (cs *CheckpointStore) Save(cp Checkpoint) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if err := os.MkdirAll(cs.dir, 0o755); err != nil {
		return fmt.Errorf("create checkpoint dir: %w", err)
	}

	cp.CreatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	filename := fmt.Sprintf("%s_%s.json", cp.CycleID, cp.TaskID)
	path := filepath.Join(cs.dir, filename)

	tmp, err := os.CreateTemp(cs.dir, ".checkpoint.*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return fileutil.ReplaceFile(tmpPath, path)
}

// Load reads a checkpoint by cycle ID.
func (cs *CheckpointStore) Load(cycleID string) (Checkpoint, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var cp Checkpoint
	entries, err := os.ReadDir(cs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return cp, nil
		}
		return cp, fmt.Errorf("read checkpoint dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), cycleID) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cs.dir, e.Name()))
		if err != nil {
			return cp, fmt.Errorf("read checkpoint: %w", err)
		}
		if err := json.Unmarshal(data, &cp); err != nil {
			return cp, fmt.Errorf("parse checkpoint: %w", err)
		}
		return cp, nil
	}
	return cp, nil
}

// Latest returns the most recent checkpoint, or empty if none exist.
func (cs *CheckpointStore) Latest() (Checkpoint, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var cp Checkpoint
	entries, err := os.ReadDir(cs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return cp, nil
		}
		return cp, fmt.Errorf("read checkpoint dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			files = append(files, e.Name())
		}
	}
	if len(files) == 0 {
		return cp, nil
	}
	sort.Strings(files)
	latest := files[len(files)-1]

	data, err := os.ReadFile(filepath.Join(cs.dir, latest))
	if err != nil {
		return cp, fmt.Errorf("read checkpoint: %w", err)
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return cp, fmt.Errorf("parse checkpoint: %w", err)
	}
	return cp, nil
}

// Incomplete returns checkpoints that are not in a terminal state (done/aborted).
// These represent cycles that were interrupted by a crash.
func (cs *CheckpointStore) Incomplete() ([]Checkpoint, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	var checkpoints []Checkpoint
	entries, err := os.ReadDir(cs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read checkpoint dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cs.dir, e.Name()))
		if err != nil {
			continue
		}
		var cp Checkpoint
		if err := json.Unmarshal(data, &cp); err != nil {
			continue
		}
		if cp.Status != "done" && cp.Status != "aborted" {
			checkpoints = append(checkpoints, cp)
		}
	}
	return checkpoints, nil
}

// Delete removes a checkpoint by cycle ID.
func (cs *CheckpointStore) Delete(cycleID string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	entries, err := os.ReadDir(cs.dir)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), cycleID) {
			os.Remove(filepath.Join(cs.dir, e.Name()))
		}
	}
	return nil
}
