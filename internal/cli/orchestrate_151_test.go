package cli

// TC-151-07/09/10 — Telegram auth-mode config plumbing (task 151, ADR 063).
//
// These exercise assembleTelegramAuthMode directly (the config-validation seam) without
// needing the full crypto env block, mirroring how TC-117-04 exercises the inbound
// selector. Fail-fast means an errUsageConfig-wrapped error (ExitUsage), never a panic.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
)

// envFrom builds a getenv func from a map (unset keys ⇒ "").
func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// TC-151-07: an unrecognized AUTH_MODE value fails assembly with an error (not a panic);
// all five recognized values pass the config-layer validation.
func TestTC151_07_UnknownModeFailsFast_RecognizedPass(t *testing.T) {
	// Unknown value ⇒ fail-fast usage-config error, no panic, no store returned.
	mode, store, err := assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode: "bogus-mode",
	}))
	if err == nil {
		t.Fatal("unknown AUTH_MODE returned nil error, want fail-fast")
	}
	if !isUsageConfig(err) {
		t.Errorf("unknown AUTH_MODE err = %v, want errUsageConfig (ExitUsage)", err)
	}
	if mode != "" || store != nil {
		t.Errorf("unknown AUTH_MODE returned mode=%q store=%v, want empty/nil", mode, store)
	}

	// All five recognized values pass config-layer validation. allowlist/pairing need a
	// (valid, writable) store path; envelope/open/disabled do not.
	dir := t.TempDir()
	for _, m := range []authz.Mode{
		authz.ModeEnvelope, authz.ModeAllowlist, authz.ModePairing, authz.ModeOpen, authz.ModeDisabled,
	} {
		env := map[string]string{EnvTelegramAuthMode: string(m)}
		if m.ConsultsStore() {
			env[EnvTelegramApprovedStore] = filepath.Join(dir, string(m)+".json")
		}
		gotMode, _, err := assembleTelegramAuthMode(envFrom(env))
		if err != nil {
			t.Errorf("recognized mode %q failed validation: %v", m, err)
			continue
		}
		if gotMode != m {
			t.Errorf("mode %q resolved to %q", m, gotMode)
		}
	}

	// Empty string is treated as unset ⇒ envelope, not an unknown value.
	gotMode, gotStore, err := assembleTelegramAuthMode(envFrom(map[string]string{}))
	if err != nil {
		t.Fatalf("unset AUTH_MODE err = %v, want nil (⇒ envelope)", err)
	}
	if gotMode != authz.ModeEnvelope || gotStore != nil {
		t.Errorf("unset AUTH_MODE ⇒ mode=%q store=%v, want envelope/nil", gotMode, gotStore)
	}
}

// TC-151-09: a blank APPROVED_STORE path in allowlist/pairing is a fail-fast config
// error; envelope/disabled with a blank path succeed.
func TestTC151_09_BlankStorePathFailsForConsultingModes(t *testing.T) {
	for _, m := range []authz.Mode{authz.ModeAllowlist, authz.ModePairing} {
		_, _, err := assembleTelegramAuthMode(envFrom(map[string]string{
			EnvTelegramAuthMode: string(m),
			// EnvTelegramApprovedStore unset/blank
		}))
		if err == nil {
			t.Errorf("%q with blank store path returned nil error, want fail-fast", m)
			continue
		}
		if !isUsageConfig(err) {
			t.Errorf("%q blank-store err = %v, want errUsageConfig", m, err)
		}
	}

	// envelope + disabled with a blank path succeed (they never consult the store).
	for _, m := range []authz.Mode{authz.ModeEnvelope, authz.ModeDisabled} {
		_, store, err := assembleTelegramAuthMode(envFrom(map[string]string{
			EnvTelegramAuthMode: string(m),
		}))
		if err != nil {
			t.Errorf("%q with blank store path err = %v, want nil", m, err)
		}
		if store != nil {
			t.Errorf("%q built a store, want nil (never consults the store)", m)
		}
	}
}

