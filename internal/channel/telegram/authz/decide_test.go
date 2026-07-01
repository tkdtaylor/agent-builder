package authz

import (
	"go/build"
	"path/filepath"
	"strings"
	"testing"
)

// TC-151-07 (enum-validation slice owned by this package): all five recognized values
// parse; unset ⇒ envelope (default, never an unknown-value error); empty string is
// treated as unset, not unknown; an unrecognized value is a fail-fast ErrUnknownMode.
func TestTC151_07_ParseModeEnum(t *testing.T) {
	recognized := map[string]Mode{
		"envelope":  ModeEnvelope,
		"allowlist": ModeAllowlist,
		"pairing":   ModePairing,
		"open":      ModeOpen,
		"disabled":  ModeDisabled,
	}
	for raw, want := range recognized {
		got, err := ParseMode(raw)
		if err != nil {
			t.Errorf("ParseMode(%q) err = %v, want nil", raw, err)
		}
		if got != want {
			t.Errorf("ParseMode(%q) = %q, want %q", raw, got, want)
		}
	}

	// Unset / empty / whitespace ⇒ envelope, no error.
	for _, raw := range []string{"", "   ", "\t"} {
		got, err := ParseMode(raw)
		if err != nil {
			t.Errorf("ParseMode(%q) err = %v, want nil (unset ⇒ envelope)", raw, err)
		}
		if got != ModeEnvelope {
			t.Errorf("ParseMode(%q) = %q, want envelope", raw, got)
		}
	}

	// Unknown value ⇒ fail-fast error.
	if _, err := ParseMode("bogus-mode"); err == nil {
		t.Error(`ParseMode("bogus-mode") err = nil, want ErrUnknownMode`)
	}
}

// ConsultsStore: only allowlist and pairing read the approved store.
func TestConsultsStore(t *testing.T) {
	consults := map[Mode]bool{
		ModeEnvelope:  false,
		ModeDisabled:  false,
		ModeOpen:      false,
		ModeAllowlist: true,
		ModePairing:   true,
	}
	for m, want := range consults {
		if got := m.ConsultsStore(); got != want {
			t.Errorf("%q.ConsultsStore() = %v, want %v", m, got, want)
		}
	}
}

// Decide covers the mode-decision seam: envelope routes to the crypto path (sender
// ignored), disabled rejects, allowlist accepts approved / rejects unapproved and
// malformed, open accepts unconditionally.
func TestDecide(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "s.json"))
	_ = store.Add("42")

	// envelope: route to envelope pipeline, sender ID irrelevant.
	if d := Decide(ModeEnvelope, "99", nil); d.Action != ActionRouteEnvelope {
		t.Errorf("envelope Decide action = %v, want ActionRouteEnvelope", d.Action)
	}

	// disabled: reject before parse.
	if d := Decide(ModeDisabled, "42", nil); d.Action != ActionRejectDisabled || d.Reason != ReasonChannelDisabled {
		t.Errorf("disabled Decide = %+v, want {ActionRejectDisabled, channel_disabled}", d)
	}

	// allowlist approved: accept plaintext.
	if d := Decide(ModeAllowlist, "042", store); d.Action != ActionAcceptPlaintext || d.Reason != ReasonPlaintextAccepted {
		t.Errorf("allowlist approved Decide = %+v, want {ActionAcceptPlaintext, plaintext_accepted}", d)
	}
	// allowlist unapproved: reject.
	if d := Decide(ModeAllowlist, "99", store); d.Action != ActionRejectUnapproved || d.Reason != ReasonSenderNotApproved {
		t.Errorf("allowlist unapproved Decide = %+v, want {ActionRejectUnapproved, sender_not_approved}", d)
	}
	// allowlist malformed sender ID: reject, never accept.
	if d := Decide(ModeAllowlist, "not-a-number", store); d.Action != ActionRejectUnapproved {
		t.Errorf("allowlist malformed Decide action = %v, want ActionRejectUnapproved", d.Action)
	}
	// allowlist with nil store: reject (defensive).
	if d := Decide(ModeAllowlist, "42", nil); d.Action != ActionRejectUnapproved {
		t.Errorf("allowlist nil-store Decide action = %v, want ActionRejectUnapproved", d.Action)
	}

	// open: accept any sender unconditionally.
	if d := Decide(ModeOpen, "99", nil); d.Action != ActionAcceptPlaintext {
		t.Errorf("open Decide action = %v, want ActionAcceptPlaintext", d.Action)
	}
}

// Isolation invariant (ADR 063 Decision 5): the authz package imports stdlib only —
// no agent-builder/internal/ import except (permitted) internal/envelope. In particular
// it MUST NOT import internal/supervisor (F-003) or reach any crypto/transport. This is
// the source-grep the test-spec notes call for in lieu of a dedicated make fitness target.
func TestAuthzIsStdlibLeaf(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("build.ImportDir: %v", err)
	}

	const modulePrefix = "github.com/tkdtaylor/agent-builder/internal/"
	const allowed = modulePrefix + "envelope" // the one permitted same-side leaf

	for _, imp := range pkg.Imports {
		if !strings.HasPrefix(imp, "github.com/tkdtaylor/agent-builder/") {
			continue // stdlib or third-party — fine
		}
		if imp == allowed || strings.HasPrefix(imp, allowed+"/") {
			continue
		}
		t.Errorf("authz imports forbidden internal package %q (ADR 063 Decision 5: stdlib-only leaf, at most internal/envelope)", imp)
	}
}
