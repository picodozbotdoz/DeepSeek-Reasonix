package agent

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/provider"
)

// PatternAnalyzer uses LLM analysis in ephemeral sessions to understand
// and describe detected patterns. It NEVER uses the main session — all
// analysis runs in isolated ephemeral sessions to preserve prefix cache.
type PatternAnalyzer struct {
	prov         provider.Provider
	promptPrefix string // system prompt for the analyzer
}

// NewPatternAnalyzer creates an analyzer that uses the given provider
// for LLM calls. All calls run in ephemeral sessions.
func NewPatternAnalyzer(prov provider.Provider) *PatternAnalyzer {
	return &PatternAnalyzer{
		prov: prov,
		promptPrefix: `You are a code pattern analyst. Analyze the given tool call pattern
and provide a concise description of what workflow it represents.
Focus on the sequence of operations and their purpose.
Be brief — 1-2 sentences max.`,
	}
}

// AnalyzePattern runs an LLM analysis of a detected pattern in an ephemeral
// session. The main session is never modified — prefix cache stays stable.
func (pa *PatternAnalyzer) AnalyzePattern(ctx context.Context, pattern *Pattern) (string, error) {
	if pattern == nil {
		return "", nil
	}
	if pa.prov == nil {
		return pattern.Description, nil
	}

	prompt := pa.buildPrompt(pattern)

	// Use EphemeralSubagentRun — isolated session, no main session pollution
	run := EphemeralSubagentRun(pa.promptPrefix)
	defer run.Release()

	sess := run.Session
	sess.Add(provider.Message{Role: provider.RoleUser, Content: prompt})

	// Stream a single completion (no tools, no agent loop)
	ch, err := pa.prov.Stream(ctx, provider.Request{
		Messages: sess.Messages,
	})
	if err != nil {
		return pattern.Description, fmt.Errorf("pattern analysis: %w", err)
	}

	var b strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			break
		}
		b.WriteString(chunk.Text)
	}

	analysis := strings.TrimSpace(b.String())
	if analysis == "" {
		return pattern.Description, nil
	}
	return analysis, nil
}

// AnalyzePatternBatch analyzes multiple patterns, each in its own ephemeral
// session. Results are returned in the same order as input.
func (pa *PatternAnalyzer) AnalyzePatternBatch(ctx context.Context, patterns []Pattern) []string {
	results := make([]string, len(patterns))
	for i := range patterns {
		analysis, err := pa.AnalyzePattern(ctx, &patterns[i])
		if err != nil {
			results[i] = patterns[i].Description
		} else {
			results[i] = analysis
		}
	}
	return results
}

// buildPrompt creates the analysis prompt for a pattern.
func (pa *PatternAnalyzer) buildPrompt(pattern *Pattern) string {
	var b strings.Builder
	b.WriteString("Analyze this tool call pattern:\n\n")
	b.WriteString("Sequence: ")
	for i, step := range pattern.Sequence {
		if i > 0 {
			b.WriteString(" → ")
		}
		b.WriteString(step)
	}
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Frequency: %d occurrences across %d sessions\n", pattern.Frequency, len(pattern.Sessions)))
	b.WriteString(fmt.Sprintf("Average duration: %dms\n", pattern.AvgDuration))
	b.WriteString(fmt.Sprintf("Confidence: %.0f%%\n\n", pattern.Confidence*100))
	b.WriteString("What workflow does this represent? Be concise (1-2 sentences).")
	return b.String()
}
