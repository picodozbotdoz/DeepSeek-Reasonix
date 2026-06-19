package boot

import (
	"strings"

	"reasonix/internal/config"
)

// asyncDelegationGuidance returns guidance text for async worker delegation
// when worker plugins are configured. Workers are detected by name containing "worker".
func asyncDelegationGuidance(entries []config.PluginEntry) string {
	var workerNames []string
	for _, e := range entries {
		name := strings.ToLower(strings.TrimSpace(e.Name))
		if strings.Contains(name, "worker") {
			workerNames = append(workerNames, e.Name)
		}
	}
	if len(workerNames) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Async Worker Delegation\n\n")
	b.WriteString("You have background workers available for parallel task execution.\n")
	b.WriteString("Use these tools to delegate work without blocking:\n\n")
	b.WriteString("### Tools\n")
	b.WriteString("- `delegate_task_async` — Start a worker without blocking. Returns worker_id immediately.\n")
	b.WriteString("- `worker_status` — Check worker status (running/done/failed). Does not block.\n")
	b.WriteString("- `worker_list` — List all active workers and their status.\n")
	b.WriteString("- `worker_kill` — Kill a running worker.\n\n")
	b.WriteString("### Workflow\n")
	b.WriteString("1. Start multiple workers: `delegate_task_async(task=\"...\")` → returns worker_id\n")
	b.WriteString("2. Continue with other work (no blocking)\n")
	b.WriteString("3. Check status periodically: `worker_status(worker_id=\"worker-1\")`\n")
	b.WriteString("4. Get results when done\n\n")
	b.WriteString("### Available Workers\n")
	b.WriteString(formatWorkerList(workerNames))
	b.WriteString("\n### Example\n")
	b.WriteString("```\n")
	b.WriteString("# Start 3 workers in parallel\n")
	b.WriteString("delegate_task_async(task=\"Implement auth module\") → worker-1\n")
	b.WriteString("delegate_task_async(task=\"Implement API layer\") → worker-2\n")
	b.WriteString("delegate_task_async(task=\"Write tests\") → worker-3\n\n")
	b.WriteString("# Check status\n")
	b.WriteString("worker_status(worker_id=\"worker-1\") → running\n")
	b.WriteString("worker_status(worker_id=\"worker-2\") → done\n\n")
	b.WriteString("# Get result\n")
	b.WriteString("worker_status(worker_id=\"worker-2\") → Result: API layer implemented...\n")
	b.WriteString("```\n\n")
	b.WriteString("### Tips\n")
	b.WriteString("- Use `delegate_task_async` instead of `delegate_task` for parallel work\n")
	b.WriteString("- Check `worker_list` to see all active workers\n")
	b.WriteString("- Kill stuck workers with `worker_kill`\n")
	b.WriteString("- Workers have heartbeat monitoring (configurable via worker.toml)\n")
	return b.String()
}

// formatWorkerList formats worker names for the system prompt.
func formatWorkerList(names []string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString("- `" + name + "`: mcp__" + name + "__delegate_task_async\n")
	}
	return b.String()
}
