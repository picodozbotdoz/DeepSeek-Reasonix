package agent

import (
	"fmt"
	"strings"
)

// GeneratedSkill is a skill template produced from a detected pattern.
type GeneratedSkill struct {
	Name        string
	Description string
	Body        string
	Pattern     *Pattern
	Source      string // "auto-generated" or "evolved"
}

// SkillGenerator converts detected patterns into skill templates.
type SkillGenerator struct {
	namePrefix string // prefix for generated skill names
}

// NewSkillGenerator creates a generator with the given name prefix.
func NewSkillGenerator(prefix string) *SkillGenerator {
	if prefix == "" {
		prefix = "auto"
	}
	return &SkillGenerator{namePrefix: prefix}
}

// Generate creates a skill template from a detected pattern.
func (sg *SkillGenerator) Generate(pattern *Pattern) *GeneratedSkill {
	if pattern == nil || len(pattern.Sequence) == 0 {
		return nil
	}

	name := sg.skillName(pattern)
	description := sg.skillDescription(pattern)
	body := sg.skillBody(pattern)

	return &GeneratedSkill{
		Name:        name,
		Description: description,
		Body:        body,
		Pattern:     pattern,
		Source:      "auto-generated",
	}
}

// GenerateFromToolSequence creates a skill from an explicit tool sequence
// (not from pattern detection).
func (sg *SkillGenerator) GenerateFromToolSequence(name string, description string, steps []string) *GeneratedSkill {
	if name == "" || len(steps) == 0 {
		return nil
	}

	var body strings.Builder
	body.WriteString(fmt.Sprintf("# %s\n\n", name))
	body.WriteString(fmt.Sprintf("> %s\n\n", description))
	body.WriteString("## Steps\n\n")
	for i, step := range steps {
		body.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
	}
	body.WriteString("\n## Execution\n\n")
	body.WriteString("Follow these steps in order. Each step may involve tool calls.\n")
	body.WriteString("Verify completion before moving to the next step.\n")

	return &GeneratedSkill{
		Name:        name,
		Description: description,
		Body:        body.String(),
		Source:      "auto-generated",
	}
}

// Evolve improves an existing skill based on usage data and feedback.
func (sg *SkillGenerator) Evolve(existing *GeneratedSkill, feedback SkillFeedback) *GeneratedSkill {
	if existing == nil {
		return nil
	}

	// Clone the existing skill
	evolved := &GeneratedSkill{
		Name:        existing.Name,
		Description: existing.Description,
		Body:        existing.Body,
		Pattern:     existing.Pattern,
		Source:      "evolved",
	}

	// Apply feedback
	if feedback.BetterDescription != "" {
		evolved.Description = feedback.BetterDescription
	}

	if feedback.AdditionalSteps != nil {
		// Append new steps to the body
		var updated strings.Builder
		updated.WriteString(evolved.Body)
		updated.WriteString("\n## Additional Steps\n\n")
		for i, step := range feedback.AdditionalSteps {
			updated.WriteString(fmt.Sprintf("%d. %s\n", i+1, step))
		}
		evolved.Body = updated.String()
	}

	if feedback.RemovedSteps != nil {
		// Remove specified steps from the body
		lines := strings.Split(evolved.Body, "\n")
		var filtered []string
		removedSet := map[int]bool{}
		for _, s := range feedback.RemovedSteps {
			removedSet[s] = true
		}
		for i, line := range lines {
			if removedSet[i] {
				continue
			}
			filtered = append(filtered, line)
		}
		evolved.Body = strings.Join(filtered, "\n")
	}

	return evolved
}

// SkillFeedback contains human or automated feedback for skill evolution.
type SkillFeedback struct {
	BetterDescription string
	AdditionalSteps   []string
	RemovedSteps      []int // 0-based line indices to remove
	Rating            int   // 1-5 rating
	Notes             string
}

// skillName generates a canonical skill name from a pattern.
func (sg *SkillGenerator) skillName(pattern *Pattern) string {
	// Use first two tools in the sequence
	if len(pattern.Sequence) >= 2 {
		return fmt.Sprintf("%s-%s-%s", sg.namePrefix, pattern.Sequence[0], pattern.Sequence[1])
	}
	return fmt.Sprintf("%s-%s", sg.namePrefix, pattern.Sequence[0])
}

// skillDescription generates a human-readable description.
func (sg *SkillGenerator) skillDescription(pattern *Pattern) string {
	if len(pattern.Sequence) == 0 {
		return "Auto-generated skill"
	}
	if len(pattern.Sequence) == 2 {
		return fmt.Sprintf("Automated workflow: %s then %s (detected %d times across %d sessions)",
			pattern.Sequence[0], pattern.Sequence[1],
			pattern.Frequency, len(pattern.Sessions))
	}
	return fmt.Sprintf("Automated workflow: %s → ... → %s (%d steps, detected %d times)",
		pattern.Sequence[0], pattern.Sequence[len(pattern.Sequence)-1],
		len(pattern.Sequence), pattern.Frequency)
}

// skillBody generates the full markdown body for the skill.
func (sg *SkillGenerator) skillBody(pattern *Pattern) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# %s\n\n", sg.skillName(pattern)))
	b.WriteString(fmt.Sprintf("> %s\n\n", sg.skillDescription(pattern)))

	b.WriteString("## Detected Pattern\n\n")
	b.WriteString("This skill was automatically generated from repeated tool call sequences.\n\n")
	b.WriteString("```")
	for i, step := range pattern.Sequence {
		if i > 0 {
			b.WriteString(" → ")
		}
		b.WriteString(step)
	}
	b.WriteString("```\n\n")

	b.WriteString(fmt.Sprintf("- **Frequency:** %d occurrences\n", pattern.Frequency))
	b.WriteString(fmt.Sprintf("- **Sessions:** %d unique sessions\n", len(pattern.Sessions)))
	b.WriteString(fmt.Sprintf("- **Avg duration:** %dms\n", pattern.AvgDuration))
	b.WriteString(fmt.Sprintf("- **Confidence:** %.0f%%\n\n", pattern.Confidence*100))

	b.WriteString("## Steps\n\n")
	for i, step := range pattern.Sequence {
		b.WriteString(fmt.Sprintf("%d. Use the `%s` tool\n", i+1, step))
	}
	b.WriteString("\n## Execution\n\n")
	b.WriteString("Follow these steps in order. Each step builds on the previous.\n")
	b.WriteString("Verify the result of each step before proceeding.\n")

	return b.String()
}

// ToSkillFrontmatter returns the skill as a markdown file with frontmatter.
func (gs *GeneratedSkill) ToSkillFrontmatter() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("description: %s\n", gs.Description))
	b.WriteString("runAs: inline\n")
	b.WriteString("source: auto-generated\n")
	b.WriteString("---\n\n")
	b.WriteString(gs.Body)
	return b.String()
}
