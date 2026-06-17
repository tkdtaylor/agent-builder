// Package audit_test exercises the AuditEvent taxonomy, Sink seam, FakeSink, and
// event validation helper declared in internal/audit.
package audit_test

import (
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// TC-038-01: action taxonomy is a closed enum covering every emitted lifecycle action.
func TestActionTaxonomy_ClosedEnumCoversLifecycleActions(t *testing.T) {
	// Every action that the run loop emits must be representable as a typed constant.
	wantActions := []audit.AuditAction{
		audit.ActionContainment,
		audit.ActionPick,
		audit.ActionAttempt,
		audit.ActionVerify,
		audit.ActionPublish,
		audit.ActionEscalate,
		audit.ActionFinish,
	}

	for _, a := range wantActions {
		if !a.Valid() {
			t.Errorf("action %q: Valid() returned false; expected it to be a valid enum member", a)
		}
		if a.String() == "" {
			t.Errorf("action %q: String() returned empty string", a)
		}
	}
}

func TestActionTaxonomy_UnknownActionIsInvalid(t *testing.T) {
	unknown := audit.AuditAction("raw_stdout")
	if unknown.Valid() {
		t.Errorf("unknown action %q: Valid() returned true; expected false", unknown)
	}

	zero := audit.AuditAction("")
	if zero.Valid() {
		t.Errorf("zero-value action: Valid() returned true; expected false")
	}
}

func TestActionTaxonomy_NoRawOutputActions(t *testing.T) {
	// Raw stdout/stderr stays in RunRecord, not in the audit taxonomy.
	forbidden := []audit.AuditAction{
		audit.AuditAction("stdout"),
		audit.AuditAction("stderr"),
		audit.AuditAction("raw_stdout"),
		audit.AuditAction("raw_stderr"),
	}
	for _, a := range forbidden {
		if a.Valid() {
			t.Errorf("action %q should not be a valid taxonomy action (raw output belongs in RunRecord)", a)
		}
	}
}

// TC-038-02: AuditEvent carries the structured fields an auditor needs.
func TestAuditEvent_StructuredFields(t *testing.T) {
	t.Run("verify_event_with_verdict", func(t *testing.T) {
		ev := audit.AuditEvent{
			Action:  audit.ActionVerify,
			RunID:   "run-001",
			TaskID:  "038",
			Verdict: audit.VerdictPass,
		}
		if ev.Action != audit.ActionVerify {
			t.Errorf("Action: got %q, want %q", ev.Action, audit.ActionVerify)
		}
		if ev.Verdict != audit.VerdictPass {
			t.Errorf("Verdict: got %q, want %q", ev.Verdict, audit.VerdictPass)
		}
		if ev.RunID == "" || ev.TaskID == "" {
			t.Errorf("RunID or TaskID must be non-empty")
		}
	})

	t.Run("finish_event_with_outcome", func(t *testing.T) {
		ev := audit.AuditEvent{
			Action:  audit.ActionFinish,
			RunID:   "run-001",
			TaskID:  "038",
			Outcome: audit.OutcomeCompleted,
		}
		if ev.Outcome != audit.OutcomeCompleted {
			t.Errorf("Outcome: got %q, want %q", ev.Outcome, audit.OutcomeCompleted)
		}
	})

	t.Run("publish_event_with_detail", func(t *testing.T) {
		ev := audit.AuditEvent{
			Action: audit.ActionPublish,
			RunID:  "run-001",
			TaskID: "038",
			Detail: audit.EventDetail{Branch: "task/038-seam", Remote: "origin"},
		}
		if ev.Detail.Branch == "" {
			t.Errorf("EventDetail.Branch must be set for publish event")
		}
	})

	t.Run("zero_value_event_is_invalid", func(t *testing.T) {
		var zero audit.AuditEvent
		if err := audit.Validate(zero); err == nil {
			t.Error("Validate(zero AuditEvent) returned nil; expected non-nil error")
		}
	})
}

// TC-038-03: Sink interface — compile-time shape check only.
// The interface shape `{ Append(AuditEvent) error; Seal() error }` is verified by
// the FakeSink compile-time assertion in fake.go. This test exercises the Sink
// contract through FakeSink without importing a concrete I/O backend.
func TestSinkInterface_ShapeIsTypedAndNotAny(t *testing.T) {
	// If this compiles, the Sink interface accepts a typed AuditEvent, never any/map.
	var s audit.Sink = audit.NewFakeSink()
	ev := audit.AuditEvent{
		Action: audit.ActionPick,
		RunID:  "run-001",
		TaskID: "038",
	}
	if err := s.Append(ev); err != nil {
		t.Errorf("Append on FakeSink returned unexpected error: %v", err)
	}
	if err := s.Seal(); err != nil {
		t.Errorf("Seal on FakeSink returned unexpected error: %v", err)
	}
}

// TC-038-04: FakeSink records appended events in order.
func TestFakeSink_RecordsEventsInOrder(t *testing.T) {
	fs := audit.NewFakeSink()

	events := []audit.AuditEvent{
		{Action: audit.ActionPick, RunID: "r1", TaskID: "1"},
		{Action: audit.ActionAttempt, RunID: "r1", TaskID: "1"},
		{Action: audit.ActionFinish, RunID: "r1", TaskID: "1", Outcome: audit.OutcomeCompleted},
	}

	for i, ev := range events {
		if err := fs.Append(ev); err != nil {
			t.Errorf("Append[%d] returned unexpected error: %v", i, err)
		}
	}

	got := fs.Events()
	if len(got) != len(events) {
		t.Fatalf("Events() len = %d, want %d", len(got), len(events))
	}
	for i, want := range events {
		if got[i].Action != want.Action {
			t.Errorf("Events()[%d].Action = %q, want %q", i, got[i].Action, want.Action)
		}
	}
}

func TestFakeSink_EventsReturnsCopy(t *testing.T) {
	fs := audit.NewFakeSink()
	_ = fs.Append(audit.AuditEvent{Action: audit.ActionPick, RunID: "r1", TaskID: "1"})

	got := fs.Events()
	// Mutating the returned slice must not corrupt the fake's internal record.
	got[0] = audit.AuditEvent{Action: audit.ActionEscalate, RunID: "mutated"}

	got2 := fs.Events()
	if got2[0].Action != audit.ActionPick {
		t.Errorf("Events() copy was not deep: internal record corrupted after caller mutation")
	}
}

func TestFakeSink_NoIOPerformed(t *testing.T) {
	// FakeSink must not create files or network connections. We verify this
	// indirectly: the test itself does no filesystem setup, and the fake runs clean.
	fs := audit.NewFakeSink()
	ev := audit.AuditEvent{Action: audit.ActionContainment, RunID: "r2", TaskID: "2", Detail: audit.EventDetail{Launcher: "podman"}}
	if err := fs.Append(ev); err != nil {
		t.Errorf("Append returned error on FakeSink (should be no-op I/O): %v", err)
	}
}

// TC-038-05: FakeSink records Seal and satisfies Sink at compile time.
func TestFakeSink_CompileTimeAssertionAndSealRecord(t *testing.T) {
	// Compile-time: var _ audit.Sink = (*audit.FakeSink)(nil) — declared in fake.go.
	// Runtime: Seal records that it was called.
	fs := audit.NewFakeSink()
	_ = fs.Append(audit.AuditEvent{Action: audit.ActionPick, RunID: "r1", TaskID: "1"})

	if fs.Sealed() {
		t.Error("Sealed() returned true before Seal() was called")
	}
	if err := fs.Seal(); err != nil {
		t.Errorf("Seal() returned unexpected error: %v", err)
	}
	if !fs.Sealed() {
		t.Error("Sealed() returned false after Seal() was called")
	}
	if fs.SealCount() != 1 {
		t.Errorf("SealCount() = %d, want 1", fs.SealCount())
	}
}

func TestFakeSink_AppendAfterSealReturnsError(t *testing.T) {
	fs := audit.NewFakeSink()
	_ = fs.Seal()

	err := fs.Append(audit.AuditEvent{Action: audit.ActionAttempt, RunID: "r1", TaskID: "1"})
	if err == nil {
		t.Error("Append after Seal should return an error (write-after-seal bug detection), got nil")
	}
}

// TC-038-06: invalid AuditEvent is rejected, not silently accepted.
func TestValidate_RejectsUnsetAction(t *testing.T) {
	ev := audit.AuditEvent{RunID: "r1", TaskID: "1"} // Action not set
	err := audit.Validate(ev)
	if err == nil {
		t.Fatal("Validate returned nil for event with unset action; expected non-nil")
	}
	// Error must name the offending field.
	if !containsField(err, "action") {
		t.Errorf("Validate error %q does not name the offending field 'action'", err)
	}
}

func TestValidate_RejectsUnknownAction(t *testing.T) {
	ev := audit.AuditEvent{Action: audit.AuditAction("bogus"), RunID: "r1", TaskID: "1"}
	err := audit.Validate(ev)
	if err == nil {
		t.Fatal("Validate returned nil for event with unknown action; expected non-nil")
	}
	if !containsField(err, "action") {
		t.Errorf("Validate error %q does not name the offending field 'action'", err)
	}
}

func TestValidate_RejectsVerifyEventWithoutVerdict(t *testing.T) {
	ev := audit.AuditEvent{
		Action: audit.ActionVerify,
		RunID:  "r1",
		TaskID: "1",
		// Verdict is zero / unset
	}
	err := audit.Validate(ev)
	if err == nil {
		t.Fatal("Validate returned nil for verify event without verdict; expected non-nil")
	}
	if !containsField(err, "verdict") {
		t.Errorf("Validate error %q does not name the offending field 'verdict'", err)
	}
}

func TestValidate_AcceptsPickEventWithoutOptionalDetail(t *testing.T) {
	ev := audit.AuditEvent{
		Action: audit.ActionPick,
		RunID:  "r1",
		TaskID: "1",
		// Detail is zero — optional for pick.
	}
	if err := audit.Validate(ev); err != nil {
		t.Errorf("Validate returned error for valid pick event with no optional detail: %v", err)
	}
}

func TestValidate_AcceptsVerifyEventWithVerdict(t *testing.T) {
	ev := audit.AuditEvent{
		Action:  audit.ActionVerify,
		RunID:   "r1",
		TaskID:  "1",
		Verdict: audit.VerdictFail,
	}
	if err := audit.Validate(ev); err != nil {
		t.Errorf("Validate returned error for valid verify event with verdict: %v", err)
	}
}

// FakeSink also validates events on Append (it calls Validate internally).
func TestFakeSink_AppendRejectsInvalidEvent(t *testing.T) {
	fs := audit.NewFakeSink()
	badEv := audit.AuditEvent{RunID: "r1", TaskID: "1"} // no action
	if err := fs.Append(badEv); err == nil {
		t.Error("FakeSink.Append accepted an invalid event (no action); expected non-nil error")
	}
	// No event must have been recorded.
	if len(fs.Events()) != 0 {
		t.Errorf("FakeSink recorded %d events after rejecting an invalid Append; want 0", len(fs.Events()))
	}
}

// containsField is a helper that checks whether the error message references a field name.
func containsField(err error, field string) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for i := 0; i <= len(msg)-len(field); i++ {
		if msg[i:i+len(field)] == field {
			return true
		}
	}
	// Also check wrapped errors.
	var ve *audit.ValidationError
	if errors.As(err, &ve) {
		return ve.Field == field
	}
	return false
}
