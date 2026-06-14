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

[clarify.context]
enabled = false
system_prompt = "Custom context prompt"
instruction = "Use full context"
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
	if cfg.Clarify.Context.Enabled {
		t.Fatal("Clarify.Context.Enabled should be false")
	}
	if cfg.Clarify.Context.SystemPrompt != "Custom context prompt" {
		t.Fatalf("Clarify.Context.SystemPrompt = %q", cfg.Clarify.Context.SystemPrompt)
	}
	if cfg.Clarify.Context.Instruction != "Use full context" {
		t.Fatalf("Clarify.Context.Instruction = %q", cfg.Clarify.Context.Instruction)
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
