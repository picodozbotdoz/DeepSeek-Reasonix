package clarify

import (
	"context"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

func TestParseVersions(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int // expected number of versions
	}{
		{
			name: "two versions",
			in:   "VERSION: First refined prompt\nVERSION: Second refined prompt",
			want: 2,
		},
		{
			name: "three versions",
			in:   "VERSION: Version one\nVERSION: Version two\nVERSION: Version three",
			want: 3,
		},
		{
			name: "no versions",
			in:   "Just some random text without version markers",
			want: 0,
		},
		{
			name: "empty input",
			in:   "",
			want: 0,
		},
		{
			name: "with extra whitespace",
			in:   "  VERSION:  Trimmed leading space\n  VERSION:   Another one  ",
			want: 2,
		},
		{
			name: "extra commentary before versions",
			in:   "Here are some refinements:\n\nVERSION: First option\nVERSION: Second option",
			want: 2,
		},
		{
			name: "extra commentary after versions",
			in:   "VERSION: First option\nVERSION: Second option\n\nChoose whichever works best.",
			want: 2,
		},
		{
			name: "empty version lines skipped",
			in:   "VERSION: \nVERSION: Valid text\nVERSION:   ",
			want: 1,
		},
		{
			name: "capped at 5",
			in:   "VERSION: A\nVERSION: B\nVERSION: C\nVERSION: D\nVERSION: E",
			want: 5,
		},
		{
			name: "mixed case VERSION prefix",
			in:   "VERSION: Valid\nversion: Not parsed\nVERSION: Also valid",
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVersions(tt.in, 5)
			if len(got) != tt.want {
				t.Errorf("parseVersions() returned %d versions, want %d\n  result: %v", len(got), tt.want, got)
			}
		})
	}
}

// mockProvider streams a single pre-built response.
type mockProvider struct {
	text string
	err  error
	name string
}

func (m *mockProvider) Name() string { return m.name }

func (m *mockProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	ch := make(chan provider.Chunk, 10)
	go func() {
		defer close(ch)
		if m.err != nil {
			ch <- provider.Chunk{Type: provider.ChunkError, Err: m.err}
			return
		}
		ch <- provider.Chunk{Type: provider.ChunkText, Text: m.text}
		ch <- provider.Chunk{Type: provider.ChunkUsage, Usage: &provider.Usage{}}
		ch <- provider.Chunk{Type: provider.ChunkDone}
	}()
	return ch, nil
}

