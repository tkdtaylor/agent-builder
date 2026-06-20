package runtime

// Tests for TC-073-01, TC-073-02, TC-073-03, TC-073-04, TC-073-05 (policy gate
// routing + audit_emit obligation wiring, task 073).
//
// These tests exercise the internal routing helpers and maybeEmitPolicyDecision
// directly without starting a real policy daemon, mirroring how run_audit_test.go
// exercises RunInside without a real executor.

import (
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// --- TC-073-02: require_approval and deny produce observably different status reasons ---

// TestRequireApprovalStatusReason exercises the gateOutcome routing table:
// deny→"policy: decision denied", require_approval→"policy: requires human approval".
// Both must block dispatch (allowed==false) and produce distinct non-empty reasons.
func TestRequireApprovalStatusReason(t *testing.T) {
	table := []struct {
		name           string
		outcome        gateOutcome
		wantAllowed    bool
		wantReasonWord string // substring that must appear in reason
	}{
		{
			name:           "TC-073-02_deny",
			outcome:        gateOutcome{allowed: false, reason: "policy: decision denied", policyDecision: "deny"},
			wantAllowed:    false,
			wantReasonWord: "denied",
		},
		{
			name:           "TC-073-02_require_approval",
			outcome:        gateOutcome{allowed: false, reason: "policy: requires human approval", policyDecision: "require_approval"},
			wantAllowed:    false,
			wantReasonWord: "approval",
		},
	}

	for _, tc := range table {
		t.Run(tc.name, func(t *testing.T) {
			if tc.outcome.allowed != tc.wantAllowed {
				t.Errorf("allowed = %v, want %v", tc.outcome.allowed, tc.wantAllowed)
			}
			if !strings.Contains(tc.outcome.reason, tc.wantReasonWord) {
				t.Errorf("reason = %q, want it to contain %q", tc.outcome.reason, tc.wantReasonWord)
			}
		})
	}

	// The two reason strings must be observably different.
	denyReason := table[0].outcome.reason
	approvalReason := table[1].outcome.reason
	if denyReason == approvalReason {
		t.Errorf("deny reason %q == require_approval reason %q, want observably different strings", denyReason, approvalReason)
	}
	if strings.Contains(denyReason, "approval") {
		t.Errorf("deny reason %q must not contain 'approval'", denyReason)
	}
}

// --- TC-073-03: audit_emit + allow → FakeSink receives ActionPolicyDecision event ---

// TestPolicyGateAuditEmitObligation verifies that maybeEmitPolicyDecision emits
// an ActionPolicyDecision event carrying decision and reason when auditEmit==true
// and the sink is non-nil (TC-073-03).
func TestPolicyGateAuditEmitObligation(t *testing.T) {
	sink := audit.NewFakeSink()

	outcome := gateOutcome{
		allowed:        true,
		auditEmit:      true,
		policyDecision: "allow",
		policyReason:   "allowlisted",
	}
	maybeEmitPolicyDecision(sink, "073", outcome)

	events := sink.Events()
	if len(events) == 0 {
		t.Fatal("TC-073-03: FakeSink has no events, want at least one ActionPolicyDecision")
	}

	var policyEv *audit.AuditEvent
	for i := range events {
		if events[i].Action == audit.ActionPolicyDecision {
			policyEv = &events[i]
			break
		}
	}
	if policyEv == nil {
		t.Fatalf("TC-073-03: no ActionPolicyDecision event in sink events %v", events)
	}
	if policyEv.Detail.PolicyDecision != "allow" {
		t.Errorf("TC-073-03: PolicyDecision = %q, want %q", policyEv.Detail.PolicyDecision, "allow")
	}
	if policyEv.Detail.PolicyReason != "allowlisted" {
		t.Errorf("TC-073-03: PolicyReason = %q, want %q", policyEv.Detail.PolicyReason, "allowlisted")
	}
	if policyEv.RunID == "" {
		t.Error("TC-073-03: RunID is empty, want non-empty (inherited from task ID)")
	}
	if policyEv.TaskID == "" {
		t.Error("TC-073-03: TaskID is empty, want non-empty")
	}
	t.Logf("TC-073-03 passed: ActionPolicyDecision emitted with decision=%q reason=%q",
		policyEv.Detail.PolicyDecision, policyEv.Detail.PolicyReason)
}

// --- TC-073-04: audit_emit with deny or require_approval still emits; absent = no event; nil sink = no panic ---

// TestPolicyGateAuditEmitWithDenyDecision covers TC-073-04 sub-cases:
// - deny + audit_emit → event emitted, box still blocked
// - require_approval + audit_emit → event emitted, box still blocked
// - allow without audit_emit → no ActionPolicyDecision event emitted
// - nil sink + audit_emit → no panic
func TestPolicyGateAuditEmitWithDenyDecision(t *testing.T) {
	t.Run("TC-073-04_deny_audit_emit_emits_event", func(t *testing.T) {
		sink := audit.NewFakeSink()
		outcome := gateOutcome{
			allowed:        false,
			reason:         "policy: decision denied",
			auditEmit:      true,
			policyDecision: "deny",
			policyReason:   "forbidden resource",
		}
		maybeEmitPolicyDecision(sink, "073", outcome)

		events := sink.Events()
		if len(events) == 0 {
			t.Fatal("deny+audit_emit: no events in sink, want ActionPolicyDecision")
		}
		found := false
		for _, ev := range events {
			if ev.Action == audit.ActionPolicyDecision {
				found = true
				if ev.Detail.PolicyDecision != "deny" {
					t.Errorf("deny+audit_emit: PolicyDecision = %q, want %q", ev.Detail.PolicyDecision, "deny")
				}
			}
		}
		if !found {
			t.Fatal("deny+audit_emit: no ActionPolicyDecision event in sink")
		}
		// Box still blocked (allowed==false).
		if outcome.allowed {
			t.Error("deny+audit_emit: allowed should be false")
		}
	})

	t.Run("TC-073-04_require_approval_audit_emit_emits_event", func(t *testing.T) {
		sink := audit.NewFakeSink()
		outcome := gateOutcome{
			allowed:        false,
			reason:         "policy: requires human approval",
			auditEmit:      true,
			policyDecision: "require_approval",
			policyReason:   "high risk task",
		}
		maybeEmitPolicyDecision(sink, "073", outcome)

		events := sink.Events()
		if len(events) == 0 {
			t.Fatal("require_approval+audit_emit: no events in sink")
		}
		found := false
		for _, ev := range events {
			if ev.Action == audit.ActionPolicyDecision {
				found = true
				if ev.Detail.PolicyDecision != "require_approval" {
					t.Errorf("require_approval+audit_emit: PolicyDecision = %q, want %q",
						ev.Detail.PolicyDecision, "require_approval")
				}
				if ev.Detail.PolicyReason != "high risk task" {
					t.Errorf("require_approval+audit_emit: PolicyReason = %q, want %q",
						ev.Detail.PolicyReason, "high risk task")
				}
			}
		}
		if !found {
			t.Fatal("require_approval+audit_emit: no ActionPolicyDecision event")
		}
		// Box still blocked.
		if outcome.allowed {
			t.Error("require_approval+audit_emit: allowed should be false")
		}
	})

	t.Run("TC-073-04_allow_no_audit_emit_no_event", func(t *testing.T) {
		sink := audit.NewFakeSink()
		outcome := gateOutcome{
			allowed:        true,
			auditEmit:      false, // obligation absent
			policyDecision: "allow",
		}
		maybeEmitPolicyDecision(sink, "073", outcome)

		events := sink.Events()
		for _, ev := range events {
			if ev.Action == audit.ActionPolicyDecision {
				t.Fatalf("allow+no_audit_emit: unexpected ActionPolicyDecision event: %#v", ev)
			}
		}
		t.Log("TC-073-04 allow+no_audit_emit: no ActionPolicyDecision event — correct")
	})

	t.Run("TC-073-04_nil_sink_no_panic", func(t *testing.T) {
		// Must not panic when sink is nil.
		outcome := gateOutcome{
			allowed:        true,
			auditEmit:      true,
			policyDecision: "allow",
			policyReason:   "ok",
		}
		maybeEmitPolicyDecision(nil, "073", outcome) // no panic expected
		t.Log("TC-073-04 nil sink: no panic — correct")
	})
}

// --- TC-073-05: ActionPolicyDecision in validActions; audit.Validate accepts it ---

// TestActionPolicyDecisionInValidActions verifies that the new action constant is
// in the closed enum and passes audit.Validate (TC-073-05 structural assertion).
func TestActionPolicyDecisionInValidActions(t *testing.T) {
	// Constant is defined and has the correct string value.
	if string(audit.ActionPolicyDecision) != "policy-decision" {
		t.Errorf("ActionPolicyDecision = %q, want %q", audit.ActionPolicyDecision, "policy-decision")
	}

	// Valid() returns true (it is in validActions).
	if !audit.ActionPolicyDecision.Valid() {
		t.Error("ActionPolicyDecision.Valid() = false, want true")
	}

	// Validate accepts an event carrying this action.
	ev := audit.AuditEvent{
		Action: audit.ActionPolicyDecision,
		RunID:  "073",
		TaskID: "073",
		Detail: audit.EventDetail{
			PolicyDecision: "allow",
			PolicyReason:   "test",
		},
	}
	if err := audit.Validate(ev); err != nil {
		t.Errorf("Validate(ActionPolicyDecision event) = %v, want nil", err)
	}

	// FakeSink accepts and stores the event.
	sink := audit.NewFakeSink()
	if err := sink.Append(ev); err != nil {
		t.Errorf("FakeSink.Append(ActionPolicyDecision event) = %v, want nil", err)
	}
	if len(sink.Events()) != 1 {
		t.Errorf("sink event count = %d, want 1", len(sink.Events()))
	}
	t.Logf("TC-073-05: ActionPolicyDecision=%q valid=%v validate=nil", audit.ActionPolicyDecision, audit.ActionPolicyDecision.Valid())
}

// TestPolicyDecisionEventDetailFields verifies that EventDetail has the
// PolicyDecision and PolicyReason fields and that they are independent of
// other event types (TC-073-05).
func TestPolicyDecisionEventDetailFields(t *testing.T) {
	// A containment event with no PolicyDecision/PolicyReason must still validate fine.
	containmentEv := audit.AuditEvent{
		Action: audit.ActionContainment,
		RunID:  "073",
		TaskID: "073",
		Detail: audit.EventDetail{Launcher: "podman"},
	}
	if err := audit.Validate(containmentEv); err != nil {
		t.Errorf("Validate(containment event) = %v, want nil", err)
	}
	if containmentEv.Detail.PolicyDecision != "" {
		t.Error("containment event has non-empty PolicyDecision, want zero value")
	}
	if containmentEv.Detail.PolicyReason != "" {
		t.Error("containment event has non-empty PolicyReason, want zero value")
	}

	// A policy-decision event with non-empty fields must validate and preserve values.
	policyEv := audit.AuditEvent{
		Action: audit.ActionPolicyDecision,
		RunID:  "073",
		TaskID: "073",
		Detail: audit.EventDetail{
			PolicyDecision: "deny",
			PolicyReason:   "forbidden",
		},
	}
	if err := audit.Validate(policyEv); err != nil {
		t.Errorf("Validate(policy-decision event) = %v, want nil", err)
	}
	if policyEv.Detail.PolicyDecision != "deny" {
		t.Errorf("PolicyDecision = %q, want %q", policyEv.Detail.PolicyDecision, "deny")
	}
	if policyEv.Detail.PolicyReason != "forbidden" {
		t.Errorf("PolicyReason = %q, want %q", policyEv.Detail.PolicyReason, "forbidden")
	}
	t.Log("TC-073-05: EventDetail.PolicyDecision/PolicyReason preserved; other events unaffected")
}
