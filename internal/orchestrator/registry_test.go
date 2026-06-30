package orchestrator_test

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
)

// TestStateClarifyingConstantAndString implements TC-127-01 to verify StateClarifying is
// a distinct lifecycle constant and has String() == "clarifying".
func TestStateClarifyingConstantAndString(t *testing.T) {
	// 1. Verify String() for StateClarifying
	if got := orchestrator.StateClarifying.String(); got != "clarifying" {
		t.Errorf("StateClarifying.String() = %q, want %q", got, "clarifying")
	}

	// 2. Verify all states are distinct
	states := []orchestrator.GoalState{
		orchestrator.StateQueued,
		orchestrator.StateClarifying,
		orchestrator.StatePlanning,
		orchestrator.StateAwaitingApproval,
		orchestrator.StateDispatching,
		orchestrator.StateDone,
		orchestrator.StateFailed,
		orchestrator.StateCancelled,
	}

	seen := make(map[int]orchestrator.GoalState)
	for _, state := range states {
		val := int(state)
		if existing, ok := seen[val]; ok {
			t.Errorf("duplicate integer value %d shared by state %q and %q", val, existing, state)
		}
		seen[val] = state
	}

	// 3. Verify existing state constants' String() values are unchanged
	expectedStrings := map[orchestrator.GoalState]string{
		orchestrator.StateQueued:           "queued",
		orchestrator.StatePlanning:         "planning",
		orchestrator.StateAwaitingApproval: "awaiting-approval",
		orchestrator.StateDispatching:      "dispatching",
		orchestrator.StateDone:             "done",
		orchestrator.StateFailed:           "failed",
		orchestrator.StateCancelled:        "cancelled",
	}

	for state, expected := range expectedStrings {
		if got := state.String(); got != expected {
			t.Errorf("%v.String() = %q, want %q", state, got, expected)
		}
	}
}