func TestRefine(t *testing.T) {
	t.Run("returns original plus parsed versions", func(t *testing.T) {
		prov := &mockProvider{
			text: "VERSION: First refinement\nVERSION: Second refinement",
		}
		results, err := Refine(context.Background(), prov, "my original prompt", "")
		if err != nil {
			t.Fatalf("Refine() error: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results (original + 2 refinements), got %d: %v", len(results), results)
		}
		if results[0] != "my original prompt" {
			t.Errorf("results[0] = %q, want %q", results[0], "my original prompt")
		}
	})

	t.Run("fallback when no VERSION lines", func(t *testing.T) {
		prov := &mockProvider{
			text: "Here's a suggestion: try rewriting it like this...",
		}
		results, err := Refine(context.Background(), prov, "original", "")
		if err != nil {
			t.Fatalf("Refine() error: %v", err)
		}
		// Should return original + full response text as fallback
		if len(results) != 2 {
			t.Fatalf("expected 2 results (original + fallback), got %d: %v", len(results), results)
		}
		if results[0] != "original" {
			t.Errorf("results[0] = %q, want %q", results[0], "original")
		}
	})

	t.Run("empty input fails", func(t *testing.T) {
		_, err := Refine(context.Background(), &mockProvider{}, "", "")
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("provider error", func(t *testing.T) {
		prov := &mockProvider{err: context.DeadlineExceeded}
		_, err := Refine(context.Background(), prov, "some text", "")
		if err == nil {
			t.Fatal("expected error from provider")
		}
	})

	t.Run("timeout via context", func(t *testing.T) {
		// A provider that never sends should time out via ctx.Done().
		ch := make(chan provider.Chunk) // unbuffered, never closed — blocks forever
		prov := &streamProvider{ch: ch}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
		defer cancel()
		_, err := Refine(ctx, prov, "some text", "")
		if err == nil {
			t.Fatal("expected timeout error")
		}
	})

	t.Run("with focus hint", func(t *testing.T) {
		prov := &mockProvider{
			text: "VERSION: First\nVERSION: Second",
		}
		results, err := Refine(context.Background(), prov, "original", "make it more concise")
		if err != nil {
			t.Fatalf("Refine() with hint error: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		if results[0] != "original" {
			t.Errorf("results[0] = %q, want %q", results[0], "original")
		}
	})

	t.Run("tool calls are ignored", func(t *testing.T) {
		ch := make(chan provider.Chunk, 10)
		go func() {
			defer close(ch)
			// Send a tool call chunk that should be ignored
			ch <- provider.Chunk{
				Type:    provider.ChunkToolCall,
				ToolCall: &provider.ToolCall{ID: "call_1", Name: "bash"},
			}
			// Then text
			ch <- provider.Chunk{Type: provider.ChunkText, Text: "VERSION: Still works"}
			ch <- provider.Chunk{Type: provider.ChunkDone}
		}()
		prov := &streamProvider{ch: ch}
		results, err := Refine(context.Background(), prov, "original", "")
		if err != nil {
			t.Fatalf("Refine() error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d: %v", len(results), results)
		}
	})
}

// streamProvider lets us inject a pre-built channel.
type streamProvider struct {
	ch chan provider.Chunk
}

func (s *streamProvider) Name() string { return "test" }

func (s *streamProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Chunk, error) {
	return s.ch, nil
}

func TestRefineFresh(t *testing.T) {
	t.Run("basic no history", func(t *testing.T) {
		prov := &mockProvider{text: "VERSION: First\nVERSION: Second"}
		results, err := RefineFresh(context.Background(), prov, "my draft", nil, "", "", "", 0, nil, 3, 1024)
		if err != nil {
			t.Fatalf("RefineFresh error: %v", err)
		}
		if len(results) != 3 {
			t.Fatalf("expected 3 results, got %d", len(results))
		}
		if results[0] != "my draft" {
			t.Errorf("results[0] = %q, want %q", results[0], "my draft")
		}
	})

	t.Run("with custom system prompt", func(t *testing.T) {
		prov := &mockProvider{text: "VERSION: A"}
		results, err := RefineFresh(context.Background(), prov, "test", nil, "", "Custom sys prompt", "", 0, nil, 3, 1024)
		if err != nil {
			t.Fatalf("RefineFresh error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("with history", func(t *testing.T) {
		prov := &mockProvider{text: "VERSION: Refined"}
		history := []provider.Message{
			{Role: provider.RoleUser, Content: "first question"},
			{Role: provider.RoleAssistant, Content: "first answer"},
		}
		results, err := RefineFresh(context.Background(), prov, "new draft", history, "", "", "", 1, nil, 3, 1024)
		if err != nil {
			t.Fatalf("RefineFresh with history error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("focus hint after draft", func(t *testing.T) {
		prov := &mockProvider{text: "VERSION: Focused"}
		results, err := RefineFresh(context.Background(), prov, "my draft", nil, "be more specific", "", "", 0, nil, 3, 1024)
		if err != nil {
			t.Fatalf("RefineFresh with focus hint error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("empty input fails", func(t *testing.T) {
		_, err := RefineFresh(context.Background(), &mockProvider{}, "", nil, "", "", "", 0, nil, 3, 1024)
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})
}

func TestRefineContextual(t *testing.T) {
	t.Run("with session messages", func(t *testing.T) {
		prov := &mockProvider{text: "VERSION: Contextual refinement"}
		session := []provider.Message{
			{Role: provider.RoleUser, Content: "help me debug"},
			{Role: provider.RoleAssistant, Content: "sure, show me the code"},
			{Role: provider.RoleUser, Content: "here it is: func main() {}"},
			{Role: provider.RoleAssistant, Content: "I see the issue"},
		}
		results, err := RefineContextual(context.Background(), prov, "fix the bug", session, "", "", "", nil, 3, 1024)
		if err != nil {
			t.Fatalf("RefineContextual error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
		if results[0] != "fix the bug" {
			t.Errorf("results[0] = %q, want %q", results[0], "fix the bug")
		}
	})

	t.Run("tool messages filtered", func(t *testing.T) {
		prov := &mockProvider{text: "VERSION: Clean"}
		session := []provider.Message{
			{Role: provider.RoleUser, Content: "hello"},
			{Role: provider.RoleAssistant, Content: "hi"},
			{Role: provider.RoleTool, Content: "some tool output"},
		}
		results, err := RefineContextual(context.Background(), prov, "test", session, "", "", "", nil, 3, 1024)
		if err != nil {
			t.Fatalf("RefineContextual error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("empty input fails", func(t *testing.T) {
		_, err := RefineContextual(context.Background(), &mockProvider{}, "", nil, "", "", "", nil, 3, 1024)
		if err == nil {
			t.Fatal("expected error for empty input")
		}
	})

	t.Run("with custom system prompt and instruction", func(t *testing.T) {
		prov := &mockProvider{text: "VERSION: Custom"}
		session := []provider.Message{
			{Role: provider.RoleUser, Content: "earlier message"},
			{Role: provider.RoleAssistant, Content: "earlier response"},
		}
		results, err := RefineContextual(context.Background(), prov, "final draft", session, "", "Custom sys", "Use the context", nil, 3, 1024)
		if err != nil {
			t.Fatalf("RefineContextual error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})
}

func TestFormatHistory(t *testing.T) {
	t.Run("zero pairs returns empty", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: provider.RoleUser, Content: "hi"},
			{Role: provider.RoleAssistant, Content: "hello"},
		}
		if got := formatHistory(msgs, 0); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})

	t.Run("single pair", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: provider.RoleUser, Content: "user msg"},
			{Role: provider.RoleAssistant, Content: "assistant reply"},
		}
		got := formatHistory(msgs, 1)
		if !strings.Contains(got, "user: user msg") {
			t.Fatalf("missing user message in:\n%s", got)
		}
		if !strings.Contains(got, "assistant: assistant reply") {
			t.Fatalf("missing assistant message in:\n%s", got)
		}
	})

	t.Run("caps at maxPairs", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: provider.RoleUser, Content: "first"},
			{Role: provider.RoleAssistant, Content: "reply1"},
			{Role: provider.RoleUser, Content: "second"},
			{Role: provider.RoleAssistant, Content: "reply2"},
			{Role: provider.RoleUser, Content: "third"},
			{Role: provider.RoleAssistant, Content: "reply3"},
		}
		got := formatHistory(msgs, 2)
		if strings.Contains(got, "first") {
			t.Fatalf("should not contain oldest pair:\n%s", got)
		}
		if !strings.Contains(got, "second") || !strings.Contains(got, "third") {
			t.Fatalf("should contain most recent pairs:\n%s", got)
		}
	})

	t.Run("skips tool messages", func(t *testing.T) {
		msgs := []provider.Message{
			{Role: provider.RoleUser, Content: "user"},
			{Role: provider.RoleTool, Content: "tool output"},
			{Role: provider.RoleAssistant, Content: "assistant"},
		}
		got := formatHistory(msgs, 1)
		if !strings.Contains(got, "user: user") {
			t.Fatalf("missing user message:\n%s", got)
		}
		if !strings.Contains(got, "assistant: assistant") {
			t.Fatalf("missing assistant message:\n%s", got)
		}
		if strings.Contains(got, "tool output") {
			t.Fatal("should not contain tool output")
		}
	})

	t.Run("empty messages", func(t *testing.T) {
		if got := formatHistory(nil, 5); got != "" {
			t.Fatalf("expected empty, got %q", got)
		}
	})
}
