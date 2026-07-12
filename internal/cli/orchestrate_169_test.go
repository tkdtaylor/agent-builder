package cli

// Task 169 TC-169-01: AGENT_BUILDER_GOAL_MAX_ATTEMPTS wiring — default, override,
// and fail-fast on a malformed value.

import (
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestTC169_01_ParseGoalMaxAttempts(t *testing.T) {
	getenvWith := func(val string, present bool) func(string) string {
		return func(k string) string {
			if k == EnvGoalMaxAttempts && present {
				return val
			}
			return ""
		}
	}

	// unset -> default 3
	if n, err := parseGoalMaxAttempts(getenvWith("", false)); err != nil || n != defaultGoalMaxAttempts {
		t.Errorf("unset -> (%d, %v), want (%d, nil)", n, err, defaultGoalMaxAttempts)
	}
	// "5" -> 5
	if n, err := parseGoalMaxAttempts(getenvWith("5", true)); err != nil || n != 5 {
		t.Errorf(`"5" -> (%d, %v), want (5, nil)`, n, err)
	}
	// "abc" -> errUsageConfig
	if _, err := parseGoalMaxAttempts(getenvWith("abc", true)); !errors.Is(err, errUsageConfig) {
		t.Errorf(`"abc" -> err %v, want errUsageConfig`, err)
	}
	// "0" (below the minimum) -> errUsageConfig
	if _, err := parseGoalMaxAttempts(getenvWith("0", true)); !errors.Is(err, errUsageConfig) {
		t.Errorf(`"0" -> err %v, want errUsageConfig`, err)
	}
}

// TC-169-01 (wiring): assembleOrchestrate threads the parsed value into the config
// and fails fast on a malformed one.
func TestTC169_01_AssembleThreadsGoalMaxAttempts(t *testing.T) {
	assemble := func(t *testing.T) (orchestrateConfig, func(), error) {
		return assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
			policyClient: &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
			dispatch:     (&spyDispatch{}).fn,
			auditSink:    audit.NewFakeSink(),
			planner:      twoRecipePlanner(),
			source:       &stubGoalSource{goals: []supervisor.Task{twoSubGoalGoal()}},
			signingKey:   testSigningKey(t),
		})
	}

	t.Run("default_when_unset", func(t *testing.T) {
		setBaseConfigEnv(t)
		oc, cleanup, err := assemble(t)
		if err != nil {
			t.Fatalf("assembleOrchestrate: %v", err)
		}
		t.Cleanup(cleanup)
		if oc.goalMaxAttempts != 3 {
			t.Errorf("goalMaxAttempts = %d, want 3 (default)", oc.goalMaxAttempts)
		}
	})

	t.Run("override_threaded", func(t *testing.T) {
		setBaseConfigEnv(t)
		t.Setenv(EnvGoalMaxAttempts, "7")
		oc, cleanup, err := assemble(t)
		if err != nil {
			t.Fatalf("assembleOrchestrate: %v", err)
		}
		t.Cleanup(cleanup)
		if oc.goalMaxAttempts != 7 {
			t.Errorf("goalMaxAttempts = %d, want 7", oc.goalMaxAttempts)
		}
	})

	t.Run("invalid_fails_fast", func(t *testing.T) {
		setBaseConfigEnv(t)
		t.Setenv(EnvGoalMaxAttempts, "abc")
		_, cleanup, err := assemble(t)
		if cleanup != nil {
			t.Cleanup(cleanup)
		}
		if !errors.Is(err, errUsageConfig) {
			t.Fatalf("assembleOrchestrate err = %v, want errUsageConfig", err)
		}
	})
}
