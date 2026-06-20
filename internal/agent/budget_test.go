package agent

import (
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestBudgetTrackerRecordUsage(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 1000})

	usage := &provider.Usage{
		PromptTokens:     100,
		CompletionTokens: 200,
		TotalTokens:      300,
	}
	pricing := &provider.Pricing{Input: 1.0, Output: 2.0}

	bt.RecordUsage(usage, pricing)

	status := bt.Check()
	if status.TokensUsed != 300 {
		t.Errorf("tokens = %d, want 300", status.TokensUsed)
	}
	if status.TokenExceeded {
		t.Error("should not be exceeded yet")
	}
}

func TestBudgetTrackerTokenExceeded(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 100})

	usage := &provider.Usage{
		PromptTokens:     50,
		CompletionTokens: 60,
		TotalTokens:      110,
	}

	bt.RecordUsage(usage, nil)

	status := bt.Check()
	if !status.TokenExceeded {
		t.Error("should be exceeded")
	}
	if status.TokensUsed != 110 {
		t.Errorf("tokens = %d, want 110", status.TokensUsed)
	}
}

func TestBudgetTrackerCostExceeded(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxCostUSD: 0.01})

	usage := &provider.Usage{
		PromptTokens:     10000,
		CompletionTokens: 10000,
		TotalTokens:      20000,
	}
	pricing := &provider.Pricing{Input: 1.0, Output: 1.0}

	bt.RecordUsage(usage, pricing)

	status := bt.Check()
	if !status.CostExceeded {
		t.Error("should be exceeded")
	}
}

func TestBudgetTrackerTurnExceeded(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTurns: 3})

	bt.RecordTurn()
	bt.RecordTurn()
	bt.RecordTurn()

	status := bt.Check()
	if !status.TurnExceeded {
		t.Error("should be exceeded after 3 turns")
	}

	bt.RecordTurn()
	status = bt.Check()
	if status.TurnsUsed != 4 {
		t.Errorf("turns = %d, want 4", status.TurnsUsed)
	}
}

func TestBudgetTrackerReset(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 100})

	bt.RecordUsage(&provider.Usage{TotalTokens: 50}, nil)
	bt.RecordTurn()

	bt.Reset()

	status := bt.Check()
	if status.TokensUsed != 0 {
		t.Errorf("tokens after reset = %d, want 0", status.TokensUsed)
	}
	if status.TurnsUsed != 0 {
		t.Errorf("turns after reset = %d, want 0", status.TurnsUsed)
	}
}

func TestBudgetTrackerSetLimits(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 100})

	bt.RecordUsage(&provider.Usage{TotalTokens: 50}, nil)
	status := bt.Check()
	if status.TokenExceeded {
		t.Error("should not be exceeded with limit 100")
	}

	bt.SetLimits(BudgetLimits{MaxTokens: 30})
	status = bt.Check()
	if !status.TokenExceeded {
		t.Error("should be exceeded with limit 30")
	}
}

func TestBudgetTrackerUnlimited(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{})

	bt.RecordUsage(&provider.Usage{TotalTokens: 999999}, nil)
	bt.RecordTurn()

	status := bt.Check()
	if status.Exceeded() {
		t.Error("should not be exceeded with unlimited limits")
	}
}

func TestBudgetTrackerConcurrent(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 10000})

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				bt.RecordUsage(&provider.Usage{TotalTokens: 1}, nil)
				bt.RecordTurn()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	status := bt.Check()
	if status.TokensUsed != 1000 {
		t.Errorf("tokens = %d, want 1000", status.TokensUsed)
	}
	if status.TurnsUsed != 1000 {
		t.Errorf("turns = %d, want 1000", status.TurnsUsed)
	}
}

func TestBudgetStatusSummary(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 1000, MaxCostUSD: 5.0, MaxTurns: 10})

	bt.RecordUsage(&provider.Usage{TotalTokens: 500}, nil)
	bt.RecordTurn()

	status := bt.Check()
	summary := status.Summary()
	if summary == "" {
		t.Error("Summary should not be empty")
	}
	// Should contain token info
	if !containsSubstring(summary, "500/1000") {
		t.Errorf("Summary should contain token count, got: %s", summary)
	}
}

func TestBudgetStatusSummaryUnlimited(t *testing.T) {
	status := BudgetStatus{}
	summary := status.Summary()
	if summary != "no budget limits set" {
		t.Errorf("unexpected summary: %s", summary)
	}
}

func TestBudgetStatusSummaryExceeded(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 100})
	bt.RecordUsage(&provider.Usage{TotalTokens: 150}, nil)

	status := bt.Check()
	summary := status.Summary()
	if !containsSubstring(summary, "EXCEEDED") {
		t.Errorf("Summary should contain EXCEEDED, got: %s", summary)
	}
}

func TestBudgetSink(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 1000})
	var received bool
	inner := event.FuncSink(func(e event.Event) {
		received = true
	})

	sink := UsageSink(inner, bt)
	sink.Emit(event.Event{
		Kind:  event.Usage,
		Usage: &provider.Usage{TotalTokens: 100},
	})

	if !received {
		t.Error("event should pass through to inner sink")
	}
	status := bt.Check()
	if status.TokensUsed != 100 {
		t.Errorf("tokens = %d, want 100", status.TokensUsed)
	}
}

func TestBudgetSinkNonUsagePassthrough(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTokens: 1000})
	var received bool
	inner := event.FuncSink(func(e event.Event) {
		received = true
	})

	sink := UsageSink(inner, bt)
	sink.Emit(event.Event{Kind: event.Text})

	if !received {
		t.Error("non-usage events should pass through")
	}
	status := bt.Check()
	if status.TokensUsed != 0 {
		t.Errorf("tokens should be 0, got %d", status.TokensUsed)
	}
}

func TestComputeCost(t *testing.T) {
	usage := &provider.Usage{
		PromptTokens:     1000,
		CompletionTokens: 2000,
	}
	pricing := &provider.Pricing{Input: 1.0, Output: 2.0}

	cost := computeCost(usage, pricing)
	// Input: 1000 * 1.0 / 1M = 0.001
	// Output: 2000 * 2.0 / 1M = 0.004
	// Total: 0.005
	expected := 0.005
	if cost != expected {
		t.Errorf("cost = %f, want %f", cost, expected)
	}
}

func TestComputeCostNilPricing(t *testing.T) {
	usage := &provider.Usage{TotalTokens: 1000}
	cost := computeCost(usage, nil)
	if cost != 0 {
		t.Errorf("cost with nil pricing = %f, want 0", cost)
	}
}

func TestBudgetTrackerRecordTurn(t *testing.T) {
	bt := NewBudgetTracker(BudgetLimits{MaxTurns: 5})

	bt.RecordTurn()
	bt.RecordTurn()

	status := bt.Check()
	if status.TurnsUsed != 2 {
		t.Errorf("turns = %d, want 2", status.TurnsUsed)
	}
}
