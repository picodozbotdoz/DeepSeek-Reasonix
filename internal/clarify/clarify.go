// Package clarify provides lightweight LLM-based prompt refinement for Reasonix.
// The model is called with no tool schemas — pure text generation — so the
// response never includes tool calls or complex processing. The output format
// uses VERSION:-prefixed lines for machine extraction.
package clarify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"reasonix/internal/provider"
)

// DefaultVersions is the default number of refined versions per call.
const DefaultVersions = 3

// DefaultMaxTokens is the default max output tokens for a refinement call.
const DefaultMaxTokens = 1024

// SystemPromptContextualFmt is a format string for the context-aware
// refinement system prompt. It tells the model to analyze the conversation
// history and ignore tool-calling patterns in assistant responses.
const SystemPromptContextualFmt = `You are a prompt refinement assistant analyzing a conversation between a user and a coding agent called Reasonix.

Below is the conversation history. IGNORE any tool call syntax, function calls, or structured data blocks in the assistant responses — they are internal agent operations and not relevant to the user's request. Focus only on the user's questions and the conversational context.

The user's latest draft prompt is at the end (after "Draft:"). Your job is to produce %d improved versions of this draft.

Consider:
- Making vague requests concrete
- Breaking multi-part asks into clear steps
- Incorporating relevant context from the conversation history
- Keeping the user's original intent and voice

Output exactly %d versions, each on a line starting with "VERSION:" followed by the text.
Do not include numbering, markdown, tool calls, or extra commentary — just the VERSION: lines.
Example:
VERSION: Refactor the authentication handler in internal/auth/ to use the new JWT library. Update the signing key rotation logic and add tests.
VERSION: Update the auth package to use the new JWT library: rewrite handler.go, update key rotation, and add unit tests covering the new flow.
`
// The %d placeholder is replaced with the desired number of versions.
const SystemPromptFmt = `You are a prompt refinement assistant for a coding agent called Reasonix.
The user has written a draft prompt for the agent. Your job is to produce %d improved versions.
Each version should be a clear, specific, actionable instruction that helps the agent understand
what the user wants. Consider:
- Making vague requests concrete
- Breaking multi-part asks into clear steps
- Adding relevant context (files, paths, constraints) when inferable
- Keeping the user's original intent and voice

Output exactly %d versions, each on a line starting with "VERSION:" followed by the text.
Do not include numbering, markdown, or extra commentary — just the VERSION: lines.
Example:
VERSION: Refactor the authentication handler in internal/auth/ to use the new JWT library. Update the signing key rotation logic and add tests.
VERSION: Update the auth package to use the new JWT library: rewrite handler.go, update key rotation, and add unit tests covering the new flow.
`

// SystemPrompt is the default system prompt for 3 versions. Kept for backward
// compatibility; new code should use SystemPromptFmt with the desired count.
var SystemPrompt = fmt.Sprintf(SystemPromptFmt, DefaultVersions, DefaultVersions)

// Refine calls the provider to generate prompt refinements (backward-
// compatible alias — delegates to RefineFresh with defaults).
func Refine(ctx context.Context, prov provider.Provider, input, focusHint string) ([]string, *provider.Usage, error) {
	return RefineFresh(ctx, prov, input, nil, focusHint, "", "", 0, nil, DefaultVersions, DefaultMaxTokens)
}

// buildUserMessage constructs the user message for a clarify request.
// instruction is optional guidance; toolNames are injected when non-empty;
// history is formatted as compact pairs; focusHint is appended after the draft.
func buildUserMessage(instruction string, toolNames []string, history string, input, focusHint string) string {
	var parts []string

	// Tool names, when provided, are injected before the instruction so they
	// are part of the cache-stable prefix (same tools every session).
	if len(toolNames) > 0 {
		toolLine := "Available tools: " + strings.Join(toolNames, ", ")
		if instruction != "" {
			parts = append(parts, toolLine+"\n"+instruction)
		} else {
			parts = append(parts, toolLine)
		}
	} else if instruction != "" {
		parts = append(parts, instruction)
	}

	if history != "" {
		parts = append(parts, history)
	}
	parts = append(parts, "Draft:\n"+input)
	if focusHint != "" {
		parts = append(parts, "\n\nFocus: "+focusHint)
	}
	return strings.Join(parts, "\n\n")
}

