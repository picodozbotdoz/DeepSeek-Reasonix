package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reasonix/internal/tool"
)

// MissionTool lets the manager create, query, and update missions.
type MissionTool struct {
	manager *MissionManager
}

// NewMissionTool builds the mission management tool.
func NewMissionTool(manager *MissionManager) tool.Tool {
	return &MissionTool{manager: manager}
}

func (*MissionTool) Name() string   { return "mission" }
func (*MissionTool) ReadOnly() bool { return false }

func (*MissionTool) Description() string {
	return "Manage long-running missions with structured tasks, dependencies, and verifiable done-conditions. " +
		"Use 'create' to start a new mission, 'add' to add tasks, 'start' to begin execution, " +
		"'complete'/'fail'/'block' to update task status, 'ready' to see which tasks can run next, " +
		"and 'status' to view the full mission state."
}

func (*MissionTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["create", "add", "start", "start_task", "complete", "fail", "block", "ready", "status"],
				"description": "Mission action to perform"
			},
			"name": {"type": "string", "description": "Mission name (for create)"},
			"task_id": {"type": "string", "description": "Task ID (for task operations)"},
			"title": {"type": "string", "description": "Task title (for add)"},
			"depends_on": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Task IDs this task depends on (for add)"
			},
			"done_when": {"type": "string", "description": "Verifiable done condition (for add)"},
			"worker": {"type": "string", "description": "Worker assignment (for start_task)"},
			"commit": {"type": "string", "description": "Git commit hash (for complete)"},
			"error": {"type": "string", "description": "Error message (for fail)"},
			"reason": {"type": "string", "description": "Block reason (for block)"},
			"max_tokens": {"type": "integer", "description": "Token budget (for create)"},
			"max_cost_usd": {"type": "number", "description": "Cost budget in USD (for create)"}
		},
		"required": ["action"]
	}`)
}

func (t *MissionTool) Execute(_ context.Context, raw json.RawMessage) (string, error) {
	var p struct {
		Action     string   `json:"action"`
		Name       string   `json:"name"`
		TaskID     string   `json:"task_id"`
		Title      string   `json:"title"`
		DependsOn  []string `json:"depends_on"`
		DoneWhen   string   `json:"done_when"`
		Worker     string   `json:"worker"`
		Commit     string   `json:"commit"`
		Error      string   `json:"error"`
		Reason     string   `json:"reason"`
		MaxTokens  int      `json:"max_tokens"`
		MaxCostUSD float64  `json:"max_cost_usd"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}

	switch strings.ToLower(p.Action) {
	case "create":
		if p.Name == "" {
			return "", fmt.Errorf("name is required for create")
		}
		mission, err := t.manager.CreateMission(p.Name, nil)
		if err != nil {
			return "", fmt.Errorf("create mission: %w", err)
		}
		if p.MaxTokens > 0 || p.MaxCostUSD > 0 {
			mission.Budget = MissionBudget{MaxTokens: p.MaxTokens, MaxCostUSD: p.MaxCostUSD}
			if err := t.manager.Save(mission); err != nil {
				return "", fmt.Errorf("save budget: %w", err)
			}
		}
		return fmt.Sprintf("Mission created: %s\nStatus: %s\nUse 'add' to add tasks.", mission.Name, mission.Status), nil

	case "add":
		if p.TaskID == "" {
			return "", fmt.Errorf("task_id is required for add")
		}
		if p.Title == "" {
			return "", fmt.Errorf("title is required for add")
		}
		task := MissionTask{ID: p.TaskID, Title: p.Title, DependsOn: p.DependsOn, DoneWhen: p.DoneWhen}
		if err := t.manager.AddTask(task); err != nil {
			return "", fmt.Errorf("add task: %w", err)
		}
		return fmt.Sprintf("Task %s added: %s", p.TaskID, p.Title), nil

	case "start":
		if err := t.manager.StartMission(); err != nil {
			return "", fmt.Errorf("start mission: %w", err)
		}
		return "Mission started.", nil

	case "start_task":
		if p.TaskID == "" {
			return "", fmt.Errorf("task_id is required for start_task")
		}
		if err := t.manager.StartTask(p.TaskID, p.Worker); err != nil {
			return "", fmt.Errorf("start task: %w", err)
		}
		return fmt.Sprintf("Task %s started.", p.TaskID), nil

	case "complete":
		if p.TaskID == "" {
			return "", fmt.Errorf("task_id is required for complete")
		}
		if err := t.manager.CompleteTask(p.TaskID, p.Commit); err != nil {
			return "", fmt.Errorf("complete task: %w", err)
		}
		return fmt.Sprintf("Task %s completed.", p.TaskID), nil

	case "fail":
		if p.TaskID == "" {
			return "", fmt.Errorf("task_id is required for fail")
		}
		if err := t.manager.FailTask(p.TaskID, p.Error); err != nil {
			return "", fmt.Errorf("fail task: %w", err)
		}
		return fmt.Sprintf("Task %s failed: %s", p.TaskID, p.Error), nil

	case "block":
		if p.TaskID == "" {
			return "", fmt.Errorf("task_id is required for block")
		}
		if err := t.manager.BlockTask(p.TaskID, p.Reason); err != nil {
			return "", fmt.Errorf("block task: %w", err)
		}
		return fmt.Sprintf("Task %s blocked: %s", p.TaskID, p.Reason), nil

	case "ready":
		ready, err := t.manager.ReadyTasks()
		if err != nil {
			return "", fmt.Errorf("get ready tasks: %w", err)
		}
		if len(ready) == 0 {
			return "No tasks ready to execute. All pending tasks have unmet dependencies.", nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d task(s) ready to execute:\n\n", len(ready))
		for _, task := range ready {
			fmt.Fprintf(&b, "  %s — %s\n", task.ID, task.Title)
			if task.DoneWhen != "" {
				fmt.Fprintf(&b, "    done_when: %s\n", task.DoneWhen)
			}
		}
		return b.String(), nil

	case "status":
		mission, err := t.manager.Load()
		if err != nil {
			return "", fmt.Errorf("load mission: %w", err)
		}
		if mission.Name == "" {
			return "No mission configured. Use 'create' to start a new mission.", nil
		}
		return mission.FormatMission(), nil

	default:
		return "", fmt.Errorf("unknown action %q; valid: create, add, start, start_task, complete, fail, block, ready, status", p.Action)
	}
}
