package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
	_ "reasonix/internal/tool/builtin"
)

// TestTruncateToolOutputUnderCap leaves small payloads alone — the cap should
// never rewrite content that already fits.
func TestTruncateToolOutputUnderCap(t *testing.T) {
	in := strings.Repeat("a", maxToolOutputBytes)
	got, notice := truncateToolOutput(in)
	if got != in {
		t.Errorf("payload at exactly the cap was rewritten")
	}
	if notice != "" {
		t.Errorf("at-cap payload should not emit a notice, got %q", notice)
	}
}

// TestTruncateToolOutputHeadTail keeps head+tail of an oversize payload and
// inserts a marker; the notice must report the elided byte count truthfully.
func TestTruncateToolOutputHeadTail(t *testing.T) {
	head := strings.Repeat("H", maxToolOutputBytes)
	tail := strings.Repeat("T", maxToolOutputBytes)
	in := head + tail
	out, notice := truncateToolOutput(in)
	if !strings.HasPrefix(out, "H") || !strings.HasSuffix(out, "T") {
		t.Errorf("head/tail not preserved at the edges: %q…%q", out[:20], out[len(out)-20:])
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("truncation marker missing: %q", out)
	}
	if len(out) >= len(in) {
		t.Errorf("output not shorter than input: in=%d out=%d", len(in), len(out))
	}
	if !strings.Contains(notice, "truncated") {
		t.Errorf("notice missing: %q", notice)
	}
}

// TestTruncateToolOutputRuneBoundaries puts multibyte runes exactly across the
// head and tail cut points; the result must still be valid UTF-8.
func TestTruncateToolOutputRuneBoundaries(t *testing.T) {
	in := strings.Repeat("中", maxToolOutputBytes) // 3 bytes each — guarantees a cut inside a rune
	out, _ := truncateToolOutput(in)
	if !utf8.ValidString(out) {
		t.Errorf("truncated output is not valid UTF-8")
	}
}

// TestFinishReasonMessage only yields a warning for abnormal terminations.
// Normal stops are silent (ok=false) so the per-turn line stays clean.
func TestFinishReasonMessage(t *testing.T) {
	silent := []string{"", "stop", "tool_calls"}
	for _, r := range silent {
		if msg, ok := finishReasonMessage(&provider.Usage{FinishReason: r}); ok {
			t.Errorf("finish_reason=%q should be silent, got %q", r, msg)
		}
	}
	loud := map[string]string{
		"length":                "max output",
		"content_filter":        "content filter",
		"repetition_truncation": "repetition",
	}
	for reason, fragment := range loud {
		msg, ok := finishReasonMessage(&provider.Usage{FinishReason: reason})
		if !ok || !strings.Contains(msg, fragment) {
			t.Errorf("finish_reason=%q: got (%q, %v), want fragment %q", reason, msg, ok, fragment)
		}
	}
}

// TestEmptyFinalNotice carries the diagnostics that tell the three empty-answer
// causes apart in reports: which provider, how it stopped, and whether the model
// produced reasoning-only output.
func TestEmptyFinalNotice(t *testing.T) {
	msg := emptyFinalNotice("deepseek-flash", &provider.Usage{FinishReason: "stop"}, 512)
	for _, want := range []string{"deepseek-flash", "finish=stop", "reasoning=512"} {
		if !strings.Contains(msg, want) {
			t.Errorf("notice %q missing %q", msg, want)
		}
	}
	if got := emptyFinalNotice("p", nil, 0); !strings.Contains(got, "finish=unknown") {
		t.Errorf("nil usage should report finish=unknown, got %q", got)
	}
}

// --- parallel-dispatch tests ---

// fakeTool is a minimal Tool stand-in for dispatch tests; ReadOnly is
// configurable and Execute sleeps a fixed duration so we can measure
// serial vs parallel behaviour by wall-clock.
type fakeTool struct {
	name     string
	readOnly bool
	delay    time.Duration
	err      error
	calls    *int32 // shared counter to assert all dispatched
}

