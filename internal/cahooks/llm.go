package cahooks

import (
	"context"
	"strings"

	"reasonix/internal/provider"
)

// CallAnalysis makes a standalone LLM call that reuses the exact same prefix
// as the next normal turn. The session messages are copied (not mutated), the
// analysis prompt is appended as a user message, and the response is collected.
// This achieves ~100% prefix cache hit because the prefix is byte-identical.
func CallAnalysis(
	ctx context.Context,
	prov provider.Provider,
	session []provider.Message,
	prompt string,
) (string, error) {
	// Copy session messages to avoid mutating the original
	msgs := make([]provider.Message, len(session))
	copy(msgs, session)

	// Append analysis prompt as user message
	msgs = append(msgs, provider.Message{
		Role:    provider.RoleUser,
		Content: prompt,
	})

	// Stream completion — no tools, low temperature for deterministic analysis
	ch, err := prov.Stream(ctx, provider.Request{
		Messages:    msgs,
		Tools:       nil,
		Temperature: 0.3,
	})
	if err != nil {
		return "", err
	}

	// Collect full response
	var result strings.Builder
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkText:
			result.WriteString(chunk.Text)
		case provider.ChunkReasoning:
			// Analysis calls don't need reasoning surfaced, but don't fail
		}
	}
	return result.String(), nil
}

// BuildAnalysisPrompt constructs the full analysis prompt from the hook config
// and the effective output structure.
func BuildAnalysisPrompt(prompt string, outputStructure map[string]any) string {
	var b strings.Builder
	b.WriteString(prompt)
	if outputStructure != nil {
		b.WriteString("\n\nReturn your analysis as JSON matching this structure:\n")
		b.WriteString("{\n")
		first := true
		for k, v := range outputStructure {
			if !first {
				b.WriteString(",\n")
			}
			b.WriteString("  \"")
			b.WriteString(k)
			b.WriteString("\": ")
			b.WriteString(formatJSONValue(v))
			first = false
		}
		b.WriteString("\n}")
	}
	return b.String()
}

func formatJSONValue(v any) string {
	switch val := v.(type) {
	case []any:
		if len(val) == 0 {
			return "[]"
		}
		return "[...]"
	case map[string]any:
		return "{...}"
	case string:
		return "\"" + val + "\""
	case float64:
		return "number"
	case bool:
		return "boolean"
	default:
		return "..."
	}
}