// TC-151-09 edge: a store path pointing at a not-yet-existing file is NOT an error (the
// file is created on Persist); an existing-but-unwritable path (read-only parent dir) IS
// a fail-fast error at assembly.
func TestTC151_09_NonexistentPathOK_UnwritableFails(t *testing.T) {
	dir := t.TempDir()

	// Nonexistent file under a writable dir: fine (created at Persist).
	newPath := filepath.Join(dir, "not-yet.json")
	if _, _, err := assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode:      string(authz.ModeAllowlist),
		EnvTelegramApprovedStore: newPath,
	})); err != nil {
		t.Fatalf("nonexistent store path err = %v, want nil (created on Persist)", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("store file not created at %q: %v", newPath, err)
	}

	// Unwritable: a read-only parent dir so CreateTemp/rename fails.
	roDir := filepath.Join(dir, "readonly")
	if err := os.Mkdir(roDir, 0o500); err != nil {
		t.Fatalf("mkdir readonly: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("running as root; read-only dir is not enforced")
	}
	_, _, err := assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode:      string(authz.ModeAllowlist),
		EnvTelegramApprovedStore: filepath.Join(roDir, "store.json"),
	}))
	if err == nil {
		t.Fatal("unwritable store path returned nil error, want fail-fast")
	}
	if !isUsageConfig(err) {
		t.Errorf("unwritable-store err = %v, want errUsageConfig", err)
	}
}

// TC-151-10: allowlist seeds the static list into a fresh store file (0600, exactly the
// seeded IDs); a second assembly against the same path with a different/empty static list
// does not remove previously-seeded IDs (additive union, not destructive overwrite).
func TestTC151_10_SeedingWritesAndIsAdditive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approved.json")

	// First assembly: seed {42, 1001}.
	_, store, err := assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode:      string(authz.ModeAllowlist),
		EnvTelegramApprovedStore: path,
		EnvTelegramApprovedIDs:   "42,1001",
	}))
	if err != nil {
		t.Fatalf("first assembly: %v", err)
	}
	if store == nil {
		t.Fatal("allowlist assembly returned nil store")
	}

	// File exists at 0600 and contains exactly {42, 1001}.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat store: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store perm = %o, want 0600", perm)
	}
	ids := readStoreIDs(t, path)
	if len(ids) != 2 || !ids[42] || !ids[1001] {
		t.Errorf("seeded store ids = %v, want {42,1001}", ids)
	}

	// Second assembly against the SAME path with an EMPTY static list: must not wipe.
	_, _, err = assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode:      string(authz.ModeAllowlist),
		EnvTelegramApprovedStore: path,
		// EnvTelegramApprovedIDs unset (empty)
	}))
	if err != nil {
		t.Fatalf("second assembly (empty list): %v", err)
	}
	ids = readStoreIDs(t, path)
	if len(ids) != 2 || !ids[42] || !ids[1001] {
		t.Errorf("after re-assembly with empty list, ids = %v, want {42,1001} preserved (union, not overwrite)", ids)
	}

	// Third assembly with a DIFFERENT id adds to the union without removing the old ones.
	_, _, err = assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode:      string(authz.ModeAllowlist),
		EnvTelegramApprovedStore: path,
		EnvTelegramApprovedIDs:   "7",
	}))
	if err != nil {
		t.Fatalf("third assembly: %v", err)
	}
	ids = readStoreIDs(t, path)
	if !ids[42] || !ids[1001] || !ids[7] {
		t.Errorf("after adding 7, ids = %v, want {42,1001,7}", ids)
	}
}

// TC-151-10 edge: a malformed static ID (non-numeric) is a fail-fast config error at
// assembly, not a silently-skipped entry.
func TestTC151_10_MalformedStaticIDFailsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "approved.json")
	_, _, err := assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode:      string(authz.ModeAllowlist),
		EnvTelegramApprovedStore: path,
		EnvTelegramApprovedIDs:   "42,abc,1001",
	}))
	if err == nil {
		t.Fatal("malformed static ID returned nil error, want fail-fast")
	}
	if !isUsageConfig(err) {
		t.Errorf("malformed-static-ID err = %v, want errUsageConfig", err)
	}
}

// readStoreIDs parses the on-disk approved-store JSON and returns a set of its IDs.
func readStoreIDs(t *testing.T, path string) map[int64]bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var doc struct {
		ApprovedIDs []int64 `json:"approved_ids"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal store: %v (bytes: %s)", err, data)
	}
	set := make(map[int64]bool, len(doc.ApprovedIDs))
	for _, id := range doc.ApprovedIDs {
		set[id] = true
	}
	return set
}