func (f fakeTool) Name() string            { return f.name }
func (f fakeTool) Description() string     { return "" }
func (f fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f fakeTool) ReadOnly() bool          { return f.readOnly }
func (f fakeTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	if f.calls != nil {
		atomic.AddInt32(f.calls, 1)
	}
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if f.err != nil {
		return "", f.err
	}
	return f.name + " done", nil
}

func TestPartitionToolCallsAllReadOnly(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "ro1", readOnly: true})
	reg.Add(fakeTool{name: "ro2", readOnly: true})
	calls := []provider.ToolCall{{Name: "ro1"}, {Name: "ro2"}}
	got := partitionToolCalls(reg, calls)
	want := []toolCallBatch{{start: 0, end: 2, parallel: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partitionToolCalls = %+v, want %+v", got, want)
	}
}

// TestPartitionToolCallsSegmentsAroundWriters verifies a writer only serializes
// its own provider-order position; read-only runs on either side stay batchable.
func TestPartitionToolCallsSegmentsAroundWriters(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "ro", readOnly: true})
	reg.Add(fakeTool{name: "rw", readOnly: false})
	calls := []provider.ToolCall{{Name: "ro"}, {Name: "rw"}, {Name: "ro"}}
	got := partitionToolCalls(reg, calls)
	want := []toolCallBatch{
		{start: 0, end: 1, parallel: true},
		{start: 1, end: 2},
		{start: 2, end: 3, parallel: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partitionToolCalls = %+v, want %+v", got, want)
	}
}

// TestPartitionToolCallsUnknownToolSerial keeps unknown-tool errors
// deterministic by forcing unknown calls into single-call serial batches.
func TestPartitionToolCallsUnknownToolSerial(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "ro", readOnly: true})
	calls := []provider.ToolCall{{Name: "ro"}, {Name: "vanished"}, {Name: "ro"}}
	got := partitionToolCalls(reg, calls)
	want := []toolCallBatch{
		{start: 0, end: 1, parallel: true},
		{start: 1, end: 2},
		{start: 2, end: 3, parallel: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partitionToolCalls = %+v, want %+v", got, want)
	}
}

// TestPartitionToolCallsCompleteStepSerial verifies complete_step never joins a
// parallel read-only run: it reads the turn's receipts, so the prior reads must
// finish (and record) in an earlier batch before it runs in its own serial one.
func TestPartitionToolCallsCompleteStepSerial(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	reg.Add(fakeTool{name: "complete_step", readOnly: true})

	calls := []provider.ToolCall{{Name: "read_file"}, {Name: "complete_step"}}
	got := partitionToolCalls(reg, calls)
	want := []toolCallBatch{
		{start: 0, end: 1, parallel: true},
		{start: 1, end: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partitionToolCalls = %+v, want %+v", got, want)
	}
}

func TestPartitionToolCallsTodoWriteSerial(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "read_file", readOnly: true})
	reg.Add(fakeTool{name: "todo_write", readOnly: true})

	calls := []provider.ToolCall{{Name: "read_file"}, {Name: "todo_write"}, {Name: "read_file"}}
	got := partitionToolCalls(reg, calls)
	want := []toolCallBatch{
		{start: 0, end: 1, parallel: true},
		{start: 1, end: 2},
		{start: 2, end: 3, parallel: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("partitionToolCalls = %+v, want %+v", got, want)
	}
}

