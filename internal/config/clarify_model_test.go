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
