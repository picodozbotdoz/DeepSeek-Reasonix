package cahooks

import (
	"testing"
)

func TestConfigLoad(t *testing.T) {
	cfg := Load("nonexistent.json")
	if cfg != nil {
		t.Error("expected nil for nonexistent file")
	}
}

func TestShouldFire(t *testing.T) {
	tests := []struct {
		name  string
		hook  HookConfig
		state State
		want  bool
	}{
		{
			name: "disabled hook",
			hook: HookConfig{Enabled: false, Trigger: TriggerPostTurn},
			state: State{Turn: 1},
			want: false,
		},
		{
			name: "post_turn always fires",
			hook: HookConfig{Enabled: true, Trigger: TriggerPostTurn, Condition: ConditionAlways},
			state: State{Turn: 1},
			want: true,
		},
		{
			name: "every_n_turns fires at interval",
			hook: HookConfig{Enabled: true, Trigger: TriggerEveryNTurns, EveryN: 5, Condition: ConditionAlways},
			state: State{Turn: 10},
			want: true,
		},
		{
			name: "every_n_turns skips non-interval",
			hook: HookConfig{Enabled: true, Trigger: TriggerEveryNTurns, EveryN: 5, Condition: ConditionAlways},
			state: State{Turn: 7},
			want: false,
		},
		{
			name: "context_threshold fires above",
			hook: HookConfig{Enabled: true, Trigger: TriggerContextThreshold, ContextThreshold: 0.7, Condition: ConditionAlways},
			state: State{ContextUsage: 0.8},
			want: true,
		},
		{
			name: "context_threshold skips below",
			hook: HookConfig{Enabled: true, Trigger: TriggerContextThreshold, ContextThreshold: 0.7, Condition: ConditionAlways},
			state: State{ContextUsage: 0.5},
			want: false,
		},
		{
			name: "condition has_errors met",
			hook: HookConfig{Enabled: true, Trigger: TriggerPostTurn, Condition: ConditionHasErrors},
			state: State{Turn: 1, HasErrors: true},
			want: true,
		},
		{
			name: "condition has_errors not met",
			hook: HookConfig{Enabled: true, Trigger: TriggerPostTurn, Condition: ConditionHasErrors},
			state: State{Turn: 1, HasErrors: false},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldFire(tt.hook, tt.state)
			if got != tt.want {
				t.Errorf("ShouldFire() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildAnalysisPrompt(t *testing.T) {
	prompt := "Analyze tool calls"
	structure := map[string]any{
		"patterns": []any{},
		"failures": []any{},
	}
	result := BuildAnalysisPrompt(prompt, structure)
	if result == "" {
		t.Error("expected non-empty prompt")
	}
	if !contains(result, "Analyze tool calls") {
		t.Error("expected prompt to contain base instruction")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSub(s, sub))
}

func containsSub(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
