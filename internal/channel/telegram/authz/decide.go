package authz

// Action is the outcome of the per-update mode decision (ADR 063 Decision 5). It tells
// the adapter's Next() loop which branch to take for this update BEFORE it reaches the
// envelope pipeline — keeping the crypto path (envelope mode) and the plaintext path
// (sender-ID modes) as two clearly separated branches rather than smearing sender-ID
// logic through the envelope pipeline.
type Action int

const (
	// ActionRouteEnvelope: run the update through envelope.VerifyAndOpen exactly as
	// today (envelope mode). The sender ID is not consulted.
	ActionRouteEnvelope Action = iota
	// ActionAcceptPlaintext: the sender-ID gate accepted this sender; treat Message.Text
	// as plaintext and run it through the SAME armor + size-cap + audit pipeline the
	// envelope path uses (after opening) before deriving the message. Never skips armor.
	ActionAcceptPlaintext
	// ActionRejectDisabled: the channel is inert (disabled mode); reject before any parse.
	ActionRejectDisabled
	// ActionRejectUnapproved: a sender-ID mode rejected this sender (not in the approved
	// set, or a malformed/non-numeric sender ID). Reject before armor.
	ActionRejectUnapproved
)

// AuditReason is the audit Detail.Reason string an adapter emits for a Decision. Each
// distinct rejection carries a distinct reason so the audit chain classifies the mode
// gate precisely (ADR 063 Decision 2 — every accept/reject decision is audited).
type AuditReason string

const (
	// ReasonChannelDisabled classifies a disabled-mode rejection.
	ReasonChannelDisabled AuditReason = "channel_disabled"
	// ReasonSenderNotApproved classifies a sender-ID-mode rejection (unapproved sender
	// or malformed sender ID).
	ReasonSenderNotApproved AuditReason = "sender_not_approved"
	// ReasonPlaintextAccepted classifies an accepted plaintext update on a sender-ID
	// path (the accept-side audit event).
	ReasonPlaintextAccepted AuditReason = "plaintext_accepted"
)

// Decision is the result of Decide: the Action the adapter takes, plus the AuditReason
// to emit. For ActionRouteEnvelope the Reason is empty (the envelope path emits its own
// reasons via the existing classifier).
type Decision struct {
	Action Action
	Reason AuditReason
}

// Decide is the mode-decision seam (ADR 063 Decision 5). Given the selected mode, the
// inbound sender ID (raw wire form), and the approved-sender store, it returns the
// branch the adapter should take BEFORE the envelope pipeline.
//
//   - envelope           → ActionRouteEnvelope (sender ID ignored, VerifyAndOpen runs).
//   - disabled           → ActionRejectDisabled (rejected before any parse).
//   - allowlist/pairing  → ActionAcceptPlaintext iff the normalized sender ID is in the
//     store; otherwise ActionRejectUnapproved. A malformed (non-numeric) sender ID is
//     ActionRejectUnapproved, never an accept. (pairing's unknown-sender in-chat flow is
//     task 152; here an unknown sender is a plain reject.)
//   - open               → ActionAcceptPlaintext unconditionally (task 153 adds the
//     startup warning; the decision itself accepts every sender here).
//
// store may be nil for modes that do not consult it (envelope, disabled, open); Decide
// only dereferences store for allowlist/pairing.
func Decide(mode Mode, rawSenderID string, store *Store) Decision {
	switch mode {
	case ModeDisabled:
		return Decision{Action: ActionRejectDisabled, Reason: ReasonChannelDisabled}

	case ModeOpen:
		return Decision{Action: ActionAcceptPlaintext, Reason: ReasonPlaintextAccepted}

	case ModeAllowlist, ModePairing:
		if store == nil {
			return Decision{Action: ActionRejectUnapproved, Reason: ReasonSenderNotApproved}
		}
		approved, err := store.Contains(rawSenderID)
		if err != nil || !approved {
			return Decision{Action: ActionRejectUnapproved, Reason: ReasonSenderNotApproved}
		}
		return Decision{Action: ActionAcceptPlaintext, Reason: ReasonPlaintextAccepted}

	default: // ModeEnvelope and any unexpected value resolve to the strong default.
		return Decision{Action: ActionRouteEnvelope}
	}
}
