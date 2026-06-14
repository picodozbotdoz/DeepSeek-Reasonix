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

// Refine calls the provider to generate 2–3 prompt refinements.
// input is the user's raw prompt text.
// Returns the original + refined versions (at least 1 — original is always first).
func Refine(ctx context.Context, prov provider.Provider, input, focusHint string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, fmt.Errorf("cannot clarify empty input")
	}

	userMsg := "Draft:\n" + input
	if focusHint != "" {
		userMsg = fmt.Sprintf("Focus: %s\n\n---\n\nDraft:\n%s", focusHint, input)
	}

	req := provider.Request{
		Messages: []provider.Message{
			{Role: provider.RoleSystem, Content: SystemPrompt},
			{Role: provider.RoleUser, Content: userMsg},
		},
		Tools:       nil, // NO tools — pure text generation
		Temperature: 0.7, // slight creativity for diverse versions
		MaxTokens:   1024,
	}

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
				// Ignore any tool-related chunks — the model should not be
				// producing them since we passed Tools: nil, but handle gracefully.
			}
		}
	}

	versions := parseVersions(sb.String())
	if len(versions) == 0 {
		// Fallback: return the whole response as one suggestion
		trimmed := strings.TrimSpace(sb.String())
		if trimmed != "" {
			return []string{input, trimmed}, nil
		}
		return []string{input}, nil
	}

	// Prepend the original as the first option
	return append([]string{input}, versions...), nil
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
