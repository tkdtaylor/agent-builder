package cli

// TC-152-09 — pairing-mode owner-ID config validation (task 152, ADR 063 Decision 3).
//
// Exercises assembleTelegramOwnerID directly (the config-validation seam): an owner-less
// or non-numeric OWNER_ID in pairing mode is a fail-fast ExitUsage error; a valid numeric
// value succeeds and is normalized; non-pairing modes ignore the owner ID entirely.

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
)

// TC-152-09: pairing mode with OWNER_ID unset/blank fails assembly; a valid numeric value
// succeeds. A non-numeric value also fails fast (normalization applies to the owner ID).
func TestTC152_09_PairingOwnerIDRequiredAndNormalized(t *testing.T) {
	// (a) unset OWNER_ID in pairing mode → fail-fast usage-config error.
	if _, err := assembleTelegramOwnerID(envFrom(map[string]string{}), authz.ModePairing); err == nil {
		t.Fatal("pairing mode with unset OWNER_ID returned nil error, want fail-fast")
	} else if !isUsageConfig(err) {
		t.Errorf("unset OWNER_ID err = %v, want errUsageConfig (ExitUsage)", err)
	}

	// (b) blank/whitespace OWNER_ID in pairing mode → fail-fast.
	for _, blank := range []string{"", "   ", "\t"} {
		if _, err := assembleTelegramOwnerID(envFrom(map[string]string{
			EnvTelegramOwnerID: blank,
		}), authz.ModePairing); err == nil {
			t.Errorf("pairing mode with blank OWNER_ID %q returned nil error, want fail-fast", blank)
		}
	}

	// (c) non-numeric OWNER_ID in pairing mode → fail-fast (normalization rejects it).
	for _, bad := range []string{"not-a-number", "1.5", "0x10", "abc"} {
		if _, err := assembleTelegramOwnerID(envFrom(map[string]string{
			EnvTelegramOwnerID: bad,
		}), authz.ModePairing); err == nil {
			t.Errorf("pairing mode with non-numeric OWNER_ID %q returned nil error, want fail-fast", bad)
		} else if !isUsageConfig(err) {
			t.Errorf("non-numeric OWNER_ID %q err = %v, want errUsageConfig", bad, err)
		}
	}

	// (d) valid numeric OWNER_ID in pairing mode → success, normalized to canonical form.
	got, err := assembleTelegramOwnerID(envFrom(map[string]string{
		EnvTelegramOwnerID: " 042 ", // leading zeros + whitespace normalize to 42
	}), authz.ModePairing)
	if err != nil {
		t.Fatalf("valid OWNER_ID err = %v, want nil", err)
	}
	if got != 42 {
		t.Errorf("normalized OWNER_ID = %d, want 42 (canonical numeric form)", got)
	}

	// (e) non-pairing modes ignore OWNER_ID entirely — unset is fine, value is not consulted.
	for _, m := range []authz.Mode{authz.ModeEnvelope, authz.ModeAllowlist, authz.ModeOpen, authz.ModeDisabled} {
		id, err := assembleTelegramOwnerID(envFrom(map[string]string{}), m)
		if err != nil {
			t.Errorf("mode %q with unset OWNER_ID err = %v, want nil (owner ID irrelevant)", m, err)
		}
		if id != 0 {
			t.Errorf("mode %q owner ID = %d, want 0 (not consulted)", m, id)
		}
	}
}
