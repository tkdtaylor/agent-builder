// Package audit declares the AuditEvent action taxonomy, the Sink seam interface,
// and event validation helpers used by the supervisor and adapter layers.
//
// This package is a strict leaf — it imports no executor, LLM, or web-fetch
// packages so that the F-005 fitness check stays trivially green. The production
// backend is reached through the Sink seam (see internal/audit/blocksink for the
// BlockSink adapter that maps events onto the audit-trail CLI block); tests use
// FakeSink with zero I/O.
//
// Governing ADR: docs/architecture/decisions/026-audit-trail-consume-shipped-block.md
package audit

import (
	"errors"
	"fmt"
)

// AuditAction is a typed, closed-enum action constant that names one lifecycle
// event the agent-builder run loop emits. The set is fixed — unknown string
// values do not become valid actions.
type AuditAction string

// Valid lifecycle actions. These map directly onto the verbs the run loop
// already emits as command-log lines in the 019 RunRecord:
//
//	containment=podman, pick task, attempt, verify, publish branch, escalated, finish … outcome=…
//
// Raw stdout/stderr stay in the RunRecord; they do not appear in this taxonomy.
const (
	ActionContainment    AuditAction = "containment"
	ActionPick           AuditAction = "pick"
	ActionAttempt        AuditAction = "attempt"
	ActionVerify         AuditAction = "verify"
	ActionPublish        AuditAction = "publish"
	ActionEscalate       AuditAction = "escalate"
	ActionFinish         AuditAction = "finish"
	ActionPolicyDecision AuditAction = "policy-decision"
)

// validActions is the closed set of known actions. Used by Valid() and Validate.
var validActions = map[AuditAction]struct{}{
	ActionContainment:    {},
	ActionPick:           {},
	ActionAttempt:        {},
	ActionVerify:         {},
	ActionPublish:        {},
	ActionEscalate:       {},
	ActionFinish:         {},
	ActionPolicyDecision: {},
}

// Valid reports whether a is a known, non-empty action in the closed enum.
func (a AuditAction) Valid() bool {
	_, ok := validActions[a]
	return ok
}

// String returns the string representation of the action for use in NDJSON or
// log output. It is identical to the constant value.
func (a AuditAction) String() string {
	return string(a)
}

// AuditVerdict is the typed result of a verify action.
type AuditVerdict string

const (
	VerdictPass AuditVerdict = "pass"
	VerdictFail AuditVerdict = "fail"
)

// Valid reports whether v is a known, non-empty verdict.
func (v AuditVerdict) Valid() bool {
	return v == VerdictPass || v == VerdictFail
}

// AuditOutcome is the typed terminal result of a finish action.
type AuditOutcome string

const (
	OutcomeCompleted AuditOutcome = "completed"
	OutcomeFailed    AuditOutcome = "failed"
	OutcomeTimedOut  AuditOutcome = "timed-out"
)

// EventDetail carries optional structured context for actions that benefit from
// additional named fields. Not all fields are relevant for every action — only
// the non-zero fields are meaningful. Using named fields instead of
// map[string]any means the call site is compile-checked.
type EventDetail struct {
	// Launcher is the containment launcher path/name (relevant for containment events).
	Launcher string
	// Branch is the executor-produced branch (relevant for publish events).
	Branch string
	// Remote is the git remote for branch publication (relevant for publish events).
	Remote string
	// Attempt is the 1-based attempt number (relevant for attempt/escalate events).
	Attempt int
	// PolicyDecision is the policy engine decision string ("allow", "deny", or
	// "require_approval"). Only set for ActionPolicyDecision events (task 073).
	PolicyDecision string
	// PolicyReason is the human-readable reason returned by the policy engine.
	// Only set for ActionPolicyDecision events (task 073).
	PolicyReason string
}

// AuditEvent is the structured, typed event that the supervisor writes through
// the Sink seam for each lifecycle action. The Action field is required;
// Verdict is required for ActionVerify; Outcome is optional for ActionFinish.
// All other fields are populated as appropriate by the call site.
type AuditEvent struct {
	// Action is the lifecycle action (required).
	Action AuditAction
	// RunID is the run correlation identifier (e.g. "NNN/box-NNN").
	RunID string
	// TaskID is the task being acted on.
	TaskID string
	// Verdict is the gate verdict for ActionVerify events.
	Verdict AuditVerdict
	// Outcome is the run outcome for ActionFinish events.
	Outcome AuditOutcome
	// Detail carries optional typed structured context for the action.
	Detail EventDetail
}

// Sink is the audit sink seam. It mirrors the sandbox.Runner seam shape:
// a small typed interface the supervisor can depend on without importing a
// concrete backend. The production implementation is BlockSink (task 039);
// tests use FakeSink.
type Sink interface {
	// Append records one audit event. It must return a non-nil error for an
	// invalid event (as determined by Validate) rather than writing a malformed
	// record.
	Append(AuditEvent) error
	// Seal flushes and closes the audit channel. It must return a non-nil error
	// if the flush or close fails, so callers can observe the failure rather
	// than swallow it silently.
	Seal() error
}

// ValidationError is the structured error returned by Validate when an event
// fails validation. The Field name identifies the offending field.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("audit: invalid event field %q: %s", e.Field, e.Message)
}

// ErrAfterSeal is returned by FakeSink.Append when Seal has already been called.
var ErrAfterSeal = errors.New("audit: Append called after Seal")

// Validate checks that ev is a well-formed AuditEvent. It returns a
// *ValidationError naming the offending field for the first validation failure,
// or nil when the event is valid.
//
// Rules:
//   - Action must be a valid, known enum member.
//   - ActionVerify events must carry a non-empty Verdict.
func Validate(ev AuditEvent) error {
	if !ev.Action.Valid() {
		return &ValidationError{
			Field:   "action",
			Message: fmt.Sprintf("must be one of {containment, pick, attempt, verify, publish, escalate, finish, policy-decision}, got %q", ev.Action),
		}
	}
	if ev.Action == ActionVerify && !ev.Verdict.Valid() {
		return &ValidationError{
			Field:   "verdict",
			Message: fmt.Sprintf("must be 'pass' or 'fail' for verify events, got %q", ev.Verdict),
		}
	}
	return nil
}
