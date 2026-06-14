package clarify

import (
	"context"
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
			name: "capped at 3",
			in:   "VERSION: A\nVERSION: B\nVERSION: C\nVERSION: D\nVERSION: E",
			want: 3,
		},
		{
			name: "mixed case VERSION prefix",
			in:   "VERSION: Valid\nversion: Not parsed\nVERSION: Also valid",
			want: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVersions(tt.in)
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
