package runtime

// Tests for task 166: distinguish a policy Decide transport/parse failure from a
// genuine policy-authored deny, and emit an audit event for the transport failure
// UNCONDITIONALLY (no audit_emit obligation can exist in a response that never
// arrived).
//
// These exercise the production outcome-builder (newTransportFailureOutcome) and
// the emission helper (maybeEmitPolicyDecision) directly, without a real policy
// daemon, mirroring run_policy_audit_test.go's pattern. The live decideGate error
// capture is proven end-to-end in tests/e2e (TC-166-02).

import (
	"errors"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// --- TC-166-01: transport error produces a reason distinct from the genuine deny ---

// TestTC166TransportFailureDistinctReason pins the two gateOutcome shapes
// decideGate now produces: (a) a genuine deny and (b) a transport failure. The
// transport-failure reason is built by the same production helper decideGate
// calls, so this asserts the real string, not a reconstructed literal.
func TestTC166TransportFailureDistinctReason(t *testing.T) {
	// (a) genuine deny reason — the exact string the default: branch produces.
	genuine := genuineDenyReason

	// (b) transport failure — built by the production helper.
	transportErr := errors.New("dial unix /tmp/policy.sock: connect: connection refused")
	got := newTransportFailureOutcome(transportErr, "")

	if got.allowed {
		t.Errorf("transport failure allowed = true, want false (must fail closed)")
	}
	if !got.transportFailure {
		t.Errorf("transportFailure = false, want true")
	}
	if got.policyDecision != "deny" {
		t.Errorf("policyDecision = %q, want %q (fail-closed to deny)", got.policyDecision, "deny")
	}
	if got.auditEmit {
		t.Errorf("auditEmit = true, want false (no obligation can exist; transportFailure forces emission instead)")
	}
	if got.classifyReason != policyTransportErrorReason {
		t.Errorf("classifyReason = %q, want %q", got.classifyReason, policyTransportErrorReason)
	}

	// REQ-166-02: the reason must be a DIFFERENT string, and must name the failure.
	if got.reason == genuine {
		t.Fatalf("transport reason %q == genuine deny reason %q, want observably different", got.reason, genuine)
	}
	if !strings.Contains(got.reason, "decide call failed") {
		t.Errorf("transport reason = %q, want it to contain %q", got.reason, "decide call failed")
	}
	if strings.Contains(got.reason, "decision denied") {
		t.Errorf("transport reason = %q, must not be confusable with the genuine deny string", got.reason)
	}
	// The underlying error text is surfaced for operator debugging.
	if !strings.Contains(got.reason, "connection refused") {
		t.Errorf("transport reason = %q, want it to surface the decideErr text", got.reason)
	}
}

// --- TC-166-03: transport failure with an audit sink emits an event unconditionally ---

// TestTC166TransportFailureEmitsUnconditionally verifies that a transport-failure
// gateOutcome (auditEmit == false, transportFailure == true) still produces
// exactly one ActionPolicyDecision event, classified with the new Reason value.
func TestTC166TransportFailureEmitsUnconditionally(t *testing.T) {
	sink := audit.NewFakeSink()
	outcome := newTransportFailureOutcome(errors.New("parse error: not valid json"), "")

	// Guard the premise: no audit_emit obligation is present on this path.
	if outcome.auditEmit {
		t.Fatal("precondition violated: auditEmit must be false on the transport-failure path")
	}

	maybeEmitPolicyDecision(sink, "166", outcome)

	events := sink.Events()
	var policyEvents []audit.AuditEvent
	for _, ev := range events {
		if ev.Action == audit.ActionPolicyDecision {
			policyEvents = append(policyEvents, ev)
		}
	}
	if len(policyEvents) != 1 {
		t.Fatalf("ActionPolicyDecision event count = %d, want exactly 1 (unconditional emission despite auditEmit==false); events=%v", len(policyEvents), events)
	}
	ev := policyEvents[0]
	if ev.Detail.Reason != policyTransportErrorReason {
		t.Errorf("Detail.Reason = %q, want %q (classification distinguishing it from an obligation-driven emission)", ev.Detail.Reason, policyTransportErrorReason)
	}
	if ev.Detail.PolicyDecision != "deny" {
		t.Errorf("Detail.PolicyDecision = %q, want %q (the fail-closed decision)", ev.Detail.PolicyDecision, "deny")
	}
	if ev.RunID != "166" || ev.TaskID != "166" {
		t.Errorf("RunID/TaskID = %q/%q, want 166/166", ev.RunID, ev.TaskID)
	}
}

// TestTC166ObligationEmissionLeavesReasonEmpty is a negative/mutation guard: a
// normal audit_emit-driven emission (NOT a transport failure) must NOT set
// Detail.Reason, so the classification is unique to the transport-failure path.
func TestTC166ObligationEmissionLeavesReasonEmpty(t *testing.T) {
	sink := audit.NewFakeSink()
	outcome := gateOutcome{
		allowed:        true,
		auditEmit:      true,
		policyDecision: "allow",
		policyReason:   "allowlisted",
		// transportFailure deliberately false.
	}
	maybeEmitPolicyDecision(sink, "166", outcome)

	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if events[0].Detail.Reason != "" {
		t.Errorf("Detail.Reason = %q, want empty (only a transport failure classifies the event)", events[0].Detail.Reason)
	}
}

// --- TC-166-04: nil sink on transport failure does not panic ---

func TestTC166TransportFailureNilSinkNoPanic(t *testing.T) {
	outcome := newTransportFailureOutcome(errors.New("timeout"), "")
	// Must not panic even though transportFailure forces the emission branch.
	maybeEmitPolicyDecision(nil, "166", outcome)
	t.Log("TC-166-04: nil sink on transport failure did not panic")
}
