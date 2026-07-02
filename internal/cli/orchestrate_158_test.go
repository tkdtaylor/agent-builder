package cli

// TC-158-01/02/03/04 — wire a real armor guard on the Telegram inbound path (task 158,
// ADR 064). These exercise assembleTelegramInbound directly (the live production
// assembly seam mirroring TC-153/157's own tests in this package), proving:
//
//   - TC-158-01: AGENT_BUILDER_TELEGRAM_ARMOR_BIN resolution mirrors resolveAuditBin's
//     unset/resolvable/unresolvable cases.
//   - TC-158-02: a configured armor binary is wired as the adapter's REAL ContentGuard
//     for all four auth modes — proven by feeding each assembled adapter a message the
//     fixture armor command is scripted to BLOCK and observing the armor_blocked audit
//     reason + message drop (this would fail if the guard were still allowAllContentGuard).
//   - TC-158-03: unconfigured armor + envelope/disabled mode still assembles with the
//     pre-task fail-open behavior UNCHANGED.
//   - TC-158-04: unconfigured armor + any plaintext mode (allowlist/pairing/open) fails
//     assembly fast with errUsageConfig, naming AGENT_BUILDER_TELEGRAM_ARMOR_BIN.
//
// TC-158-05 (a real armor.Guard backed by a fake in-process Runner, directly injected as
// the adapter's ContentGuard) lives in internal/channel/telegram/adapter_158_test.go —
// see that file's header comment for why it is scoped there.

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// tc158Env builds a full valid Telegram env for mode, pointed at baseURL, reusing the
// SAME operator/orchestrator key material tc157's helpers generate (so envelope-mode
// updates built with tc157SealedUpdate verify against it). armorBin, when non-empty, is
// wired as AGENT_BUILDER_TELEGRAM_ARMOR_BIN; empty ⇒ unset (no armor configured). extra
// overrides/augments individual vars (e.g. APPROVED_STORE/APPROVED_IDS/OWNER_ID for
// store-consulting modes).
func tc158Env(k tc157Keys, baseURL, mode, armorBin string, extra map[string]string) func(string) string {
	m := map[string]string{
		EnvInbound:             "telegram",
		EnvTelegramAuthMode:    mode,
		EnvTelegramBotToken:    "tc158-token",
		EnvTelegramBaseURL:     baseURL,
		EnvTelegramChatID:      "9",
		EnvTelegramSigningKey:  hex.EncodeToString(k.opEdPub),
		EnvTelegramX25519Pub:   hex.EncodeToString(k.opXPub[:]),
		EnvTelegramOrchPriv:    hex.EncodeToString(k.orchXPriv[:]),
		EnvTelegramOrchEdPriv:  hex.EncodeToString(k.orchEdPriv),
		EnvTelegramOpX25519Pub: hex.EncodeToString(k.opReplyPub[:]),
		EnvTelegramPollBackoff: "1ms",
	}
	if armorBin != "" {
		m[EnvTelegramArmorBin] = armorBin
	}
	for key, val := range extra {
		m[key] = val
	}
	return func(key string) string { return m[key] }
}

// tc158Done returns an already-cancelled context, mirroring internal/channel/telegram's
// tc157Done(). A cancelled-at-entry context makes Adapter.Next() perform exactly ONE poll
// (fully processing that batch's side effects — including any armor_blocked audit event)
// before returning, rather than internally re-polling forever waiting for a shutdown
// signal that never arrives (the whole point of task 157's fix — see adapter.go's Next()
// doc comment). A batch that yields a deliverable message returns immediately regardless
// of ctx state, so this is also safe for the fail-open acceptance case (TC-158-03).
func tc158Done() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// tc158PlaintextUpdate builds a Telegram getUpdates update map carrying a raw plaintext
// message (no envelope) from senderID, for the sender-ID auth modes (allowlist/pairing/open).
func tc158PlaintextUpdate(updateID, msgID, senderID int64, text string) map[string]interface{} {
	msg := map[string]interface{}{
		"message_id": msgID,
		"text":       text,
		"chat":       map[string]interface{}{"id": senderID},
		"from":       map[string]interface{}{"id": senderID},
	}
	return map[string]interface{}{"update_id": updateID, "message": msg}
}

// tc158ArmorFixture writes an executable POSIX shell script to a temp dir that outputs a
// "block" armor decision when the JSON request content contains the sentinel "BLOCKME",
// and an "allow" decision otherwise. Returns the absolute path (resolvable via
// exec.LookPath since it contains a "/").
func tc158ArmorFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "armor-fixture.sh")
	script := "#!/bin/sh\n" +
		"input=$(cat)\n" +
		"case \"$input\" in\n" +
		"  *BLOCKME*) echo '{\"decision\":\"block\",\"reason\":\"tc158_fixture_block\"}' ;;\n" +
		"  *) echo '{\"decision\":\"allow\"}' ;;\n" +
		"esac\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture, intentionally executable
		t.Fatalf("write armor fixture: %v", err)
	}
	return path
}

// --- TC-158-01 -------------------------------------------------------------------------

