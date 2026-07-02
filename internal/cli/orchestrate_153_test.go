package cli

// TC-153-03/05 — Telegram `open` mode: exact-literal-only config gate + mandatory startup
// WARNING (task 153, ADR 063 Decision 1 / REQ-153-02 / REQ-153-03).
//
// TC-153-03 exercises assembleTelegramAuthMode directly (the config-validation seam,
// mirroring how TC-151-07/09 exercise it): unset/"" still resolves to envelope; "Open",
// "OPEN", " open" (leading space), "open " (trailing space) all fail assembly as
// unknown-mode errors — none of them silently resolve to ModeOpen.
//
// TC-153-05 exercises assembleTelegramInbound/inboundFromEnv end-to-end (the full
// assembly path, mirroring TC-117-04B): AUTH_MODE=open emits exactly one WARNING-prefixed
// stderr line naming the risk phrase; the other four modes emit zero such lines.
//
// TC-153-04 (regression guard, per the test spec's own Notes: "a regression re-run
// rather than new assertions") is satisfied by the full pre-existing 151/152/117 suites
// in this package and internal/channel/telegram continuing to pass UNMODIFIED (in
// behavior — only their inboundFromEnv call sites gained a trailing nil warnOut arg for
// the new parameter) after this file's diff lands: TestTC151_07_UnknownModeFailsFast_RecognizedPass,
// TestTC151_09_BlankStorePathFailsForConsultingModes, TestTC151_10_SeedingWritesAndIsAdditive,
// TestTC152_09_PairingOwnerIDRequiredAndNormalized (this package) and
// TestTC151_01_UnsetAndEnvelopeModeIdenticalToPreTask, TestTC151_06_DisabledModeRejectsEverything,
// TestTC151_03_AllowlistAcceptsApprovedSender, TestTC151_04_AllowlistRejectsUnapprovedSender,
// TestTC152_05_StrangerCannotSelfApprove, TestTC152_08_ApprovalSurvivesSimulatedRestart
// (internal/channel/telegram) — see `go test ./internal/channel/telegram/... ./internal/cli/...`.
//
// TC-153-06 (documentation footprint) is a documentation-completeness review, not a Go
// unit test, per the test spec's own Notes — see docs/spec/configuration.md's
// AGENT_BUILDER_TELEGRAM_AUTH_MODE row, docs/spec/behaviors.md's B-038, and
// docs/architecture/diagrams.md's Telegram inbound auth-mode branch paragraph, all
// closed out (present tense, no future-tense "planned" language) in this task's commit.

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
)

// TC-153-03: "open" is reachable ONLY via the exact, case-sensitive, whitespace-exact
// literal "open". Unset/"" still resolves to envelope (unaffected by this task). Every
// near-miss variant is a fail-fast unknown-mode config error — never a silent fallback to
// envelope, and never an accidental match to open.
func TestTC153_03_OpenModeExactLiteralOnly(t *testing.T) {
	// Unset/"" ⇒ envelope, unaffected (task 151's TC-151-01 contract, reconfirmed here).
	for _, raw := range []string{"", "   "} {
		mode, store, err := assembleTelegramAuthMode(envFrom(map[string]string{
			EnvTelegramAuthMode: raw,
		}))
		if err != nil {
			t.Errorf("AUTH_MODE=%q err = %v, want nil (⇒ envelope)", raw, err)
		}
		if mode != authz.ModeEnvelope || store != nil {
			t.Errorf("AUTH_MODE=%q ⇒ mode=%q store=%v, want envelope/nil", raw, mode, store)
		}
	}

	// Near-miss variants: wrong case or untrimmed whitespace. NONE may resolve to open,
	// and none may silently fall back to envelope — each must be a fail-fast unknown-mode
	// config error.
	for _, raw := range []string{"Open", "OPEN", " open", "open ", " open ", "oPeN"} {
		mode, store, err := assembleTelegramAuthMode(envFrom(map[string]string{
			EnvTelegramAuthMode: raw,
		}))
		if err == nil {
			t.Errorf("AUTH_MODE=%q returned nil error (mode=%q), want fail-fast unknown-mode error", raw, mode)
			continue
		}
		if !isUsageConfig(err) {
			t.Errorf("AUTH_MODE=%q err = %v, want errUsageConfig (ExitUsage)", raw, err)
		}
		if mode == authz.ModeOpen {
			t.Errorf("AUTH_MODE=%q accidentally resolved to ModeOpen, want rejection", raw)
		}
		if mode != "" || store != nil {
			t.Errorf("AUTH_MODE=%q returned mode=%q store=%v on error, want empty/nil", raw, mode, store)
		}
	}

	// The exact literal "open" (no surrounding whitespace, exact case) succeeds and
	// consults no store (never a store-consulting mode).
	mode, store, err := assembleTelegramAuthMode(envFrom(map[string]string{
		EnvTelegramAuthMode: "open",
	}))
	if err != nil {
		t.Fatalf(`AUTH_MODE="open" err = %v, want nil`, err)
	}
	if mode != authz.ModeOpen {
		t.Errorf(`AUTH_MODE="open" resolved to %q, want ModeOpen`, mode)
	}
	if store != nil {
		t.Errorf(`AUTH_MODE="open" built a store, want nil (open never consults the store)`, )
	}
}

