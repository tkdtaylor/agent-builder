package authz

import (
	"path/filepath"
	"testing"
)

// pairingStore builds a store with the given raw IDs pre-approved.
func pairingStore(t *testing.T, ids ...string) *Store {
	t.Helper()
	s := NewStore(filepath.Join(t.TempDir(), "approved.json"))
	for _, id := range ids {
		if err := s.Add(id); err != nil {
			t.Fatalf("Add(%s): %v", id, err)
		}
	}
	return s
}

// parseApproveDeny structural-grammar coverage (feeds TC-152-07's edge case): the match
// is verb + single numeric arg, not a bare "approve" string prefix.
func TestParseApproveDeny_Grammar(t *testing.T) {
	cases := []struct {
		text    string
		isVerb  bool
		deny    bool
		validID bool
		id      int64
	}{
		{"approve 77", true, false, true, 77},
		{"deny 77", true, true, true, 77},
		{"APPROVE 042", true, false, true, 42}, // case-insensitive verb, normalized ID
		{"approve", true, false, false, 0},     // verb only, no ID → malformed
		{"deny", true, true, false, 0},         // verb only, no ID → malformed
		{"approve foo", true, false, false, 0}, // non-numeric arg → malformed
		{"approve 77 please", true, false, false, 0}, // extra token → malformed
		{"approve of this plan", true, false, false, 0}, // prose starting with verb → malformed, NOT valid
		{"status 77", false, false, false, 0},           // different verb → not the grammar
		{"build me a thing", false, false, false, 0},    // not the grammar
		{"", false, false, false, 0},                    // empty
	}
	for _, tc := range cases {
		g := parseApproveDeny(tc.text)
		if g.isVerb != tc.isVerb || g.deny != tc.deny || g.validID != tc.validID || (tc.validID && g.id != tc.id) {
			t.Errorf("parseApproveDeny(%q) = %+v, want isVerb=%v deny=%v validID=%v id=%d",
				tc.text, g, tc.isVerb, tc.deny, tc.validID, tc.id)
		}
	}
}

// TC-152-05 core (LOAD-BEARING at the decision seam): a stranger's "approve <own-id>"
// must NOT be an approve action — it is Pending. This is the anti-self-approval gate at
// its narrowest, purely-logical form. Mutation check: if the owner gate were dropped
// (isOwner replaced by true, or the check moved after grammar-parse), this asserts
// Pending, so such a mutation FAILS this test.
func TestDecidePairing_StrangerCannotSelfApprove(t *testing.T) {
	store := pairingStore(t) // empty — 77 is unknown
	const owner = int64(1)

	d := DecidePairing("77", owner, "approve 77", store)
	if d.Action != ActionPairingPending {
		t.Fatalf("stranger approve<own-id> action = %v, want ActionPairingPending (owner gate must hold)", d.Action)
	}
	if d.Reason != ReasonPairingRequest {
		t.Errorf("stranger approve<own-id> reason = %q, want %q", d.Reason, ReasonPairingRequest)
	}
	// A stranger "deny 77" (denying its own request) is likewise ordinary pending input.
	if d := DecidePairing("77", owner, "deny 77", store); d.Action != ActionPairingPending {
		t.Errorf("stranger deny<own-id> action = %v, want ActionPairingPending", d.Action)
	}
	// Sanity: the SAME text from the OWNER is an approve action (proving the gate is the
	// sender identity, not the text).
	if d := DecidePairing("1", owner, "approve 77", store); d.Action != ActionPairingApprove || d.TargetID != 77 {
		t.Errorf("owner approve 77 = %+v, want {ActionPairingApprove, target 77}", d)
	}
}

// Owner approve/deny/malformed precedence, and owner's ordinary command falls through.
func TestDecidePairing_OwnerBranches(t *testing.T) {
	store := pairingStore(t)
	const owner = int64(1)

	if d := DecidePairing("1", owner, "approve 77", store); d.Action != ActionPairingApprove || d.TargetID != 77 || d.Reason != ReasonPairingApproved {
		t.Errorf("owner approve = %+v, want {ActionPairingApprove, 77, pairing_approved}", d)
	}
	if d := DecidePairing("1", owner, "deny 77", store); d.Action != ActionPairingDeny || d.TargetID != 77 || d.Reason != ReasonPairingDenied {
		t.Errorf("owner deny = %+v, want {ActionPairingDeny, 77, pairing_denied}", d)
	}
	// Malformed owner grammar → ActionPairingMalformed (does NOT fall through to routing).
	for _, bad := range []string{"approve", "approve foo", "deny", "deny xyz"} {
		if d := DecidePairing("1", owner, bad, store); d.Action != ActionPairingMalformed || d.Reason != ReasonPairingMalformed {
			t.Errorf("owner malformed %q = %+v, want {ActionPairingMalformed, pairing_malformed}", bad, d)
		}
	}
	// Owner's ordinary (non-approve/deny) command falls through — NOT intercepted. The
	// owner is not pre-approved here, so it hits Pending (owner identity alone ≠ approved).
	if d := DecidePairing("1", owner, "status", store); d.Action != ActionPairingPending {
		t.Errorf("owner ordinary 'status' (unapproved) = %v, want ActionPairingPending (not intercepted by grammar)", d.Action)
	}
	// Owner's prose starting with "approve" is malformed grammar (verb, no numeric ID) —
	// it is consumed by the owner branch as malformed, not routed. (TC-152-07 edge.)
	if d := DecidePairing("1", owner, "approve of this plan", store); d.Action != ActionPairingMalformed {
		t.Errorf("owner 'approve of this plan' = %v, want ActionPairingMalformed", d.Action)
	}
}

// An already-approved sender (including the owner once approved) gets the normal
// plaintext path, bypassing the pairing machinery.
func TestDecidePairing_ApprovedSenderAcceptsPlaintext(t *testing.T) {
	store := pairingStore(t, "77")
	const owner = int64(1)

	if d := DecidePairing("77", owner, "status", store); d.Action != ActionAcceptPlaintext || d.Reason != ReasonPlaintextAccepted {
		t.Errorf("approved sender = %+v, want {ActionAcceptPlaintext, plaintext_accepted}", d)
	}
	// Cross-format normalization: "042" approved, "42" queried, still accepted.
	store2 := pairingStore(t, "042")
	if d := DecidePairing("42", owner, "status", store2); d.Action != ActionAcceptPlaintext {
		t.Errorf("approved cross-format sender = %v, want ActionAcceptPlaintext", d.Action)
	}
}

// A nil store defensively routes every sender to Pending (no one approved), and a
// malformed sender ID (non-numeric) can never be the owner.
func TestDecidePairing_Defensive(t *testing.T) {
	const owner = int64(1)
	if d := DecidePairing("77", owner, "status", nil); d.Action != ActionPairingPending {
		t.Errorf("nil-store unknown sender = %v, want ActionPairingPending", d.Action)
	}
	// Non-numeric sender ID cannot equal the numeric owner ID → not the owner branch.
	if d := DecidePairing("not-a-number", owner, "approve 77", pairingStore(t)); d.Action != ActionPairingPending {
		t.Errorf("malformed sender approve = %v, want ActionPairingPending (never owner)", d.Action)
	}
}