// RefineFresh is the prefix-stable clarification mode (mode 2). It builds a
// fresh provider.Request with a stable prefix — system prompt + instruction +
// optional tool names + optional history — followed by "Draft:\n" + input.
// toolNames, when non-empty, is injected into the instruction so the refiner
// can suggest tool-specific prompts. Since tool names are static per session
// they keep the prefix cacheable.
//
// Cache boundary:
//
//	[system prompt]         — fixed, cached across calls
//	[tool names]            — fixed per session, cached ("" = no-op)
//	[instruction]           — fixed, cached ("" = no-op)
//	[history(maxPairs)]     — variable content, fixed size; cache miss on content change
//	[Draft:\n + input]      — fixed prefix "Draft:\n" cached; input varies
func RefineFresh(ctx context.Context, prov provider.Provider, input string, history []provider.Message, focusHint, sysPrompt, instruction string, maxPairs int, toolNames []string, maxVersions, maxTokens int) ([]string, *provider.Usage, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil, fmt.Errorf("cannot clarify empty input")
	}

	userMsg := buildUserMessage(instruction, toolNames, formatHistory(history, maxPairs), input, focusHint)

	sys := sysPrompt
	if sys == "" {
		versions := maxVersions
		if versions <= 0 {
			versions = DefaultVersions
		}
		sys = fmt.Sprintf(SystemPromptFmt, versions, versions)
	}

	tok := maxTokens
	if tok <= 0 {
		tok = DefaultMaxTokens
	}

	req := provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: sys},
			{Role: provider.RoleUser, Content: userMsg},
		},
		Tools:       nil,
		Temperature: 0.7,
		MaxTokens:   tok,
	}

	return refineCall(ctx, prov, req, input, maxVersions)
}

// RefineContextual is the session-aware clarification mode (mode 1). It sends
// the full session messages (filtered to user/assistant, tool results omitted)
// plus the user's draft as the final message. No prefix caching, but full
// conversation context for richer refinements.
//
// sessionMsgs should already be filtered (no RoleTool messages).
// The draft is appended as the last user message with "Draft:\n" prefix.
func RefineContextual(ctx context.Context, prov provider.Provider, input string, sessionMsgs []provider.Message, focusHint, sysPrompt, instruction string, toolNames []string, maxVersions, maxTokens int) ([]string, *provider.Usage, error) {
	if strings.TrimSpace(input) == "" {
		return nil, nil, fmt.Errorf("cannot clarify empty input")
	}

	sys := sysPrompt
	if sys == "" {
		versions := maxVersions
		if versions <= 0 {
			versions = DefaultVersions
		}
		sys = fmt.Sprintf(SystemPromptContextualFmt, versions, versions)
	}

	tok := maxTokens
	if tok <= 0 {
		tok = DefaultMaxTokens
	}

	msgs := make([]provider.Message, 0, len(sessionMsgs)+2)
	msgs = append(msgs, provider.Message{Role: provider.RoleSystem, Content: sys})

	// Build instruction with tool names injected when provided.
	inst := instruction
	if len(toolNames) > 0 {
		toolLine := "Available tools: " + strings.Join(toolNames, ", ")
		if inst != "" {
			inst = toolLine + "\n" + inst
		} else {
			inst = toolLine
		}
	}
	if inst != "" {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: inst})
	}

	// Add the filtered session messages (user + assistant turns).
	// Tool calls are stripped from assistant messages — only text content
	// provides useful context; DSML/function-call syntax confuses the model.
	for _, m := range sessionMsgs {
		if m.Role == provider.RoleTool {
			continue
		}
		cleaned := m
		if cleaned.Role == provider.RoleAssistant {
			cleaned.ToolCalls = nil
		}
		msgs = append(msgs, cleaned)
	}

	// Append the draft as the final user message.
	draft := "Draft:\n" + input
	if focusHint != "" {
		draft += "\n\nFocus: " + focusHint
	}
	msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: draft})

	req := provider.Request{
		Messages:    msgs,
		Tools:       nil,
		Temperature: 0.7,
		MaxTokens:   tok,
	}

	return refineCall(ctx, prov, req, input, maxVersions)
}

