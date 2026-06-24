package agent

import (
	"fmt"
	"sort"
	"strings"
)

// ToolCallRecord is a single tool invocation with its context.
type ToolCallRecord struct {
	Name      string            // tool name
	Args      map[string]string // simplified args (key params only)
	SessionID string
	TurnIndex int
	Success   bool
	Duration  int64 // milliseconds
}

// Pattern is a repeated sequence of tool calls detected across sessions.
type Pattern struct {
	ID          string           // unique identifier
	Sequence    []string         // tool names in order
	Frequency   int              // how many times this pattern occurred
	Sessions    []string         // session IDs where it appeared
	AvgDuration int64            // average duration in ms
	Description string           // human-readable description
	Confidence  float64          // 0.0-1.0 confidence score
}

// PatternDetector analyzes tool call sequences to find repeated patterns.
type PatternDetector struct {
	minFrequency int     // minimum occurrences to consider a pattern
	minConfidence float64 // minimum confidence score
	maxSequence   int     // maximum sequence length to consider
}

// NewPatternDetector creates a detector with the given thresholds.
func NewPatternDetector(minFreq int, minConfidence float64, maxSeq int) *PatternDetector {
	if minFreq <= 0 {
		minFreq = 3
	}
	if minConfidence <= 0 {
		minConfidence = 0.6
	}
	if maxSeq <= 0 {
		maxSeq = 10
	}
	return &PatternDetector{
		minFrequency:  minFreq,
		minConfidence: minConfidence,
		maxSequence:   maxSeq,
	}
}

// DetectPatterns analyzes a list of tool call records and returns detected patterns.
func (pd *PatternDetector) DetectPatterns(records []ToolCallRecord) []Pattern {
	if len(records) == 0 {
		return nil
	}

	// Group records by session
	bySession := map[string][]ToolCallRecord{}
	for _, r := range records {
		bySession[r.SessionID] = append(bySession[r.SessionID], r)
	}

	// Extract all subsequences of length 2..maxSequence
	sequenceCounts := map[string]*patternAccumulator{}
	for _, sessionRecords := range bySession {
		sorted := sortRecords(sessionRecords)
		for seqLen := 2; seqLen <= pd.maxSequence && seqLen <= len(sorted); seqLen++ {
			for i := 0; i <= len(sorted)-seqLen; i++ {
				subseq := sorted[i : i+seqLen]
				key := sequenceKey(subseq)
				if _, exists := sequenceCounts[key]; !exists {
					sequenceCounts[key] = &patternAccumulator{
						sequence:  toolNames(subseq),
						sessions:  map[string]bool{},
						durations: []int64{},
					}
				}
				acc := sequenceCounts[key]
				acc.sessions[sessionRecords[0].SessionID] = true
				acc.frequency++
				acc.durations = append(acc.durations, avgDuration(subseq))
			}
		}
	}

	// Filter by frequency and compute confidence
	var patterns []Pattern
	id := 0
	for _, acc := range sequenceCounts {
		if acc.frequency < pd.minFrequency {
			continue
		}
		confidence := pd.computeConfidence(acc)
		if confidence < pd.minConfidence {
			continue
		}

		sessions := make([]string, 0, len(acc.sessions))
		for s := range acc.sessions {
			sessions = append(sessions, s)
		}

		id++
		patterns = append(patterns, Pattern{
			ID:          fmt.Sprintf("pat-%d", id),
			Sequence:    acc.sequence,
			Frequency:   acc.frequency,
			Sessions:    sessions,
			AvgDuration: avgInt64(acc.durations),
			Description: describePattern(acc.sequence),
			Confidence:  confidence,
		})
	}

	// Sort by frequency descending
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Frequency > patterns[j].Frequency
	})

	return patterns
}

// computeConfidence calculates a confidence score for a pattern.
// Higher frequency, more sessions, and shorter sequences increase confidence.
func (pd *PatternDetector) computeConfidence(acc *patternAccumulator) float64 {
	// Frequency score: log-scaled, maxes out around 10 occurrences
	freqScore := float64(acc.frequency) / float64(pd.minFrequency+5)
	if freqScore > 1.0 {
		freqScore = 1.0
	}

	// Session diversity: patterns seen in more sessions are more reliable
	sessionScore := float64(len(acc.sessions)) / 5.0
	if sessionScore > 1.0 {
		sessionScore = 1.0
	}

	// Sequence length penalty: shorter patterns are more likely to be real
	lengthPenalty := 1.0 - float64(len(acc.sequence)-2)*0.1
	if lengthPenalty < 0.5 {
		lengthPenalty = 0.5
	}

	return (freqScore*0.5 + sessionScore*0.3 + lengthPenalty*0.2)
}

// patternAccumulator holds intermediate data during pattern detection.
type patternAccumulator struct {
	sequence  []string
	frequency int
	sessions  map[string]bool
	durations []int64
}

func sequenceKey(records []ToolCallRecord) string {
	names := make([]string, len(records))
	for i, r := range records {
		names[i] = r.Name
	}
	return strings.Join(names, "->")
}

func toolNames(records []ToolCallRecord) []string {
	names := make([]string, len(records))
	for i, r := range records {
		names[i] = r.Name
	}
	return names
}

func sortRecords(records []ToolCallRecord) []ToolCallRecord {
	sorted := make([]ToolCallRecord, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].TurnIndex != sorted[j].TurnIndex {
			return sorted[i].TurnIndex < sorted[j].TurnIndex
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

func avgDuration(records []ToolCallRecord) int64 {
	if len(records) == 0 {
		return 0
	}
	var total int64
	for _, r := range records {
		total += r.Duration
	}
	return total / int64(len(records))
}

func avgInt64(vals []int64) int64 {
	if len(vals) == 0 {
		return 0
	}
	var total int64
	for _, v := range vals {
		total += v
	}
	return total / int64(len(vals))
}

func describePattern(sequence []string) string {
	if len(sequence) == 0 {
		return "empty pattern"
	}
	if len(sequence) == 2 {
		return fmt.Sprintf("%s → %s", sequence[0], sequence[1])
	}
	return fmt.Sprintf("%s → ... → %s (%d steps)", sequence[0], sequence[len(sequence)-1], len(sequence))
}