// TestExecuteBatchParallelReadOnly checks that three 80ms read-only calls
// complete in well under 3×80ms — the wall-clock proof of true parallelism.
func TestExecuteBatchParallelReadOnly(t *testing.T) {
	const delay = 80 * time.Millisecond
	calls := int32(0)
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "a", readOnly: true, delay: delay, calls: &calls})
	reg.Add(fakeTool{name: "b", readOnly: true, delay: delay, calls: &calls})
	reg.Add(fakeTool{name: "c", readOnly: true, delay: delay, calls: &calls})

	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	start := time.Now()
	results := a.executeBatch(context.Background(), []provider.ToolCall{{Name: "a"}, {Name: "b"}, {Name: "c"}})
	elapsed := time.Since(start)

	if calls != 3 {
		t.Errorf("dispatched %d calls, want 3", calls)
	}
	if len(results) != 3 || results[0] != "a done" || results[1] != "b done" || results[2] != "c done" {
		t.Errorf("results out of order or wrong: %v", results)
	}
	// Allow generous slack for CI; even 2x serial would prove we got parallelism.
	if elapsed >= 2*delay {
		t.Errorf("read-only batch took %v (>= %v) — not parallel", elapsed, 2*delay)
	}
}

// TestExecuteBatchSegmentsAroundWrites ensures a write call only serializes its
// own position in the provider-ordered batch: read-only runs before and after it
// may still parallelise within their contiguous segments.
func TestExecuteBatchSegmentsAroundWrites(t *testing.T) {
	// A larger per-call delay keeps fixed scheduler jitter on loaded CI a small
	// fraction of the segment time, so the tight relative bound below stays
	// reliable instead of being widened toward the serial floor.
	const delay = 100 * time.Millisecond
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "ro1", readOnly: true, delay: delay})
	reg.Add(fakeTool{name: "ro2", readOnly: true, delay: delay})
	reg.Add(fakeTool{name: "ro3", readOnly: true, delay: delay})
	reg.Add(fakeTool{name: "ro4", readOnly: true, delay: delay})
	reg.Add(fakeTool{name: "rw", readOnly: false, delay: delay})

	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	start := time.Now()
	results := a.executeBatch(context.Background(), []provider.ToolCall{
		{Name: "ro1"},
		{Name: "ro2"},
		{Name: "rw"},
		{Name: "ro3"},
		{Name: "ro4"},
	})
	elapsed := time.Since(start)

	want := []string{"ro1 done", "ro2 done", "rw done", "ro3 done", "ro4 done"}
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d: %v", len(results), len(want), results)
	}
	for i := range want {
		if results[i] != want[i] {
			t.Fatalf("results out of order or wrong: got %v want %v", results, want)
		}
	}
	// Desired shape is roughly 3*delay: (ro1|ro2), then rw, then (ro3|ro4).
	// Old all-serial behaviour is roughly 5*delay and should fail this bound.
	if elapsed >= 4*delay {
		t.Errorf("mixed batch took %v (>= %v) — read-only segments did not parallelise", elapsed, 4*delay)
	}
	if elapsed < 2*delay {
		t.Errorf("mixed batch took only %v — write call appears to have overlapped a read-only segment", elapsed)
	}
}

func TestExecuteBatchFeedsReceiptsToCompleteStep(t *testing.T) {
	completeStep, ok := tool.LookupBuiltin("complete_step")
	if !ok {
		t.Fatal("complete_step builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false})
	reg.Add(completeStep)
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	results := a.executeBatch(context.Background(), []provider.ToolCall{
		{Name: "bash", Arguments: `{"command":"go test ./internal/..."}`},
		{Name: "complete_step", Arguments: `{
			"step":"Run checks",
			"result":"checks passed",
			"evidence":[{"kind":"verification","summary":"tests passed","command":"go test ./internal/..."}]
		}`},
	})

	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if !strings.Contains(results[1], "host-verified 1") {
		t.Fatalf("complete_step did not see bash receipt: %q", results[1])
	}
}

func TestExecuteOneFailedReceiptDoesNotVerify(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false, err: errors.New("boom")})
	a := New(nil, reg, NewSession(""), Options{}, event.Discard)

	out := a.executeOne(context.Background(), provider.ToolCall{Name: "bash", Arguments: `{"command":"go test ./..."}`})
	if out.errMsg == "" {
		t.Fatal("failing fake tool should return an error outcome")
	}
	if a.evidence.HasSuccessfulCommand("go test ./...") {
		t.Fatal("failed bash receipt must not verify")
	}
}

// --- cross-server group tests ---

