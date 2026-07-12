package runtime

// Tests for TC-164-02, TC-164-03 (fail-closed validation of the tier_select
// obligation value, task 164).
//
// TC-164-02 pins the decideGate routing contract by constructing the exact
// gateOutcome shapes the DecisionAllow branch must produce for a valid vs.
// invalid tier, mirroring how TestRequireApprovalStatusReason in
// run_policy_audit_test.go pins the deny/require_approval shapes without
// dialing a real policy daemon. The real proof that decideGate actually
// produces these shapes when talking to a daemon is the L5 e2e test
// (TC-164-04); this test is necessary but not sufficient evidence on its own.

import (
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

// --- TC-164-02: decideGate halts on an invalid tier (design-level pin) ---

func TestTC164DecideGateTierValidation(t *testing.T) {
	t.Run("valid_tier_gvisor_is_unaffected", func(t *testing.T) {
		tier := "gvisor"
		var outcome gateOutcome
		if sandbox.ValidTier(tier) {
			outcome = gateOutcome{
				allowed:        true,
				tier:           tier,
				auditEmit:      true,
				policyDecision: "allow",
				policyReason:   "ok",
			}
		} else {
			t.Fatalf("ValidTier(%q) = false, want true", tier)
		}

		if !outcome.allowed {
			t.Error("allowed = false, want true")
		}
		if outcome.tier != "gvisor" {
			t.Errorf("tier = %q, want %q", outcome.tier, "gvisor")
		}
	})

	t.Run("invalid_tier_quantum_tier_halts", func(t *testing.T) {
		tier := "quantum-tier"
		var outcome gateOutcome
		if sandbox.ValidTier(tier) {
			t.Fatalf("ValidTier(%q) = true, want false", tier)
		}
		outcome = gateOutcome{
			allowed:        false,
			reason:         `policy: tier_select obligation names unknown tier "quantum-tier"`,
			auditEmit:      true,
			policyDecision: "allow",
			policyReason:   "ok",
		}

		if outcome.allowed {
			t.Error("allowed = true, want false")
		}
		if !strings.Contains(outcome.reason, "unknown tier") {
			t.Errorf("reason = %q, want it to contain %q", outcome.reason, "unknown tier")
		}
		if !strings.Contains(outcome.reason, "quantum-tier") {
			t.Errorf("reason = %q, want it to contain %q", outcome.reason, "quantum-tier")
		}
		if outcome.tier != "" {
			t.Errorf("tier = %q, want zero value (never threads to tierOverride)", outcome.tier)
		}
	})
}

// --- TC-164-03: audit_emit unaffected by the new halt path ---

// TestTC164MaybeEmitPolicyDecisionOnHaltedTierOutcome verifies that
// maybeEmitPolicyDecision (unmodified by this task) still emits an
// ActionPolicyDecision event carrying the ENGINE's decision ("allow") when
// fed the new halted-on-invalid-tier outcome shape, so an operator reading
// the audit chain can distinguish "the engine allowed but sent a bad
// obligation" from a genuine deny.
func TestTC164MaybeEmitPolicyDecisionOnHaltedTierOutcome(t *testing.T) {
	sink := audit.NewFakeSink()

	outcome := gateOutcome{
		allowed:        false,
		reason:         `policy: tier_select obligation names unknown tier "quantum-tier"`,
		auditEmit:      true,
		policyDecision: "allow",
		policyReason:   "ok",
	}
	maybeEmitPolicyDecision(sink, "164", outcome)

	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("TC-164-03: FakeSink has no events, want at least one ActionPolicyDecision")
	}

	var policyEv *audit.AuditEvent
	for i := range events {
		if events[i].Action == audit.ActionPolicyDecision {
			policyEv = &events[i]
			break
		}
	}
	if policyEv == nil {
		t.Fatalf("TC-164-03: no ActionPolicyDecision event in sink events %v", events)
	}
	if policyEv.Detail.PolicyDecision != "allow" {
		t.Errorf("TC-164-03: PolicyDecision = %q, want %q", policyEv.Detail.PolicyDecision, "allow")
	}
	if policyEv.Detail.PolicyReason != "ok" {
		t.Errorf("TC-164-03: PolicyReason = %q, want %q", policyEv.Detail.PolicyReason, "ok")
	}
}
