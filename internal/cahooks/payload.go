package cahooks

import (
	"encoding/json"

	"reasonix/internal/provider"
)

// Payload is the JSON envelope passed to cahooks scripts and used for
// LLM analysis. It carries the session context and hook metadata.
type Payload struct {
	Event          string          `json:"event"`
	Cwd            string          `json:"cwd"`
	Turn           int             `json:"turn"`
	SessionID      string          `json:"session_id"`
	ContextUsage   float64         `json:"context_usage"`
	ContextWindow  int             `json:"context_window"`
	TokensUsed     int             `json:"tokens_used"`
	Messages       json.RawMessage `json:"messages"`
	ToolCalls      json.RawMessage `json:"tool_calls,omitempty"`
	ToolResults    json.RawMessage `json:"tool_results,omitempty"`
	Reasoning      string          `json:"reasoning,omitempty"`
	Errors         []string        `json:"errors,omitempty"`
	Config         *HookConfig     `json:"config,omitempty"`
	OutputDir      string          `json:"output_dir,omitempty"`
	OutputFile     string          `json:"output_file,omitempty"`
}

// BuildPayload constructs a Payload from agent state.
func BuildPayload(
	event string,
	hook *HookConfig,
	turn int,
	sessionID string,
	contextUsage float64,
	contextWindow int,
	messages []provider.Message,
	toolCalls []provider.ToolCall,
	toolResults []string,
	reasoning string,
	errors []string,
	cwd string,
) Payload {
	msgJSON, _ := json.Marshal(messages)
	tcJSON, _ := json.Marshal(toolCalls)
	trJSON, _ := json.Marshal(toolResults)

	p := Payload{
		Event:         event,
		Cwd:           cwd,
		Turn:          turn,
		SessionID:     sessionID,
		ContextUsage:  contextUsage,
		ContextWindow: contextWindow,
		TokensUsed:    int(float64(contextWindow) * contextUsage),
		Messages:      msgJSON,
		ToolCalls:     tcJSON,
		ToolResults:   trJSON,
		Reasoning:     reasoning,
		Errors:        errors,
		Config:        hook,
	}
	if hook != nil {
		p.OutputDir = hook.Output.Dir
		p.OutputFile = hook.Output.File
	}
	return p
}

// BeforeLLMCallInput is the JSON structure sent to the beforeLLMCall script.
type BeforeLLMCallInput struct {
	Payload
	Config struct {
		Prompt          string         `json:"prompt"`
		OutputStructure map[string]any `json:"output_structure"`
	} `json:"config"`
}

// BeforeLLMCallOutput is the JSON structure expected from the beforeLLMCall script.
type BeforeLLMCallOutput struct {
	UserPrompt      string         `json:"user_prompt"`
	OutputStructure map[string]any `json:"output_structure"`
}

// AfterLLMCallInput is the JSON structure sent to the afterLLMCall script.
type AfterLLMCallInput struct {
	Payload
	TmpFile  string `json:"tmp_file"`
	LLMOutput any    `json:"llm_output,omitempty"`
}
