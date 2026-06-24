package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseMissionTOML parses a simple TOML-like mission file. It handles the
// exact format written by marshalMissionTOML. Not a full TOML parser —
// intentionally minimal for the mission use case.
func parseMissionTOML(data []byte, m *Mission) error {
	lines := strings.Split(string(data), "\n")
	var currentSection string
	var currentTask *MissionTask

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Section headers: [tasks] or [[tasks]]
		if strings.HasPrefix(trimmed, "[[tasks]]") {
			// Start a new task
			t := MissionTask{}
			m.Tasks = append(m.Tasks, t)
			currentTask = &m.Tasks[len(m.Tasks)-1]
			currentSection = "tasks"
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			currentSection = strings.Trim(trimmed, "[]")
			currentTask = nil
			continue
		}

		// Key-value pairs
		k, v, ok := splitKeyValue(trimmed)
		if !ok {
			continue
		}

		switch currentSection {
		case "tasks":
			if currentTask != nil {
				parseTaskField(currentTask, k, v)
			}
		case "budget":
			parseBudgetField(&m.Budget, k, v)
		default:
			parseTopField(m, k, v)
		}
	}

	return nil
}

// marshalMissionTOML serializes a Mission to TOML format.
func marshalMissionTOML(m Mission) ([]byte, error) {
	var b strings.Builder

	fmt.Fprintf(&b, "# Mission: %s\n", m.Name)
	fmt.Fprintf(&b, "mission = %q\n", m.Name)
	fmt.Fprintf(&b, "created = %q\n", m.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "updated = %q\n", m.UpdatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "status = %q\n\n", m.Status)

	if len(m.Tasks) > 0 {
		for _, t := range m.Tasks {
			b.WriteString("[[tasks]]\n")
			fmt.Fprintf(&b, "id = %q\n", t.ID)
			fmt.Fprintf(&b, "title = %q\n", t.Title)
			fmt.Fprintf(&b, "status = %q\n", t.Status)
			if len(t.DependsOn) > 0 {
				fmt.Fprintf(&b, "depends_on = [%s]\n", formatStringSlice(t.DependsOn))
			}
			if t.DoneWhen != "" {
				fmt.Fprintf(&b, "done_when = %q\n", t.DoneWhen)
			}
			if t.Worker != "" {
				fmt.Fprintf(&b, "worker = %q\n", t.Worker)
			}
			if t.BlockedBy != "" {
				fmt.Fprintf(&b, "blocked_by = %q\n", t.BlockedBy)
			}
			if t.Commit != "" {
				fmt.Fprintf(&b, "committed = %q\n", t.Commit)
			}
			if t.Error != "" {
				fmt.Fprintf(&b, "error = %q\n", t.Error)
			}
			fmt.Fprintf(&b, "created_at = %q\n", t.CreatedAt.Format(time.RFC3339))
			if t.StartedAt != nil {
				fmt.Fprintf(&b, "started_at = %q\n", t.StartedAt.Format(time.RFC3339))
			}
			if t.CompletedAt != nil {
				fmt.Fprintf(&b, "completed_at = %q\n", t.CompletedAt.Format(time.RFC3339))
			}
			b.WriteString("\n")
		}
	}

	if m.Budget.MaxTokens > 0 || m.Budget.MaxCostUSD > 0 {
		b.WriteString("[budget]\n")
		if m.Budget.MaxTokens > 0 {
			fmt.Fprintf(&b, "max_tokens = %d\n", m.Budget.MaxTokens)
		}
		fmt.Fprintf(&b, "spent_tokens = %d\n", m.Budget.SpentTokens)
		if m.Budget.MaxCostUSD > 0 {
			fmt.Fprintf(&b, "max_cost_usd = %.2f\n", m.Budget.MaxCostUSD)
		}
		fmt.Fprintf(&b, "spent_cost_usd = %.2f\n", m.Budget.SpentCostUSD)
	}

	return []byte(b.String()), nil
}

func splitKeyValue(line string) (key, value string, ok bool) {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	return key, value, true
}

func parseTopField(m *Mission, k, v string) {
	switch k {
	case "mission":
		m.Name = unquote(v)
	case "status":
		m.Status = MissionStatus(unquote(v))
	case "created":
		if t, err := time.Parse(time.RFC3339, unquote(v)); err == nil {
			m.CreatedAt = t
		}
	case "updated":
		if t, err := time.Parse(time.RFC3339, unquote(v)); err == nil {
			m.UpdatedAt = t
		}
	}
}

func parseTaskField(t *MissionTask, k, v string) {
	switch k {
	case "id":
		t.ID = unquote(v)
	case "title":
		t.Title = unquote(v)
	case "status":
		t.Status = TaskStatus(unquote(v))
	case "done_when":
		t.DoneWhen = unquote(v)
	case "worker":
		t.Worker = unquote(v)
	case "blocked_by":
		t.BlockedBy = unquote(v)
	case "committed":
		t.Commit = unquote(v)
	case "error":
		t.Error = unquote(v)
	case "depends_on":
		t.DependsOn = parseStringSlice(v)
	case "created_at":
		if t2, err := time.Parse(time.RFC3339, unquote(v)); err == nil {
			t.CreatedAt = t2
		}
	case "started_at":
		if t2, err := time.Parse(time.RFC3339, unquote(v)); err == nil {
			t.StartedAt = &t2
		}
	case "completed_at":
		if t2, err := time.Parse(time.RFC3339, unquote(v)); err == nil {
			t.CompletedAt = &t2
		}
	}
}

func parseBudgetField(b *MissionBudget, k, v string) {
	switch k {
	case "max_tokens":
		b.MaxTokens = parseInt(v)
	case "spent_tokens":
		b.SpentTokens = parseInt(v)
	case "max_cost_usd":
		b.MaxCostUSD = parseFloat(v)
	case "spent_cost_usd":
		b.SpentCostUSD = parseFloat(v)
	}
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	// Unescape basic TOML strings
	s = strings.ReplaceAll(s, "\\\"", "\"")
	s = strings.ReplaceAll(s, "\\\\", "\\")
	return s
}

func parseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func parseStringSlice(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return nil
	}
	s = strings.Trim(s, "[]")
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = unquote(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func formatStringSlice(items []string) string {
	quoted := make([]string, len(items))
	for i, s := range items {
		quoted[i] = strconv.Quote(s)
	}
	return strings.Join(quoted, ", ")
}
