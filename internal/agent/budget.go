package agent

import (
	"fmt"
	"strings"
	"sync"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// BudgetLimits defines hard limits for resource usage.
type BudgetLimits struct {
	MaxTokens    int     // 0 = unlimited
	MaxCostUSD   float64 // 0 = unlimited
	MaxTurns     int     // 0 = unlimited
}

// BudgetStatus is the current state of budget consumption.
type BudgetStatus struct {
	TokensUsed   int
	CostUSD      float64
	TurnsUsed    int
	Limits       BudgetLimits
	TokenExceeded bool
	CostExceeded  bool
	TurnExceeded  bool
}

// Exceeded returns true if any limit is exceeded.
func (s BudgetStatus) Exceeded() bool {
	return s.TokenExceeded || s.CostExceeded || s.TurnExceeded
}

// Summary returns a human-readable budget status line.
func (s BudgetStatus) Summary() string {
	var parts []string
	if s.Limits.MaxTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens: %d/%d", s.TokensUsed, s.Limits.MaxTokens))
	}
	if s.Limits.MaxCostUSD > 0 {
		parts = append(parts, fmt.Sprintf("cost: $%.2f/$%.2f", s.CostUSD, s.Limits.MaxCostUSD))
	}
	if s.Limits.MaxTurns > 0 {
		parts = append(parts, fmt.Sprintf("turns: %d/%d", s.TurnsUsed, s.Limits.MaxTurns))
	}
	if len(parts) == 0 {
		return "no budget limits set"
	}
	status := "ok"
	if s.Exceeded() {
		status = "EXCEEDED"
	}
	return fmt.Sprintf("[%s] %s", status, strings.Join(parts, ", "))
}

// BudgetTracker provides incremental cost tracking with hard limits.
// It is safe for concurrent use.
type BudgetTracker struct {
	mu     sync.Mutex
	tokens int
	cost   float64
	turns  int
	limits BudgetLimits
}

// NewBudgetTracker creates a tracker with the given limits.
func NewBudgetTracker(limits BudgetLimits) *BudgetTracker {
	return &BudgetTracker{limits: limits}
}

// SetLimits updates the budget limits (e.g. from config reload).
func (bt *BudgetTracker) SetLimits(limits BudgetLimits) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.limits = limits
}

// RecordUsage records token usage and cost from a Usage event.
func (bt *BudgetTracker) RecordUsage(usage *provider.Usage, pricing *provider.Pricing) {
	if usage == nil {
		return
	}
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.tokens += usage.TotalTokens
	bt.cost += computeCost(usage, pricing)
}

// RecordTurn increments the turn counter.
func (bt *BudgetTracker) RecordTurn() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.turns++
}

// Check returns the current budget status. Call before each LLM call to
// determine if the budget is exceeded.
func (bt *BudgetTracker) Check() BudgetStatus {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	status := BudgetStatus{
		TokensUsed: bt.tokens,
		CostUSD:    bt.cost,
		TurnsUsed:  bt.turns,
		Limits:     bt.limits,
	}
	if bt.limits.MaxTokens > 0 && bt.tokens >= bt.limits.MaxTokens {
		status.TokenExceeded = true
	}
	if bt.limits.MaxCostUSD > 0 && bt.cost >= bt.limits.MaxCostUSD {
		status.CostExceeded = true
	}
	if bt.limits.MaxTurns > 0 && bt.turns >= bt.limits.MaxTurns {
		status.TurnExceeded = true
	}
	return status
}

// Reset clears all counters.
func (bt *BudgetTracker) Reset() {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.tokens = 0
	bt.cost = 0
	bt.turns = 0
}

// UsageSink wraps an event.Sink and records usage into a BudgetTracker.
// All other events pass through unchanged.
func UsageSink(inner event.Sink, tracker *BudgetTracker) event.Sink {
	if inner == nil || tracker == nil {
		return inner
	}
	return event.FuncSink(func(e event.Event) {
		if e.Kind == event.Usage && e.Usage != nil {
			tracker.RecordUsage(e.Usage, e.Pricing)
		}
		inner.Emit(e)
	})
}

// computeCost calculates the cost in USD from usage and pricing.
func computeCost(usage *provider.Usage, pricing *provider.Pricing) float64 {
	if usage == nil || pricing == nil {
		return 0
	}
	tokens := float64(usage.PromptTokens + usage.CompletionTokens)
	if tokens == 0 {
		return 0
	}
	// Simple linear pricing: cost = tokens * pricePerToken
	// pricing.Input and pricing.Output are per-million-token rates
	inputCost := float64(usage.PromptTokens) * pricing.Input / 1_000_000
	outputCost := float64(usage.CompletionTokens) * pricing.Output / 1_000_000
	return inputCost + outputCost
}
