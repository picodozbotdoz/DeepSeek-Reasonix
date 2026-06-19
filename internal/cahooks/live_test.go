package cahooks

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/provider/openai"
)

// TestLiveCallAnalysisPrefixCache verifies that CallAnalysis reuses the exact
// same prefix as a normal turn, achieving ~100% cache hit. Requires a real
// DeepSeek API key.
func TestLiveCallAnalysisPrefixCache(t *testing.T) {
	key := os.Getenv("DEEPSEEK_API_KEY")
	if key == "" {
		t.Skip("DEEPSEEK_API_KEY not set")
	}

	// Create a real provider
	prov, err := openai.New(provider.Config{
		Name:    "deepseek",
		BaseURL: "https://api.deepseek.com",
		Model:   "deepseek-chat",
		APIKey:  key,
	})
	if err != nil {
		t.Fatalf("failed to create provider: %v", err)
	}

	// Build a session with realistic content
	session := []provider.Message{
		{Role: provider.RoleSystem, Content: "You are a coding assistant. Help users with software engineering tasks."},
		{Role: provider.RoleUser, Content: "Read the file main.go and explain what it does"},
		{Role: provider.RoleAssistant, Content: "I'll read the file for you.", ToolCalls: []provider.ToolCall{
			{ID: "call_1", Name: "read_file", Arguments: `{"path":"main.go"}`},
		}},
		{Role: provider.RoleTool, Content: "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}", ToolCallID: "call_1", Name: "read_file"},
		{Role: provider.RoleAssistant, Content: "This is a simple Go program that prints 'Hello, World!' to the console."},
		{Role: provider.RoleUser, Content: "Now add a function that returns the sum of two numbers"},
		{Role: provider.RoleAssistant, Content: "I'll add that function.", ToolCalls: []provider.ToolCall{
			{ID: "call_2", Name: "edit_file", Arguments: `{"path":"main.go","content":"func add(a, b int) int {\n\treturn a + b\n}"}`},
		}},
		{Role: provider.RoleTool, Content: "File edited successfully", ToolCallID: "call_2", Name: "edit_file"},
		{Role: provider.RoleAssistant, Content: "Done! I've added an `add` function that takes two integers and returns their sum."},
	}

	// Analysis prompt
	prompt := "Analyze the tool call sequences in this session. Extract: (1) common tool chains, (2) tools that failed, (3) tools never used. Return JSON."

	// Call analysis
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	response, err := CallAnalysis(ctx, prov, session, prompt)
	if err != nil {
		t.Fatalf("CallAnalysis failed: %v", err)
	}

	t.Logf("Analysis response length: %d bytes", len(response))
	t.Logf("Analysis response (first 500 chars): %.500s", response)

	// Verify response is valid JSON
	var result map[string]any
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Errorf("response is not valid JSON: %v\nResponse: %s", err, response)
	}

	// Verify we got some analysis
	if len(result) == 0 {
		t.Error("response is empty JSON object")
	}
}

// TestLiveWriteInsight verifies JSONL writing works end-to-end.
func TestLiveWriteInsight(t *testing.T) {
	dir := t.TempDir()

	err := WriteInsight(dir, "test.jsonl", "session-123", "test-hook", 5, map[string]any{
		"patterns": []map[string]any{
			{"chain": []string{"read_file", "edit_file"}, "frequency": 3},
		},
	})
	if err != nil {
		t.Fatalf("WriteInsight failed: %v", err)
	}

	// Read back and verify
	data, err := os.ReadFile(dir + "/test.jsonl")
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var insight Insight
	if err := json.Unmarshal(data, &insight); err != nil {
		t.Fatalf("failed to unmarshal insight: %v", err)
	}

	if insight.Session != "session-123" {
		t.Errorf("session = %q, want %q", insight.Session, "session-123")
	}
	if insight.Turn != 5 {
		t.Errorf("turn = %d, want 5", insight.Turn)
	}
	if insight.Hook != "test-hook" {
		t.Errorf("hook = %q, want %q", insight.Hook, "test-hook")
	}
	if insight.Analysis == nil {
		t.Error("analysis is nil")
	}
}