// TestTC158_01_ArmorBinResolution mirrors resolveAuditBin's three cases: unset (no armor
// configured, no error), resolvable (resolves, no error), unresolvable (fail-fast
// errUsageConfig, mirroring resolveAuditBin's unresolvable-binary error shape).
func TestTC158_01_ArmorBinResolution(t *testing.T) {
	// (a) unset ⇒ no armor configured.
	resolved, err := resolveTelegramArmorBin(envFrom(map[string]string{}))
	if err != nil || resolved != "" {
		t.Errorf("unset: resolved=%q err=%v, want (\"\", nil)", resolved, err)
	}

	// (b) resolvable ⇒ resolves, no error.
	fixture := tc158ArmorFixture(t)
	resolved, err = resolveTelegramArmorBin(envFrom(map[string]string{EnvTelegramArmorBin: fixture}))
	if err != nil {
		t.Fatalf("resolvable: err = %v, want nil", err)
	}
	if resolved == "" {
		t.Error("resolvable: resolved path empty, want non-empty")
	}

	// (c) unresolvable ⇒ fail-fast errUsageConfig, mirroring resolveAuditBin's shape.
	_, err = resolveTelegramArmorBin(envFrom(map[string]string{EnvTelegramArmorBin: "/no/such/tc158-armor-binary-xyz"}))
	if err == nil {
		t.Fatal("unresolvable: err = nil, want errUsageConfig")
	}
	if !isUsageConfig(err) {
		t.Errorf("unresolvable: err = %v, want errUsageConfig", err)
	}
}

// TestTC158_01_UnresolvableArmorBinFailsAssembly proves the resolution error propagates
// through the full assembleTelegramInbound seam (envelope mode, so REQ-158-04's separate
// plaintext-mode gate does not also fire), never constructing an adapter/reporter.
func TestTC158_01_UnresolvableArmorBinFailsAssembly(t *testing.T) {
	k := tc157GenKeys(t)
	getenv := tc158Env(k, "http://127.0.0.1:0", "envelope", "/no/such/tc158-armor-binary-xyz", nil)
	adapter, rep, err := assembleTelegramInbound(context.Background(), getenv, audit.NewFakeSink(), nil, nil)
	if err == nil {
		t.Fatal("expected error for unresolvable armor bin, got nil")
	}
	if !isUsageConfig(err) {
		t.Errorf("err = %v, want errUsageConfig", err)
	}
	if adapter != nil || rep != nil {
		t.Error("expected nil adapter/reporter on assembly failure")
	}
}

// --- TC-158-02 -------------------------------------------------------------------------

// TestTC158_02_ConfiguredArmorWiredForEveryMode proves a configured armor binary is
// wired as the REAL ContentGuard (armor.Guard, not allowAllContentGuard) for all four
// auth modes: each assembled adapter is fed a message the fixture armor command is
// scripted to BLOCK, and the message must be dropped with an armor_blocked audit reason.
// This is the dead-wire guard: it would fail if assembleTelegramInbound still hardwired
// allowAllContentGuard regardless of AGENT_BUILDER_TELEGRAM_ARMOR_BIN.
func TestTC158_02_ConfiguredArmorWiredForEveryMode(t *testing.T) {
	fixture := tc158ArmorFixture(t)
	dir := t.TempDir()

	cases := []struct {
		mode  string
		extra map[string]string
	}{
		{mode: "envelope"},
		{mode: "disabled"}, // included for completeness; disabled never reaches armor (see note below)
		{mode: "allowlist", extra: map[string]string{
			EnvTelegramApprovedStore: filepath.Join(dir, "allowlist.json"),
			EnvTelegramApprovedIDs:   "42",
		}},
		{mode: "pairing", extra: map[string]string{
			EnvTelegramApprovedStore: filepath.Join(dir, "pairing.json"),
			EnvTelegramApprovedIDs:   "42",
			EnvTelegramOwnerID:       "999",
		}},
		{mode: "open"},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			k := tc157GenKeys(t)

			var update map[string]interface{}
			switch tc.mode {
			case "envelope":
				update = tc157SealedUpdate(t, k, 7000, 2, "BLOCKME do the thing")
			case "disabled":
				// disabled mode rejects everything before armor is ever consulted — armor
				// wiring is irrelevant on this path. Confirm assembly succeeds (regression
				// only) and skip the armor-block assertion below.
				update = tc158PlaintextUpdate(7003, 2, 42, "BLOCKME do the thing")
			default: // allowlist, pairing, open — plaintext from an approved/any sender
				update = tc158PlaintextUpdate(7001, 2, 42, "BLOCKME do the thing")
			}

			var calls int64
			srv := tc157CliServer(t, &calls, []map[string]interface{}{update})
			getenv := tc158Env(k, srv.URL, tc.mode, fixture, tc.extra)
			sink := audit.NewFakeSink()

			adapter, _, err := assembleTelegramInbound(tc158Done(), getenv, sink, nil, nil)
			if err != nil {
				t.Fatalf("assembleTelegramInbound: %v", err)
			}

			msg, ok, err := adapter.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if ok {
				t.Fatalf("message accepted (msg=%+v), want dropped", msg)
			}

			if tc.mode == "disabled" {
				// disabled rejects before armor: no armor_blocked reason, but also no
				// silent accept — confirmed above (ok=false).
				assertNoAuditReasonPrefixCLI(t, sink, "armor_blocked")
				return
			}
			assertHasAuditReasonPrefixCLI(t, sink, "armor_blocked")
		})
	}
}

