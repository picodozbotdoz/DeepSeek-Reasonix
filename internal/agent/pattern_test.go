package agent

import (
	"testing"
)

func TestPatternDetectorBasic(t *testing.T) {
	pd := NewPatternDetector(2, 0.5, 5)

	records := []ToolCallRecord{
		{Name: "read_file", SessionID: "s1", TurnIndex: 0, Success: true},
		{Name: "grep", SessionID: "s1", TurnIndex: 1, Success: true},
		{Name: "read_file", SessionID: "s1", TurnIndex: 2, Success: true},

		{Name: "read_file", SessionID: "s2", TurnIndex: 0, Success: true},
		{Name: "grep", SessionID: "s2", TurnIndex: 1, Success: true},
		{Name: "read_file", SessionID: "s2", TurnIndex: 2, Success: true},

		{Name: "read_file", SessionID: "s3", TurnIndex: 0, Success: true},
		{Name: "grep", SessionID: "s3", TurnIndex: 1, Success: true},
		{Name: "read_file", SessionID: "s3", TurnIndex: 2, Success: true},
	}

	patterns := pd.DetectPatterns(records)
	if len(patterns) == 0 {
		t.Fatal("expected at least one pattern")
	}

	// Should find read_file → grep pattern
	found := false
	for _, p := range patterns {
		if len(p.Sequence) >= 2 && p.Sequence[0] == "read_file" && p.Sequence[1] == "grep" {
			found = true
			if p.Frequency < 2 {
				t.Errorf("pattern frequency = %d, want >= 2", p.Frequency)
			}
			if len(p.Sessions) < 2 {
				t.Errorf("pattern sessions = %d, want >= 2", len(p.Sessions))
			}
		}
	}
	if !found {
		t.Error("expected to find read_file → grep pattern")
	}
}

func TestPatternDetectorEmpty(t *testing.T) {
	pd := NewPatternDetector(2, 0.5, 5)
	patterns := pd.DetectPatterns(nil)
	if len(patterns) != 0 {
		t.Errorf("expected 0 patterns, got %d", len(patterns))
	}
}

func TestPatternDetectorMinFrequency(t *testing.T) {
	pd := NewPatternDetector(5, 0.5, 5) // require 5 occurrences

	records := []ToolCallRecord{
		{Name: "bash", SessionID: "s1", TurnIndex: 0},
		{Name: "read_file", SessionID: "s1", TurnIndex: 1},

		{Name: "bash", SessionID: "s2", TurnIndex: 0},
		{Name: "read_file", SessionID: "s2", TurnIndex: 1},
	}

	patterns := pd.DetectPatterns(records)
	// Only 2 occurrences, need 5 — should find nothing
	if len(patterns) != 0 {
		t.Errorf("expected 0 patterns with minFreq=5, got %d", len(patterns))
	}
}

func TestPatternDetectorConfidence(t *testing.T) {
	pd := NewPatternDetector(2, 0.8, 5) // high confidence threshold

	// Single session, low diversity — should have low confidence
	records := []ToolCallRecord{
		{Name: "bash", SessionID: "s1", TurnIndex: 0},
		{Name: "grep", SessionID: "s1", TurnIndex: 1},
		{Name: "bash", SessionID: "s1", TurnIndex: 2},
		{Name: "grep", SessionID: "s1", TurnIndex: 3},
	}

	patterns := pd.DetectPatterns(records)
	// Low session diversity should result in low confidence
	for _, p := range patterns {
		if p.Confidence >= 0.8 {
			t.Errorf("pattern confidence = %.2f, want < 0.8 for single session", p.Confidence)
		}
	}
}

func TestSkillGeneratorBasic(t *testing.T) {
	sg := NewSkillGenerator("auto")

	pattern := &Pattern{
		ID:        "pat-1",
		Sequence:  []string{"read_file", "grep", "edit_file"},
		Frequency: 5,
		Sessions:  []string{"s1", "s2", "s3"},
	}

	skill := sg.Generate(pattern)
	if skill == nil {
		t.Fatal("expected non-nil skill")
	}
	if skill.Name == "" {
		t.Error("skill name should not be empty")
	}
	if skill.Description == "" {
		t.Error("skill description should not be empty")
	}
	if skill.Body == "" {
		t.Error("skill body should not be empty")
	}
	if skill.Source != "auto-generated" {
		t.Errorf("source = %q, want auto-generated", skill.Source)
	}
}

