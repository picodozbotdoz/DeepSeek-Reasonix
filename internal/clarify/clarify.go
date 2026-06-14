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

// SystemPrompt instructs the model to produce 2–3 refined prompt versions.
// Used as the default when no custom system prompt is configured.
const SystemPrompt = `You are a prompt refinement assistant for a coding agent called Reasonix.
The user has written a draft prompt for the agent. Your job is to produce 2–3 improved versions.
Each version should be a clear, specific, actionable instruction that helps the agent understand
what the user wants. Consider:
- Making vague requests concrete
- Breaking multi-part asks into clear steps
- Adding relevant context (files, paths, constraints) when inferable
- Keeping the user's original intent and voice

Output exactly 2–3 versions, each on a line starting with "VERSION:" followed by the text.
Do not include numbering, markdown, or extra commentary — just the VERSION: lines.
Example:
VERSION: Refactor the authentication handler in internal/auth/ to use the new JWT library. Update the signing key rotation logic and add tests.
VERSION: Update the auth package to use the new JWT library: rewrite handler.go, update key rotation, and add unit tests covering the new flow.
`

// Refine calls the provider to generate 2–3 prompt refinements (backward-
// compatible alias — delegates to RefineFresh with no history).
// input is the user's raw prompt text.
// Returns the original + refined versions (at least 1 — original is always first).
func Refine(ctx context.Context, prov provider.Provider, input, focusHint string) ([]string, error) {
	return RefineFresh(ctx, prov, input, nil, focusHint, SystemPrompt, "", 0)
}

// RefineFresh is the prefix-stable clarification mode (mode 2). It builds a
// fresh provider.Request with a stable prefix — system prompt + instruction +
// optional history — followed by "Draft:\n" + input. The focus hint, when set,
// is appended after the draft so the prefix stays cacheable.
//
// Cache boundary:
//
//	[system prompt]         — fixed, cached across calls
//	[instruction]           — fixed, cached ("" = no-op)
//	[history(maxPairs)]     — variable content, fixed size; cache miss on content change
//	[Draft:\n + input]      — fixed prefix "Draft:\n" cached; input varies
//
// When maxPairs is 0 and instruction is "", the behaviour is identical to the
// original Refine().
func RefineFresh(ctx context.Context, prov provider.Provider, input string, history []provider.Message, focusHint, sysPrompt, instruction string, maxPairs int) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, fmt.Errorf("cannot clarify empty input")
	}

	// Build the user message with a stable cache boundary.
	var userParts []string
	if instruction != "" {
		userParts = append(userParts, instruction)
	}
	if hist := formatHistory(history, maxPairs); hist != "" {
		userParts = append(userParts, hist)
	}
	userParts = append(userParts, "Draft:\n"+input)
	if focusHint != "" {
		userParts = append(userParts, "\n\nFocus: "+focusHint)
	}
	userMsg := strings.Join(userParts, "\n\n")

	// Use the default system prompt when none is configured.
	sys := sysPrompt
	if sys == "" {
		sys = SystemPrompt
	}

	req := provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: sys},
			{Role: provider.RoleUser, Content: userMsg},
		},
		Tools:       nil, // NO tools — pure text generation
		Temperature: 0.7, // slight creativity for diverse versions
		MaxTokens:   1024,
	}

	return refineCall(ctx, prov, req, input)
}

// RefineContextual is the session-aware clarification mode (mode 1). It sends
// the full session messages (filtered to user/assistant, tool results omitted)
// plus the user's draft as the final message. No prefix caching, but full
// conversation context for richer refinements.
//
// sessionMsgs should already be filtered (no RoleTool messages).
// The draft is appended as the last user message with "Draft:\n" prefix.
func RefineContextual(ctx context.Context, prov provider.Provider, input string, sessionMsgs []provider.Message, focusHint, sysPrompt, instruction string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, fmt.Errorf("cannot clarify empty input")
	}

	sys := sysPrompt
	if sys == "" {
		sys = SystemPrompt
	}

	msgs := make([]provider.Message, 0, len(sessionMsgs)+2)
	msgs = append(msgs, provider.Message{Role: provider.RoleSystem, Content: sys})

	// Add instruction as a user message if set (guidance, not a draft).
	if instruction != "" {
		msgs = append(msgs, provider.Message{Role: provider.RoleUser, Content: instruction})
	}

	// Add the filtered session messages (user + assistant turns).
	for _, m := range sessionMsgs {
		if m.Role == provider.RoleTool {
			continue
		}
		msgs = append(msgs, m)
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
		MaxTokens:   1024,
	}

	return refineCall(ctx, prov, req, input)
}

// refineCall sends the request, streams the response, and parses VERSION: lines.
func refineCall(ctx context.Context, prov provider.Provider, req provider.Request, originalInput string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ch, err := prov.Stream(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("clarify: stream: %w", err)
	}

	var sb strings.Builder
loop:
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("clarify: %w", ctx.Err())
		case chunk, ok := <-ch:
			if !ok {
				break loop
			}
			switch chunk.Type {
			case provider.ChunkText:
				sb.WriteString(chunk.Text)
			case provider.ChunkError:
				return nil, fmt.Errorf("clarify: %w", chunk.Err)
			case provider.ChunkToolCallStart, provider.ChunkToolCall:
				// Ignore — Tools: nil means the model shouldn't produce these.
			}
		}
	}

	versions := parseVersions(sb.String())
	if len(versions) == 0 {
		trimmed := strings.TrimSpace(sb.String())
		if trimmed != "" {
			return []string{originalInput, trimmed}, nil
		}
		return []string{originalInput}, nil
	}

	return append([]string{originalInput}, versions...), nil
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
func parseVersions(text string) []string {
	var versions []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "VERSION:"); ok {
			v := strings.TrimSpace(after)
			if v != "" {
				versions = append(versions, v)
			}
		}
	}
	if len(versions) > 3 {
		versions = versions[:3]
	}
	return versions
}