// refineCall sends the request, streams the response, and parses VERSION: lines.
// It returns the versions (original + refinements), token usage, and any error.
func refineCall(ctx context.Context, prov provider.Provider, req provider.Request, originalInput string, maxVersions int) ([]string, *provider.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ch, err := prov.Stream(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("clarify: stream: %w", err)
	}

	var sb strings.Builder
	var usage *provider.Usage
loop:
	for {
		select {
		case <-ctx.Done():
			return nil, usage, fmt.Errorf("clarify: %w", ctx.Err())
		case chunk, ok := <-ch:
			if !ok {
				break loop
			}
			switch chunk.Type {
			case provider.ChunkText:
				sb.WriteString(chunk.Text)
			case provider.ChunkError:
				return nil, usage, fmt.Errorf("clarify: %w", chunk.Err)
			case provider.ChunkToolCallStart, provider.ChunkToolCall:
			case provider.ChunkUsage:
				usage = chunk.Usage
			}
		}
	}

	versions := parseVersions(sb.String(), maxVersions)
	if len(versions) == 0 {
		trimmed := strings.TrimSpace(sb.String())
		if trimmed != "" {
			return []string{originalInput, trimmed}, usage, nil
		}
		return []string{originalInput}, usage, nil
	}

	return append([]string{originalInput}, versions...), usage, nil
}

// formatHistory formats session messages into compact user/assistant pairs,
// keeping only the most recent maxPairs pairs. Tool messages are skipped.
// Returns "" when maxPairs is 0 or there are no user/assistant messages.
func formatHistory(msgs []provider.Message, maxPairs int) string {
	if maxPairs <= 0 || len(msgs) == 0 {
		return ""
	}

	// Collect user/assistant pairs from the end.
	var pairs []string
	userMsgs := make([]string, 0, maxPairs)
	assistantMsgs := make([]string, 0, maxPairs)
	for i := len(msgs) - 1; i >= 0 && len(pairs) < maxPairs; i-- {
		m := msgs[i]
		switch m.Role {
		case provider.RoleAssistant:
			text := strings.TrimSpace(m.Content)
			if text != "" {
				assistantMsgs = append(assistantMsgs, text)
			}
		case provider.RoleUser:
			text := strings.TrimSpace(m.Content)
			if text != "" {
				userMsgs = append(userMsgs, text)
				// When we have a user message, try to pair it with an assistant.
				if len(assistantMsgs) > 0 {
					pair := "user: " + userMsgs[len(userMsgs)-1] + "\nassistant: " + assistantMsgs[len(assistantMsgs)-1]
					pairs = append(pairs, pair)
					assistantMsgs = assistantMsgs[:len(assistantMsgs)-1]
					userMsgs = userMsgs[:len(userMsgs)-1]
				}
			}
		}
	}

	if len(pairs) == 0 {
		return ""
	}

	// Reverse pairs to chronological order.
	for i, j := 0, len(pairs)-1; i < j; i, j = i+1, j-1 {
		pairs[i], pairs[j] = pairs[j], pairs[i]
	}

	return "Recent conversation:\n" + strings.Join(pairs, "\n")
}

// parseVersions extracts VERSION:-prefixed lines from the model output.
// max caps the number of versions returned; 0 or negative means use DefaultVersions.
func parseVersions(text string, max int) []string {
	cap := DefaultVersions
	if max > 0 {
		cap = max
	}

	var versions []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "VERSION:"); ok {
			v := strings.TrimSpace(after)
			if v != "" {
				versions = append(versions, v)
				if len(versions) >= cap {
					break
				}
			}
		}
	}
	return versions
}
