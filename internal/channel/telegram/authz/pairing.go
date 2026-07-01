package authz

import "strings"

// This file adds the pairing-mode decision (ADR 063 Decision 3): the owner-gated
// approve/deny grammar and the unknown-sender pending flow. It is a stdlib-only
// extension of the mode-decision seam in decide.go — no supervisor, no crypto.
//
// The load-bearing ordering rule (ADR 063 Decision 3): the owner gate is checked on
// the SENDER-ID IDENTITY first, and only then is the text inspected for the
// approve/deny grammar. A stranger's "approve <own-id>" therefore never reaches the
// grammar branch — it is treated as ordinary unapproved input and routed to the
// pending path. This ordering is the anti-self-approval control (TC-152-05).

// Pairing-mode actions extend the base decide.go action set. They are the branches the
// adapter's Next() takes for a pairing-mode update, decided BEFORE deriveMessage.
const (
	// ActionPairingPending: an unknown (unapproved, non-owner) sender in pairing mode.
	// The adapter replies "pending" to the sender, notifies the owner's chat with the
	// approve/deny instruction, emits a pairing_request audit event, and derives no
	// supervisor.Message. A stranger's "approve <id>" text lands here (owner-gate holds).
	ActionPairingPending Action = iota + 100
	// ActionPairingApprove: the OWNER sent a well-formed "approve <numeric-id>". The
	// adapter adds the target ID to the store, persists, audits, confirms to the owner,
	// and derives no message. Decision.TargetID carries the ID to approve.
	ActionPairingApprove
	// ActionPairingDeny: the OWNER sent a well-formed "deny <numeric-id>". The adapter
	// audits the denial, confirms to the owner, does NOT mutate the store, and derives no
	// message. Decision.TargetID carries the ID that was denied.
	ActionPairingDeny
	// ActionPairingMalformed: the OWNER sent "approve"/"deny" verb text with a missing or
	// non-numeric ID argument. The adapter audits a malformed-grammar event, does NOT
	// mutate the store, and derives no message (it does not fall through to command
	// routing). This keeps a fat-fingered owner command from leaking into deriveMessage.
	ActionPairingMalformed
)

// Pairing-mode audit reasons (ADR 063 Decision 2: every accept/reject/flow decision is
// audited with a distinct, typed reason). These are Detail.Reason values on the existing
// ActionChannelReject audit action, matching the pattern task 151 established for the
// sender-ID gate — keeping the closed audit enum unchanged while classifying precisely.
const (
	// ReasonPairingRequest classifies an unknown-sender pending request (owner notified).
	ReasonPairingRequest AuditReason = "pairing_request"
	// ReasonPairingApproved classifies an owner-approved sender addition.
	ReasonPairingApproved AuditReason = "pairing_approved"
	// ReasonPairingDenied classifies an owner denial (no store mutation).
	ReasonPairingDenied AuditReason = "pairing_denied"
	// ReasonPairingMalformed classifies an owner approve/deny with a missing/bad ID.
	ReasonPairingMalformed AuditReason = "pairing_malformed"
)

// approveDenyGrammar is the parsed result of an owner approve/deny attempt.
type approveDenyGrammar struct {
	// isVerb reports whether the text's first word is exactly "approve" or "deny"
	// (case-insensitive), i.e. the message is SHAPED like an approve/deny command. A
	// bare "approve of this plan" has isVerb=true only if the first token is exactly
	// "approve"/"deny" — but validID distinguishes a well-formed command from a
	// malformed one. "approve of ..." has isVerb=true, validID=false (malformed).
	isVerb bool
	// deny reports whether the verb was "deny" (vs "approve").
	deny bool
	// validID reports whether the argument normalized to a canonical numeric ID.
	validID bool
	// id is the normalized target ID, meaningful only when validID is true.
	id int64
}

// parseApproveDeny inspects text for the "approve <numeric-id>" / "deny <numeric-id>"
// grammar. It is a STRUCTURAL match (verb token + numeric-argument), not a bare string
// prefix: "approve of this plan" matches the verb but has no numeric ID, so validID is
// false (a malformed command, per TC-152-07's edge case — it must NOT be treated as a
// valid approval, but it also must not silently mutate anything).
func parseApproveDeny(text string) approveDenyGrammar {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return approveDenyGrammar{}
	}
	verb := strings.ToLower(fields[0])
	var g approveDenyGrammar
	switch verb {
	case "approve":
		g.isVerb = true
	case "deny":
		g.isVerb, g.deny = true, true
	default:
		return approveDenyGrammar{} // not the grammar at all — falls through to deriveMessage.
	}

	// Require exactly one argument that normalizes to a canonical numeric ID. Extra
	// tokens ("approve 77 please") or a non-numeric argument ("approve foo") are
	// malformed — verb matched, but no valid single numeric ID.
	if len(fields) != 2 {
		return g // isVerb=true, validID=false → malformed.
	}
	id, err := Normalize(fields[1])
	if err != nil {
		return g // isVerb=true, validID=false → malformed.
	}
	g.validID = true
	g.id = id
	return g
}

// DecidePairing is the pairing-mode decision (ADR 063 Decision 3). It gates the
// approve/deny grammar on the OWNER's sender-ID identity BEFORE inspecting the text, then
// falls through to the ordinary approved/unapproved plaintext decision.
//
// Precedence (the ordering is load-bearing):
//  1. sender == owner AND text is approve/deny-shaped → Approve / Deny (valid ID) or
//     Malformed (missing/non-numeric ID). The owner's approve/deny NEVER reaches
//     deriveMessage.
//  2. sender == owner AND text is NOT approve/deny-shaped → fall through to (3): the
//     owner's ordinary commands ("status", a new goal) route normally. Owner identity
//     alone does not skip command routing — only approve/deny-shaped text is intercepted.
//  3. sender is approved in the store → ActionAcceptPlaintext (normal task-151 pipeline).
//  4. otherwise (unknown sender, INCLUDING a stranger's "approve <id>") →
//     ActionPairingPending. A stranger's approve/deny text is ordinary unapproved input;
//     the owner gate in step 1 already excluded it, so it can never self-approve.
//
// ownerID is the configured, already-normalized owner sender ID. store must be non-nil in
// pairing mode (config validation guarantees a store for store-consulting modes); a nil
// store defensively routes every sender to Pending (no one is approved).
func DecidePairing(rawSenderID string, ownerID int64, text string, store *Store) Decision {
	senderID, err := Normalize(rawSenderID)
	isOwner := err == nil && senderID == ownerID

	// (1)/(2) Owner gate FIRST, on identity — before the text is inspected for the verb.
	if isOwner {
		if g := parseApproveDeny(text); g.isVerb {
			if !g.validID {
				return Decision{Action: ActionPairingMalformed, Reason: ReasonPairingMalformed}
			}
			if g.deny {
				return Decision{Action: ActionPairingDeny, Reason: ReasonPairingDenied, TargetID: g.id}
			}
			return Decision{Action: ActionPairingApprove, Reason: ReasonPairingApproved, TargetID: g.id}
		}
		// Owner's non-approve/deny text falls through to the normal command path below.
	}

	// (3) Already-approved sender (owner included, once approved) → normal plaintext path.
	if store != nil {
		approved, cerr := store.Contains(rawSenderID)
		if cerr == nil && approved {
			return Decision{Action: ActionAcceptPlaintext, Reason: ReasonPlaintextAccepted}
		}
	}

	// (4) Unknown sender (or a stranger's approve/deny text) → pending/owner-notify flow.
	return Decision{Action: ActionPairingPending, Reason: ReasonPairingRequest}
}
