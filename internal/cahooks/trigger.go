package cahooks

// State represents the current agent state for trigger evaluation.
type State struct {
	Turn          int
	ContextUsage  float64
	HasErrors     bool
	HasToolCalls  bool
	HasWrites     bool
	HasFileChanges bool
	IdleSeconds   int
	IsPreCompact  bool
	IsPreNewSession bool
}

// ShouldFire reports whether a hook should fire given the current state.
func ShouldFire(hook HookConfig, state State) bool {
	if !hook.Enabled {
		return false
	}
	if !checkCondition(hook.Condition, state) {
		return false
	}
	return checkTrigger(hook, state)
}

func checkTrigger(hook HookConfig, state State) bool {
	switch hook.Trigger {
	case TriggerPostTurn:
		return true
	case TriggerPreCompact:
		return state.IsPreCompact
	case TriggerPreNewSession:
		return state.IsPreNewSession
	case TriggerEveryNTurns:
		n := hook.EveryN
		if n <= 0 {
			n = 5
		}
		return state.Turn > 0 && state.Turn%n == 0
	case TriggerContextThreshold:
		threshold := hook.ContextThreshold
		if threshold <= 0 {
			threshold = 0.7
		}
		return state.ContextUsage >= threshold
	case TriggerSessionIdle:
		return state.IdleSeconds >= hook.IdleSeconds
	default:
		return false
	}
}

func checkCondition(condition string, state State) bool {
	switch condition {
	case ConditionAlways, "":
		return true
	case ConditionHasErrors:
		return state.HasErrors
	case ConditionHasToolCalls:
		return state.HasToolCalls
	case ConditionHasWrites:
		return state.HasWrites
	case ConditionHasFileChanges:
		return state.HasFileChanges
	default:
		return true
	}
}