func TestMCPServer(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"mcp__learner__delegate_task", "learner"},
		{"mcp__coder__delegate_task", "coder"},
		{"mcp__tester__delegate_task", "tester"},
		{"mcp__single", "single"},                          // no double underscore after server
		{"mcp__a__b__c", "a"},                              // only first segment
		{"builtin_read_file", ""},
		{"bash", ""},
		{"", ""},
		{"mcp__", ""},
		{"mcp____", ""},
	}
	for _, tc := range tests {
		got := mcpServer(tc.name)
		if got != tc.want {
			t.Errorf("mcpServer(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestGroupBatchesByServerAllBuiltIn(t *testing.T) {
	// Three serial batches (same built-in server="") → one group.
	batches := []toolCallBatch{
		{start: 0, end: 1}, // bash
		{start: 1, end: 2}, // read_file
		{start: 2, end: 3}, // edit_file
	}
	calls := []provider.ToolCall{
		{Name: "bash"},
		{Name: "read_file"},
		{Name: "edit_file"},
	}
	got := groupBatchesByServer(batches, calls)
	if len(got) != 1 {
		t.Fatalf("wanted 1 group, got %d: %+v", len(got), got)
	}
	if len(got[0].batches) != 3 {
		t.Errorf("wanted 3 batches in the group, got %d", len(got[0].batches))
	}
}

func TestGroupBatchesByServerDifferentMCP(t *testing.T) {
	// Two serial batches for different MCP servers → two groups.
	batches := []toolCallBatch{
		{start: 0, end: 1}, // mcp__learner__delegate_task
		{start: 1, end: 2}, // mcp__coder__delegate_task
	}
	calls := []provider.ToolCall{
		{Name: "mcp__learner__delegate_task"},
		{Name: "mcp__coder__delegate_task"},
	}
	got := groupBatchesByServer(batches, calls)
	if len(got) != 2 {
		t.Fatalf("wanted 2 groups, got %d: %+v", len(got), got)
	}
}

func TestGroupBatchesByServerSameMCPMerges(t *testing.T) {
	// Two serial batches for the same MCP server → merged into one group.
	batches := []toolCallBatch{
		{start: 0, end: 1},
		{start: 1, end: 2},
	}
	calls := []provider.ToolCall{
		{Name: "mcp__learner__delegate_task"},
		{Name: "mcp__learner__something_else"},
	}
	got := groupBatchesByServer(batches, calls)
	if len(got) != 1 {
		t.Fatalf("wanted 1 group, got %d: %+v", len(got), got)
	}
	if len(got[0].batches) != 2 {
		t.Errorf("wanted 2 batches in the merged group, got %d", len(got[0].batches))
	}
}

func TestGroupBatchesByServerParallelBreaksGroup(t *testing.T) {
	// A serial batch, then a parallel batch, then the same server again.
	// The parallel batch always starts a new group, so the second serial batch
	// does NOT merge with the first.
	batches := []toolCallBatch{
		{start: 0, end: 1},                   // serial, mcp__learner
		{start: 1, end: 2, parallel: true},   // parallel, breaks group
		{start: 2, end: 3},                   // serial, mcp__learner again
	}
	calls := []provider.ToolCall{
		{Name: "mcp__learner__a"},
		{Name: "mcp__learner__b"},
		{Name: "mcp__learner__c"},
	}
	got := groupBatchesByServer(batches, calls)
	if len(got) != 3 {
		t.Fatalf("wanted 3 groups, got %d: %+v", len(got), got)
	}
}

func TestGroupBatchesByServerMixed(t *testing.T) {
	// Built-in, MCP server A, MCP server B → three groups.
	batches := []toolCallBatch{
		{start: 0, end: 1}, // built-in
		{start: 1, end: 2}, // mcp__learner
		{start: 2, end: 3}, // mcp__coder
	}
	calls := []provider.ToolCall{
		{Name: "bash"},
		{Name: "mcp__learner__delegate_task"},
		{Name: "mcp__coder__delegate_task"},
	}
	got := groupBatchesByServer(batches, calls)
	if len(got) != 3 {
		t.Fatalf("wanted 3 groups, got %d: %+v", len(got), got)
	}
}

// TestRunServerGroupsCrossServerParallel verifies that groups targeting
// different MCP servers run concurrently. Each group has one 80ms call;
// sequential execution would take ~160ms, parallel ~80ms.
func TestRunServerGroupsCrossServerParallel(t *testing.T) {
	const delay = 80 * time.Millisecond

	callsDispatched := int32(0)
	run := func(i int) {
		atomic.AddInt32(&callsDispatched, 1)
		<-time.After(delay)
	}

	groups := []serverGroup{
		{server: "learner", batches: []toolCallBatch{{start: 0, end: 1}}},
		{server: "coder", batches: []toolCallBatch{{start: 1, end: 2}}},
	}

	start := time.Now()
	runServerGroups(groups, run)
	elapsed := time.Since(start)

	if callsDispatched != 2 {
		t.Errorf("dispatched %d calls, want 2", callsDispatched)
	}
	// Parallel: ~80ms + scheduler overhead; serial: >160ms.
	if elapsed >= 2*delay {
		t.Errorf("cross-server groups took %v (>= %v) — not parallel", elapsed, 2*delay)
	}
}

// TestRunServerGroupsSameServerSerial verifies that groups targeting the same
// MCP server run serially (shared connection).
func TestRunServerGroupsSameServerSerial(t *testing.T) {
	const delay = 50 * time.Millisecond

	callOrder := make([]int, 0, 2)
	var mu sync.Mutex
	run := func(i int) {
		mu.Lock()
		callOrder = append(callOrder, i)
		mu.Unlock()
		<-time.After(delay)
	}

	// Two serial batches merged into one group for the same server.
	groups := []serverGroup{
		{server: "learner", batches: []toolCallBatch{{start: 0, end: 1}, {start: 1, end: 2}}},
	}

	start := time.Now()
	runServerGroups(groups, run)
	elapsed := time.Since(start)

	if len(callOrder) != 2 {
		t.Fatalf("dispatched %d calls, want 2", len(callOrder))
	}
	if callOrder[0] != 0 || callOrder[1] != 1 {
		t.Errorf("calls out of order: %v, want [0 1]", callOrder)
	}
	// Serial: ~100ms.
	if elapsed < 2*delay {
		t.Errorf("same-server group took %v (< %v) — batches ran in parallel", elapsed, 2*delay)
	}
}

// TestRunServerGroupsSameNamedServerSerial verifies that two groups targeting
// the same named MCP server run serially (only different servers parallelize).
func TestRunServerGroupsSameNamedServerSerial(t *testing.T) {
	const delay = 50 * time.Millisecond

	callOrder := make([]int, 0, 2)
	var mu sync.Mutex
	run := func(i int) {
		mu.Lock()
		callOrder = append(callOrder, i)
		mu.Unlock()
		<-time.After(delay)
	}

	// Two groups with the same server — should NOT parallelize.
	groups := []serverGroup{
		{server: "learner", batches: []toolCallBatch{{start: 0, end: 1}}},
		{server: "learner", batches: []toolCallBatch{{start: 1, end: 2}}},
	}

	start := time.Now()
	runServerGroups(groups, run)
	elapsed := time.Since(start)

	if len(callOrder) != 2 {
		t.Fatalf("dispatched %d calls, want 2", len(callOrder))
	}
	if callOrder[0] != 0 || callOrder[1] != 1 {
		t.Errorf("calls out of order: %v, want [0 1]", callOrder)
	}
	// Sequential: ~100ms; parallel would be ~50ms.
	if elapsed < 2*delay {
		t.Errorf("same-named-server groups took %v (< %v) — ran in parallel despite same server", elapsed, 2*delay)
	}
}

// TestRunServerGroupsPreservesOutputOrder verifies that even with cross-server
// parallelism, results still come back in call order.
// TestRunServerGroupsCapUnderLimit executes 3 groups (under the cap of 8) and
// verifies they all complete correctly.
func TestRunServerGroupsCapUnderLimit(t *testing.T) {
	const delay = 30 * time.Millisecond

	callsDispatched := int32(0)
	run := func(i int) {
		atomic.AddInt32(&callsDispatched, 1)
		<-time.After(delay)
	}

	groups := []serverGroup{
		{server: "s1", batches: []toolCallBatch{{start: 0, end: 1}}},
		{server: "s2", batches: []toolCallBatch{{start: 1, end: 2}}},
		{server: "s3", batches: []toolCallBatch{{start: 2, end: 3}}},
	}

	start := time.Now()
	runServerGroups(groups, run)
	elapsed := time.Since(start)

	if callsDispatched != 3 {
		t.Errorf("dispatched %d calls, want 3", callsDispatched)
	}
	// Parallel (3 under cap of 8): ~30ms + overhead; serial: ~90ms.
	if elapsed >= 3*delay {
		t.Errorf("3 server groups took %v (>= %v) — not parallel", elapsed, 3*delay)
	}
}

// TestRunServerGroupsCapPreventsOverflow verifies that with 12 different server
// groups (over the cap of 8), at most 8 run concurrently. We measure this by
// tracking the peak concurrent count.
func TestRunServerGroupsCapPreventsOverflow(t *testing.T) {
	const delay = 50 * time.Millisecond

	var mu sync.Mutex
	var concurrent, peak int32
	run := func(i int) {
		mu.Lock()
		concurrent++
		if concurrent > peak {
			peak = concurrent
		}
		mu.Unlock()

		<-time.After(delay)

		mu.Lock()
		concurrent--
		mu.Unlock()
	}

	groups := make([]serverGroup, 12)
	for i := range groups {
		groups[i] = serverGroup{
			server:   fmt.Sprintf("s%d", i),
			batches:  []toolCallBatch{{start: i, end: i + 1}},
		}
	}

	runServerGroups(groups, run)

	if peak > maxParallel {
		t.Errorf("peak concurrency was %d, want ≤ %d", peak, maxParallel)
	}
	if peak < 2 {
		t.Errorf("peak concurrency was only %d — groups appear to have run serially", peak)
	}
}

func TestRunServerGroupsPreservesOutputOrder(t *testing.T) {
	// Each call records its index in a shared slice. The first call (server A)
	// sleeps 100ms; the second (server B) sleeps 10ms. With parallelism the
	// fast one finishes first, but the output ordering reflects call order.
	const fastDelay = 10 * time.Millisecond
	const slowDelay = 100 * time.Millisecond

	results := make([]int, 2)
	run := func(i int) {
		if i == 0 {
			<-time.After(slowDelay)
		} else {
			<-time.After(fastDelay)
		}
		results[i] = i
	}

	groups := []serverGroup{
		{server: "learner", batches: []toolCallBatch{{start: 0, end: 1}}}, // slow
		{server: "coder", batches: []toolCallBatch{{start: 1, end: 2}}},   // fast
	}

	runServerGroups(groups, run)

	// Even with parallelism, results are filled by call index.
	if results[0] != 0 || results[1] != 1 {
		t.Errorf("results out of order: %v, want [0 1]", results)
	}
}

func TestRunResetsEvidenceLedger(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false})
	prov := &mockProvider{name: "p", chunks: []provider.Chunk{{Type: provider.ChunkText, Text: "done"}}}
	a := New(prov, reg, NewSession(""), Options{}, event.Discard)

	a.executeOne(context.Background(), provider.ToolCall{Name: "bash", Arguments: `{"command":"go test ./..."}`})
	if !a.evidence.HasSuccessfulCommand("go test ./...") {
		t.Fatal("setup failed to record evidence")
	}

	if err := a.Run(context.Background(), "next turn"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.evidence.HasSuccessfulCommand("go test ./...") {
		t.Fatal("new user turn should not inherit previous receipts")
	}
}