// tc153FullTelegramEnv returns a getenv func with the complete set of required
// AGENT_BUILDER_TELEGRAM_* crypto vars (mirroring TC-117-04B's key material), plus
// AGENT_BUILDER_INBOUND=telegram and AUTH_MODE set to mode. Callers may pass additional
// overrides (e.g. a stray APPROVED_STORE value) via extra.
func tc153FullTelegramEnv(t *testing.T, mode string, extra map[string]string) func(string) string {
	t.Helper()
	opEdPub, opEdPriv, opXPub, opXPriv, orchXPub, orchXPriv := tc117CliKeyMaterial(t)
	orchEdPub, orchEdPriv, _, _, opXPubReply, _ := tc117CliKeyMaterial(t)
	_ = opEdPub
	_ = opXPub
	_ = orchXPub
	_ = orchEdPub

	base := map[string]string{
		EnvInbound:            "telegram",
		EnvTelegramAuthMode:   mode,
		EnvTelegramBotToken:   "test-bot-token-153",
		EnvTelegramBaseURL:    "https://api.telegram.org",
		EnvTelegramChatID:     "12345",
		EnvTelegramSigningKey: hex.EncodeToString(opEdPriv.Public().(ed25519.PublicKey)),
		EnvTelegramX25519Pub:  hex.EncodeToString(opXPriv[:]),
		EnvTelegramOrchPriv:   hex.EncodeToString(orchXPriv[:]),
		EnvTelegramOrchEdPriv: hex.EncodeToString(orchEdPriv),
		EnvTelegramOpX25519Pub: hex.EncodeToString(opXPubReply[:]),
	}
	for k, v := range extra {
		base[k] = v
	}
	return envFrom(base)
}

// TC-153-05: assembling with AUTH_MODE=open emits exactly one WARNING-prefixed stderr
// line containing the risk phrase; assembling with any of the other four modes emits
// zero such lines. Exercises the full inboundFromEnv/assembleTelegramInbound path (the
// live assembly seam), not a hand-constructed adapter.
func TestTC153_05_OpenModeEmitsMandatoryWarning_OthersDoNot(t *testing.T) {
	sink := audit.NewFakeSink()

	// (a) open mode: exactly one WARNING line naming the risk phrase.
	var stderr strings.Builder
	getenv := tc153FullTelegramEnv(t, "open", nil)
	src, rep, err := inboundFromEnv(context.Background(), getenv, nil, sink, nil, &stderr)
	if err != nil {
		t.Fatalf("open mode assembly error: %v", err)
	}
	if src == nil || rep == nil {
		t.Fatal("open mode assembly returned nil source/reporter")
	}
	out := stderr.String()
	lines := warningLines(out)
	if len(lines) != 1 {
		t.Fatalf("open mode stderr WARNING lines = %d, want exactly 1; full output: %q", len(lines), out)
	}
	if !strings.Contains(lines[0], "any account") || !strings.Contains(lines[0], "command it") {
		t.Errorf("open mode WARNING line = %q, want it to contain the risk phrase (\"any account ... command it\")", lines[0])
	}

	// (b) the other four modes: zero WARNING lines. allowlist/pairing need a valid store
	// path + (for pairing) an owner ID to assemble successfully.
	dir := t.TempDir()
	cases := []struct {
		mode  string
		extra map[string]string
	}{
		{mode: "envelope", extra: nil},
		{mode: "disabled", extra: nil},
		{mode: "allowlist", extra: map[string]string{EnvTelegramApprovedStore: dir + "/allowlist.json"}},
		{mode: "pairing", extra: map[string]string{
			EnvTelegramApprovedStore: dir + "/pairing.json",
			EnvTelegramOwnerID:       "42",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			var stderrOther strings.Builder
			getenv := tc153FullTelegramEnv(t, tc.mode, tc.extra)
			_, _, err := inboundFromEnv(context.Background(), getenv, nil, sink, nil, &stderrOther)
			if err != nil {
				t.Fatalf("%s mode assembly error: %v", tc.mode, err)
			}
			out := stderrOther.String()
			if got := len(warningLines(out)); got != 0 {
				t.Errorf("%s mode emitted %d WARNING line(s), want 0; output: %q", tc.mode, got, out)
			}
		})
	}
}

// TC-153-05 edge: the warning fires even when an unrelated env var (APPROVED_STORE) is
// also set alongside open mode — open never reads the store, but its mere presence must
// not suppress or duplicate the warning.
func TestTC153_05_WarningFiresRegardlessOfUnrelatedStoreVar(t *testing.T) {
	sink := audit.NewFakeSink()
	dir := t.TempDir()
	var stderr strings.Builder

	getenv := tc153FullTelegramEnv(t, "open", map[string]string{
		EnvTelegramApprovedStore: dir + "/unused.json",
	})
	_, _, err := inboundFromEnv(context.Background(), getenv, nil, sink, nil, &stderr)
	if err != nil {
		t.Fatalf("open mode (with stray APPROVED_STORE) assembly error: %v", err)
	}
	if got := len(warningLines(stderr.String())); got != 1 {
		t.Errorf("open mode with stray APPROVED_STORE emitted %d WARNING line(s), want exactly 1", got)
	}
}

// warningLines returns the subset of lines in s that start with "WARNING".
func warningLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "WARNING") {
			lines = append(lines, line)
		}
	}
	return lines
}
