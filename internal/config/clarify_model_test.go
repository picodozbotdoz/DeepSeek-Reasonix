package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func TestAgentClarifyModelDecodesFromTOML(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
[agent]
clarify_model = "deepseek/deepseek-chat"
`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Agent.ClarifyModel != "deepseek/deepseek-chat" {
		t.Fatalf("clarify_model = %q, want deepseek/deepseek-chat", cfg.Agent.ClarifyModel)
	}
	if got := cfg.ClarifyModel(); got != "deepseek/deepseek-chat" {
		t.Fatalf("ClarifyModel() = %q, want deepseek/deepseek-chat", got)
	}
}

func TestAgentClarifyModelEmptyByDefault(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
[agent]
max_steps = 10
`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Agent.ClarifyModel != "" {
		t.Fatalf("clarify_model should be empty by default, got %q", cfg.Agent.ClarifyModel)
	}
	if got := cfg.ClarifyModel(); got != "" {
		t.Fatalf("ClarifyModel() should be empty, got %q", got)
	}
}

func TestAgentClarifyModelAccessorNilConfig(t *testing.T) {
	var c *Config
	if got := c.ClarifyModel(); got != "" {
		t.Fatalf("ClarifyModel() on nil config should return \"\", got %q", got)
	}
}

func TestClarifyConfigDecodesFromTOML(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
[clarify]
model = "deepseek/deepseek-chat"

[clarify.fresh]
enabled = true
system_prompt = "Custom fresh prompt"
instruction = "Make it concise"
max_history_pairs = 3
tool_names = true

[clarify.context]
enabled = false
system_prompt = "Custom context prompt"
instruction = "Use full context"
tool_names = true
`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if cfg.Clarify.Model != "deepseek/deepseek-chat" {
		t.Fatalf("Clarify.Model = %q", cfg.Clarify.Model)
	}
	if !cfg.Clarify.Fresh.Enabled {
		t.Fatal("Clarify.Fresh.Enabled should be true")
	}
	if cfg.Clarify.Fresh.SystemPrompt != "Custom fresh prompt" {
		t.Fatalf("Clarify.Fresh.SystemPrompt = %q", cfg.Clarify.Fresh.SystemPrompt)
	}
	if cfg.Clarify.Fresh.Instruction != "Make it concise" {
		t.Fatalf("Clarify.Fresh.Instruction = %q", cfg.Clarify.Fresh.Instruction)
	}
	if cfg.Clarify.Fresh.MaxHistoryPairs != 3 {
		t.Fatalf("Clarify.Fresh.MaxHistoryPairs = %d", cfg.Clarify.Fresh.MaxHistoryPairs)
	}
	if !cfg.Clarify.Fresh.ToolNames {
		t.Fatal("Clarify.Fresh.ToolNames should be true")
	}
	if cfg.Clarify.Context.Enabled {
		t.Fatal("Clarify.Context.Enabled should be false")
	}
	if cfg.Clarify.Context.SystemPrompt != "Custom context prompt" {
		t.Fatalf("Clarify.Context.SystemPrompt = %q", cfg.Clarify.Context.SystemPrompt)
	}
	if cfg.Clarify.Context.Instruction != "Use full context" {
		t.Fatalf("Clarify.Context.Instruction = %q", cfg.Clarify.Context.Instruction)
	}
	if !cfg.Clarify.Context.ToolNames {
		t.Fatal("Clarify.Context.ToolNames should be true")
	}
}

func TestClarifyConfigDefaultValues(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(``, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Defaults: bools are false (Go zero value), strings empty
	if cfg.Clarify.Fresh.Enabled {
		t.Fatal("Fresh mode should default to false (Go zero value)")
	}
	if cfg.Clarify.Context.Enabled {
		t.Fatal("Context mode should default to false (Go zero value)")
	}
	if cfg.Clarify.Fresh.MaxHistoryPairs != 0 {
		t.Fatalf("MaxHistoryPairs should default to 0, got %d", cfg.Clarify.Fresh.MaxHistoryPairs)
	}
}

func TestClarifyConfigNewStyleOverridesLegacy(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
[agent]
clarify_model = "legacy-model"

[clarify]
model = "new-model"
`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := cfg.ClarifyModel(); got != "new-model" {
		t.Fatalf("ClarifyModel() should prefer new model, got %q", got)
	}
}

func TestClarifyConfigVersionsAndTokensDecode(t *testing.T) {
	var cfg Config
	if _, err := toml.Decode(`
[clarify.fresh]
max_versions = 4
max_tokens = 2048

[clarify.context]
max_versions = 2
max_tokens = 512
`, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg.Clarify.Fresh.MaxVersions != 4 {
		t.Fatalf("Fresh.MaxVersions = %d, want 4", cfg.Clarify.Fresh.MaxVersions)
	}
	if cfg.Clarify.Fresh.MaxTokens != 2048 {
		t.Fatalf("Fresh.MaxTokens = %d, want 2048", cfg.Clarify.Fresh.MaxTokens)
	}
	if cfg.Clarify.Context.MaxVersions != 2 {
		t.Fatalf("Context.MaxVersions = %d, want 2", cfg.Clarify.Context.MaxVersions)
	}
	if cfg.Clarify.Context.MaxTokens != 512 {
		t.Fatalf("Context.MaxTokens = %d, want 512", cfg.Clarify.Context.MaxTokens)
	}
}

func TestClarifyEffectiveVersions(t *testing.T) {
	m := ClarifyFreshMode{}
	if v := m.EffectiveVersions(); v != 3 {
		t.Fatalf("default effective versions = %d, want 3", v)
	}
	m = ClarifyFreshMode{MaxVersions: 10}
	if v := m.EffectiveVersions(); v != 5 {
		t.Fatalf("clamped effective versions = %d, want 5", v)
	}
	m = ClarifyFreshMode{MaxVersions: 0}
	if v := m.EffectiveVersions(); v != 3 {
		t.Fatalf("zero effective versions = %d, want 3", v)
	}
	m = ClarifyFreshMode{MaxVersions: 2}
	if v := m.EffectiveVersions(); v != 2 {
		t.Fatalf("configured effective versions = %d, want 2", v)
	}

	// Same for Context mode.
	cm := ClarifyContextMode{}
	if v := cm.EffectiveVersions(); v != 3 {
		t.Fatalf("context default effective versions = %d, want 3", v)
	}
}

func TestClarifyEffectiveTokens(t *testing.T) {
	m := ClarifyFreshMode{}
	if v := m.EffectiveTokens(); v != 1024 {
		t.Fatalf("default effective tokens = %d, want 1024", v)
	}
	m = ClarifyFreshMode{MaxTokens: 5000}
	if v := m.EffectiveTokens(); v != 4096 {
		t.Fatalf("clamped effective tokens = %d, want 4096", v)
	}
	m = ClarifyFreshMode{MaxTokens: 100}
	if v := m.EffectiveTokens(); v != 256 {
		t.Fatalf("floor effective tokens = %d, want 256", v)
	}
	m = ClarifyFreshMode{MaxTokens: 0}
	if v := m.EffectiveTokens(); v != 1024 {
		t.Fatalf("zero effective tokens = %d, want 1024", v)
	}
	m = ClarifyFreshMode{MaxTokens: 512}
	if v := m.EffectiveTokens(); v != 512 {
		t.Fatalf("configured effective tokens = %d, want 512", v)
	}

	cm := ClarifyContextMode{}
	if v := cm.EffectiveTokens(); v != 1024 {
		t.Fatalf("context default effective tokens = %d, want 1024", v)
	}
}
