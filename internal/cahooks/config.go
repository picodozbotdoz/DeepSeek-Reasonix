// Package cahooks implements Context Analysis Hooks — a user-configurable system
// that runs LLM analysis on the context window at various lifecycle points and
// writes insights to persistent files. The LLM call reuses the exact same prefix
// as the next normal turn, achieving ~100% cache hit. Results never enter the
// conversation history.
package cahooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Config is the top-level cahooks configuration, loaded from .reasonix/cahooks.json.
type Config struct {
	Hooks []HookConfig `json:"hooks"`
}

// HookConfig defines one context analysis hook.
type HookConfig struct {
	Name              string      `json:"name"`
	Enabled           bool        `json:"enabled"`
	Trigger           string      `json:"trigger"`
	EveryN            int         `json:"every_n,omitempty"`
	ContextThreshold  float64     `json:"context_threshold,omitempty"`
	IdleSeconds       int         `json:"idle_seconds,omitempty"`
	Condition         string      `json:"condition,omitempty"`
	BeforeLLMCall     string      `json:"before_llm_call,omitempty"`
	LLM               LLMConfig   `json:"llm"`
	AfterLLMCall      string      `json:"after_llm_call,omitempty"`
	Output            OutputConfig `json:"output"`
	Async             bool        `json:"async"`
	TimeoutMS         int         `json:"timeout_ms,omitempty"`
	InjectIntoContext bool        `json:"inject_into_context,omitempty"`
}

// LLMConfig configures the LLM analysis call.
type LLMConfig struct {
	Prompt          string         `json:"prompt"`
	OutputStructure map[string]any `json:"output_structure"`
	Model           string         `json:"model,omitempty"`
}

// OutputConfig configures where insights are written.
type OutputConfig struct {
	Dir  string `json:"dir"`
	File string `json:"file"`
}

// Valid triggers.
const (
	TriggerPostTurn       = "post_turn"
	TriggerPreCompact     = "pre_compact"
	TriggerPreNewSession  = "pre_new_session"
	TriggerEveryNTurns    = "every_n_turns"
	TriggerContextThreshold = "context_threshold"
	TriggerSessionIdle    = "session_idle"
)

// Valid conditions.
const (
	ConditionAlways       = "always"
	ConditionHasErrors    = "has_errors"
	ConditionHasToolCalls = "has_tool_calls"
	ConditionHasWrites    = "has_writes"
	ConditionHasFileChanges = "has_file_changes"
)

// Load reads cahooks configuration from the given path. Returns nil if the file
// doesn't exist or is malformed — a typo shouldn't take down the CLI.
func Load(path string) *Config {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	// Apply defaults
	for i := range cfg.Hooks {
		h := &cfg.Hooks[i]
		if h.TimeoutMS == 0 {
			h.TimeoutMS = 30000
		}
		if h.EveryN == 0 {
			h.EveryN = 5
		}
		if h.ContextThreshold == 0 {
			h.ContextThreshold = 0.7
		}
		if h.Condition == "" {
			h.Condition = ConditionAlways
		}
		if h.Output.Dir == "" {
			h.Output.Dir = ".reasonix/meta"
		}
	}
	return &cfg
}

// LoadAll loads cahooks config from project and global paths. Project overrides
// global on name collision.
func LoadAll(projectRoot, homeDir string) *Config {
	merged := &Config{}
	globalPath := filepath.Join(homeDir, ".reasonix", "cahooks.json")
	if cfg := Load(globalPath); cfg != nil {
		merged.Hooks = append(merged.Hooks, cfg.Hooks...)
	}
	if projectRoot != "" {
		projectPath := filepath.Join(projectRoot, ".reasonix", "cahooks.json")
		if cfg := Load(projectPath); cfg != nil {
			// Project hooks override global hooks with same name
			existing := make(map[string]int, len(merged.Hooks))
			for i, h := range merged.Hooks {
				existing[h.Name] = i
			}
			for _, h := range cfg.Hooks {
				if idx, ok := existing[h.Name]; ok {
					merged.Hooks[idx] = h
				} else {
					merged.Hooks = append(merged.Hooks, h)
				}
			}
		}
	}
	return merged
}

// Enabled returns only enabled hooks.
func (c *Config) Enabled() []HookConfig {
	if c == nil {
		return nil
	}
	var out []HookConfig
	for _, h := range c.Hooks {
		if h.Enabled && strings.TrimSpace(h.Name) != "" {
			out = append(out, h)
		}
	}
	return out
}

// ForTrigger returns enabled hooks matching the given trigger type.
func (c *Config) ForTrigger(trigger string) []HookConfig {
	var out []HookConfig
	for _, h := range c.Enabled() {
		if h.Trigger == trigger {
			out = append(out, h)
		}
	}
	return out
}