func TestSkillGeneratorNilPattern(t *testing.T) {
	sg := NewSkillGenerator("auto")
	skill := sg.Generate(nil)
	if skill != nil {
		t.Error("expected nil for nil pattern")
	}
}

func TestSkillGeneratorFromToolSequence(t *testing.T) {
	sg := NewSkillGenerator("auto")

	skill := sg.GenerateFromToolSequence(
		"test-workflow",
		"Run tests and check coverage",
		[]string{"bash: go test ./...", "bash: go cover"},
	)

	if skill == nil {
		t.Fatal("expected non-nil skill")
	}
	if skill.Name != "test-workflow" {
		t.Errorf("name = %q, want test-workflow", skill.Name)
	}
	if skill.Body == "" {
		t.Error("body should not be empty")
	}
}

func TestSkillGeneratorEmptySequence(t *testing.T) {
	sg := NewSkillGenerator("auto")
	skill := sg.GenerateFromToolSequence("name", "desc", nil)
	if skill != nil {
		t.Error("expected nil for empty steps")
	}
}

func TestSkillGeneratorEvolve(t *testing.T) {
	sg := NewSkillGenerator("auto")

	original := &GeneratedSkill{
		Name:        "test-skill",
		Description: "Original description",
		Body:        "# test-skill\n\nOriginal body",
		Source:      "auto-generated",
	}

	feedback := SkillFeedback{
		BetterDescription: "Improved description",
		AdditionalSteps:   []string{"new step 1", "new step 2"},
	}

	evolved := sg.Evolve(original, feedback)
	if evolved == nil {
		t.Fatal("expected non-nil evolved skill")
	}
	if evolved.Description != "Improved description" {
		t.Errorf("description = %q, want 'Improved description'", evolved.Description)
	}
	if evolved.Source != "evolved" {
		t.Errorf("source = %q, want evolved", evolved.Source)
	}
	if evolved.Body == original.Body {
		t.Error("body should have changed")
	}
}

func TestSkillGeneratorEvolveNil(t *testing.T) {
	sg := NewSkillGenerator("auto")
	evolved := sg.Evolve(nil, SkillFeedback{})
	if evolved != nil {
		t.Error("expected nil for nil input")
	}
}

func TestPatternDescription(t *testing.T) {
	tests := []struct {
		seq    []string
		expect string
	}{
		{[]string{"a", "b"}, "a → b"},
		{[]string{"a", "b", "c"}, "a → ... → c (3 steps)"},
		{nil, "empty pattern"},
	}
	for _, tt := range tests {
		p := &Pattern{Sequence: tt.seq}
		desc := describePattern(p.Sequence)
		if desc != tt.expect {
			t.Errorf("describePattern(%v) = %q, want %q", tt.seq, desc, tt.expect)
		}
	}
}

func TestGeneratedSkillToFrontmatter(t *testing.T) {
	skill := &GeneratedSkill{
		Name:        "test",
		Description: "A test skill",
		Body:        "# Test\n\nBody content",
	}

	fm := skill.ToSkillFrontmatter()
	if fm == "" {
		t.Error("frontmatter should not be empty")
	}
	if !containsStr(fm, "description: A test skill") {
		t.Error("frontmatter should contain description")
	}
	if !containsStr(fm, "runAs: inline") {
		t.Error("frontmatter should contain runAs")
	}
	if !containsStr(fm, "# Test") {
		t.Error("frontmatter should contain body")
	}
}

func TestPatternDetectorSingleSession(t *testing.T) {
	pd := NewPatternDetector(1, 0.3, 5)

	records := []ToolCallRecord{
		{Name: "bash", SessionID: "s1", TurnIndex: 0},
		{Name: "read_file", SessionID: "s1", TurnIndex: 1},
		{Name: "grep", SessionID: "s1", TurnIndex: 2},
	}

	patterns := pd.DetectPatterns(records)
	if len(patterns) == 0 {
		t.Error("should detect at least one pattern from single session")
	}
}

func TestSkillGeneratorNamePrefix(t *testing.T) {
	sg := NewSkillGenerator("custom")

	pattern := &Pattern{
		Sequence: []string{"bash", "grep"},
	}

	skill := sg.Generate(pattern)
	if skill == nil {
		t.Fatal("expected non-nil skill")
	}
	if len(skill.Name) < 8 || skill.Name[:7] != "custom-" {
		t.Errorf("name = %q, want prefix 'custom-'", skill.Name)
	}
}