// --- TC-158-03 -------------------------------------------------------------------------

// TestTC158_03_NoArmorConfiguredEnvelopeDisabledFailOpenUnchanged proves that with no
// armor binary configured, envelope mode still assembles and delivers content a REAL
// armor guard WOULD have blocked (fail-open, unchanged from pre-task behavior); disabled
// mode still assembles successfully too (regression only — it never reaches armor).
func TestTC158_03_NoArmorConfiguredEnvelopeDisabledFailOpenUnchanged(t *testing.T) {
	k := tc157GenKeys(t)
	update := tc157SealedUpdate(t, k, 7100, 2, "BLOCKME this would be blocked by real armor")
	var calls int64
	srv := tc157CliServer(t, &calls, []map[string]interface{}{update})
	getenv := tc158Env(k, srv.URL, "envelope", "", nil) // no armor bin configured
	sink := audit.NewFakeSink()

	adapter, _, err := assembleTelegramInbound(tc158Done(), getenv, sink, nil, nil)
	if err != nil {
		t.Fatalf("envelope mode assembly with no armor configured: %v", err)
	}
	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ok {
		t.Fatal("envelope-mode message rejected with no armor configured, want fail-open acceptance (unchanged pre-task behavior)")
	}
	if !strings.Contains(msg.Goal.Spec, "BLOCKME") {
		t.Errorf("delivered message spec = %q, want it to contain the content a real armor guard would have blocked", msg.Goal.Spec)
	}
	assertNoAuditReasonPrefixCLI(t, sink, "armor_blocked")

	// disabled: assembly still succeeds with no armor configured (regression only).
	getenvDisabled := tc158Env(k, srv.URL, "disabled", "", nil)
	if _, _, err := assembleTelegramInbound(context.Background(), getenvDisabled, audit.NewFakeSink(), nil, nil); err != nil {
		t.Fatalf("disabled mode assembly with no armor configured: %v", err)
	}
}

// --- TC-158-04 -------------------------------------------------------------------------

// TestTC158_04_NoArmorConfiguredPlaintextModesFailFast proves that assembling any
// plaintext mode (allowlist/pairing/open) with no armor binary configured is a fail-fast
// errUsageConfig error naming AGENT_BUILDER_TELEGRAM_ARMOR_BIN — never a silently
// fail-open guard — and that no adapter/reporter is constructed on that failure.
func TestTC158_04_NoArmorConfiguredPlaintextModesFailFast(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		mode  string
		extra map[string]string
	}{
		{mode: "allowlist", extra: map[string]string{EnvTelegramApprovedStore: filepath.Join(dir, "allowlist.json")}},
		{mode: "pairing", extra: map[string]string{
			EnvTelegramApprovedStore: filepath.Join(dir, "pairing.json"),
			EnvTelegramOwnerID:       "999",
		}},
		{mode: "open"},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			k := tc157GenKeys(t)
			getenv := tc158Env(k, "http://127.0.0.1:0", tc.mode, "", tc.extra)
			adapter, rep, err := assembleTelegramInbound(context.Background(), getenv, audit.NewFakeSink(), nil, nil)
			if err == nil {
				t.Fatalf("%s mode with no armor configured: err = nil, want fail-fast errUsageConfig", tc.mode)
			}
			if !isUsageConfig(err) {
				t.Errorf("%s mode err = %v, want errUsageConfig", tc.mode, err)
			}
			if !strings.Contains(err.Error(), EnvTelegramArmorBin) {
				t.Errorf("%s mode err = %v, want it to name %s", tc.mode, err, EnvTelegramArmorBin)
			}
			if adapter != nil || rep != nil {
				t.Errorf("%s mode: adapter/rep non-nil on assembly failure, want nil (no adapter constructed before the fail-fast check)", tc.mode)
			}
		})
	}
}

// --- shared audit-reason helpers (this package has no telegram_test-style helpers) -----

func assertHasAuditReasonPrefixCLI(t *testing.T, sink *audit.FakeSink, prefix string) {
	t.Helper()
	for _, ev := range sink.Events() {
		if strings.HasPrefix(ev.Detail.Reason, prefix) {
			return
		}
	}
	var reasons []string
	for _, ev := range sink.Events() {
		reasons = append(reasons, ev.Detail.Reason)
	}
	t.Errorf("no audit event with reason prefix %q; got %v", prefix, reasons)
}

func assertNoAuditReasonPrefixCLI(t *testing.T, sink *audit.FakeSink, prefix string) {
	t.Helper()
	for _, ev := range sink.Events() {
		if strings.HasPrefix(ev.Detail.Reason, prefix) {
			t.Errorf("unexpected audit event with reason prefix %q: %+v", prefix, ev)
		}
	}
}
