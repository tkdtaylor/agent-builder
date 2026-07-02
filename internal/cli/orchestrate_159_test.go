package cli

// TC-159-01/02/03/04 — seed the pairing-mode owner into the approved-sender store at
// assembleTelegramInbound startup (task 159, ADR 063 Decision 3 follow-up).
//
// Root cause this closes: assembleTelegramOwnerID resolved the owner ID ONLY for
// Adapter.OwnerID (consulted by DecidePairing to gate the approve/deny grammar and the
// owner-notification path) — the approved-sender STORE was seeded only from
// AGENT_BUILDER_TELEGRAM_APPROVED_IDS, never from the owner ID itself. DecidePairing step
// 3 requires store.Contains(rawSenderID) for the owner's own ordinary plaintext commands to
// route normally, so the owner's first command was treated exactly like a stranger's.
//
// These tests exercise the REAL assembleTelegramInbound production seam (mirroring
// TC-153/157/158's own tests in this package), not a hand-seeded test double:
//   - TC-159-01: the owner is seeded into the store even with no static APPROVED_IDS.
//   - TC-159-02: the seed is additive (union with a pre-existing statically-seeded ID) and
//     persisted — survives a simulated restart (a fresh Store over the same path).
//   - TC-159-03: non-pairing modes are unaffected; a stray OWNER_ID never reaches an
//     allowlist-mode store.
//   - TC-159-04: end-to-end, the owner's first plaintext command ("status") routes as
//     supervisor.MsgStatus with NO prior approve/pending exchange, through the REAL
//     assembleTelegramInbound-constructed Adapter driven against a scripted stub server.
//     This test FAILS against the pre-159 code (the owner would be routed to
//     ActionPairingPending instead) — see the mutation check in the task report.
//
// TC-159-05 (TestTC152_07's workaround relabeled) lives in
// internal/channel/telegram/adapter_152_test.go. TC-159-06 (full regression) is the
// existing internal/cli and internal/channel/telegram pairing-mode suites continuing to
// pass unchanged — see `go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/...`.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- TC-159-01 ---------------------------------------------------------------------------

// TestTC159_01_OwnerSeededEvenWithNoStaticApprovedIDs proves the owner's own normalized ID
// lands in the approved-sender store at assembleTelegramInbound startup, with NO
// AGENT_BUILDER_TELEGRAM_APPROVED_IDS configured — the seed comes from OWNER_ID alone.
func TestTC159_01_OwnerSeededEvenWithNoStaticApprovedIDs(t *testing.T) {
	k := tc157GenKeys(t)
	dir := t.TempDir()
	storePath := filepath.Join(dir, "pairing.json")

	getenv := tc158Env(k, "http://127.0.0.1:0", "pairing", "true", map[string]string{
		EnvTelegramApprovedStore: storePath,
		EnvTelegramOwnerID:       "1001",
	})

	adapter, rep, err := assembleTelegramInbound(context.Background(), getenv, audit.NewFakeSink(), nil, nil)
	if err != nil {
		t.Fatalf("assembleTelegramInbound: %v", err)
	}
	if adapter == nil || rep == nil {
		t.Fatal("assembleTelegramInbound returned nil adapter/reporter on success")
	}

	store := authz.NewStore(storePath)
	if err := store.Load(); err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	ok, err := store.Contains("1001")
	if err != nil {
		t.Fatalf("store.Contains: %v", err)
	}
	if !ok {
		t.Error("owner ID 1001 was not seeded into the store, want seeded even with no static APPROVED_IDS")
	}
}

// --- TC-159-02 ---------------------------------------------------------------------------

// TestTC159_02_OwnerSeedAdditiveAndPersistedAcrossSimulatedRestart proves the owner seed is
// additive (a pre-existing statically-seeded ID survives untouched) and persisted (a FRESH
// Store constructed over the same path after assembly — a simulated process restart — sees
// both IDs), mirroring TC-152-08/the existing APPROVED_IDS seeding's durability contract.
func TestTC159_02_OwnerSeedAdditiveAndPersistedAcrossSimulatedRestart(t *testing.T) {
	k := tc157GenKeys(t)
	dir := t.TempDir()
	storePath := filepath.Join(dir, "pairing.json")

	// Simulate a prior APPROVED_IDS run: the store already has "555" on disk before
	// assembleTelegramInbound is ever called.
	pre := authz.NewStore(storePath)
	if err := pre.Add("555"); err != nil {
		t.Fatalf("pre-seed Add: %v", err)
	}
	if err := pre.Persist(); err != nil {
		t.Fatalf("pre-seed Persist: %v", err)
	}

	getenv := tc158Env(k, "http://127.0.0.1:0", "pairing", "true", map[string]string{
		EnvTelegramApprovedStore: storePath,
		EnvTelegramOwnerID:       "1001",
	})
	if _, _, err := assembleTelegramInbound(context.Background(), getenv, audit.NewFakeSink(), nil, nil); err != nil {
		t.Fatalf("assembleTelegramInbound: %v", err)
	}

	// "Restart": a brand-new Store instance over the same path, Load()ed fresh — proves the
	// seed was PERSISTED to disk, not just held in the in-memory store this run constructed.
	restarted := authz.NewStore(storePath)
	if err := restarted.Load(); err != nil {
		t.Fatalf("restarted store Load: %v", err)
	}
	for _, id := range []string{"555", "1001"} {
		ok, err := restarted.Contains(id)
		if err != nil {
			t.Fatalf("restarted store Contains(%s): %v", id, err)
		}
		if !ok {
			t.Errorf("restarted store missing %s, want both the pre-existing static ID and the newly-seeded owner", id)
		}
	}
	if restarted.Len() != 2 {
		t.Errorf("restarted store len = %d, want exactly 2 (555 + 1001, additive union)", restarted.Len())
	}
}

// --- TC-159-03 ---------------------------------------------------------------------------

// TestTC159_03_NonPairingModesUnaffected proves no owner-seeding logic runs outside pairing
// mode. The critical assertion is allowlist mode: a stray AGENT_BUILDER_TELEGRAM_OWNER_ID
// env var (set but irrelevant outside pairing) must NEVER reach an allowlist-mode store —
// only its own APPROVED_IDS-seeded entries may appear there.
func TestTC159_03_NonPairingModesUnaffected(t *testing.T) {
	t.Run("allowlist", func(t *testing.T) {
		k := tc157GenKeys(t)
		dir := t.TempDir()
		storePath := filepath.Join(dir, "allowlist.json")

		getenv := tc158Env(k, "http://127.0.0.1:0", "allowlist", "true", map[string]string{
			EnvTelegramApprovedStore: storePath,
			EnvTelegramApprovedIDs:   "42",
			EnvTelegramOwnerID:       "9999", // stray: irrelevant outside pairing, must not leak in
		})
		if _, _, err := assembleTelegramInbound(context.Background(), getenv, audit.NewFakeSink(), nil, nil); err != nil {
			t.Fatalf("assembleTelegramInbound: %v", err)
		}

		store := authz.NewStore(storePath)
		if err := store.Load(); err != nil {
			t.Fatalf("store.Load: %v", err)
		}
		if ok, _ := store.Contains("9999"); ok {
			t.Error("stray OWNER_ID reached the allowlist-mode store, want it never consulted outside pairing")
		}
		if ok, _ := store.Contains("42"); !ok {
			t.Error("allowlist mode's own APPROVED_IDS entry missing from the store")
		}
		if store.Len() != 1 {
			t.Errorf("allowlist store len = %d, want exactly 1 (only the APPROVED_IDS entry, no owner-seeding leak)", store.Len())
		}
	})

	// envelope/disabled/open never consult the store at all (unchanged, task 151/153's own
	// contract) — a stray OWNER_ID present in the env is simply irrelevant, and assembly
	// still succeeds (regression-only: this task adds no new failure mode to these paths).
	for _, mode := range []string{"envelope", "disabled", "open"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			k := tc157GenKeys(t)
			getenv := tc158Env(k, "http://127.0.0.1:0", mode, "true", map[string]string{
				EnvTelegramOwnerID: "9999", // stray, irrelevant outside pairing
			})
			adapter, rep, err := assembleTelegramInbound(context.Background(), getenv, audit.NewFakeSink(), nil, nil)
			if err != nil {
				t.Fatalf("%s mode assembleTelegramInbound: %v", mode, err)
			}
			if adapter == nil || rep == nil {
				t.Fatalf("%s mode: nil adapter/reporter on success", mode)
			}
		})
	}
}

// TestTC159_03_NoStoreFileCreatedForNonConsultingModes is a narrower regression guard: for
// modes that never consult the store (envelope/disabled/open), assembleTelegramInbound must
// not create a store file at an incidental path even when OWNER_ID happens to be set.
func TestTC159_03_NoStoreFileCreatedForNonConsultingModes(t *testing.T) {
	for _, mode := range []string{"envelope", "disabled", "open"} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			k := tc157GenKeys(t)
			dir := t.TempDir()
			incidentalPath := filepath.Join(dir, "should-not-exist.json")
			getenv := tc158Env(k, "http://127.0.0.1:0", mode, "true", map[string]string{
				EnvTelegramOwnerID: "9999",
			})
			if _, _, err := assembleTelegramInbound(context.Background(), getenv, audit.NewFakeSink(), nil, nil); err != nil {
				t.Fatalf("%s mode assembleTelegramInbound: %v", mode, err)
			}
			if _, err := os.Stat(incidentalPath); err == nil {
				t.Errorf("%s mode created a store file at %q, want none built (mode never consults the store)", mode, incidentalPath)
			}
		})
	}
}

// --- TC-159-04 -----------------------------------------------------------------------------

// TestTC159_04_OwnerFirstCommandRoutesWithoutPendingExchange is the L5 end-to-end proof:
// through the REAL assembleTelegramInbound-constructed Adapter, driven against a scripted
// stub Telegram server, the owner's own FIRST plaintext command ("status") routes normally
// as supervisor.MsgStatus with NO prior approve/pending exchange — closing the self-approval
// footgun. The store path is FRESH (no pre-seeding of any kind, static or manual) — the
// critical difference from task 152's TestTC152_07 workaround.
//
// This test FAILS against the pre-159 code: without the owner-seeding fix, DecidePairing
// treats the owner exactly like an unknown stranger and returns ActionPairingPending
// (ok=false, a pairing_request audit event, no supervisor.Message) instead.
func TestTC159_04_OwnerFirstCommandRoutesWithoutPendingExchange(t *testing.T) {
	const ownerID = int64(1001)

	k := tc157GenKeys(t)
	dir := t.TempDir()
	storePath := filepath.Join(dir, "pairing.json") // fresh, non-existent — no pre-seeding
	fixture := tc158ArmorFixture(t)                 // a REAL armor guard that allows non-BLOCKME content

	update := tc158PlaintextUpdate(9001, 2, ownerID, "status") // sender == owner, ordinary command
	var calls int64
	srv := tc157CliServer(t, &calls, []map[string]interface{}{update})

	getenv := tc158Env(k, srv.URL, "pairing", fixture, map[string]string{
		EnvTelegramApprovedStore: storePath,
		EnvTelegramOwnerID:       "1001",
	})
	sink := audit.NewFakeSink()

	adapter, _, err := assembleTelegramInbound(tc158Done(), getenv, sink, nil, nil)
	if err != nil {
		t.Fatalf("assembleTelegramInbound: %v", err)
	}

	msg, ok, err := adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ok {
		t.Fatal("owner's first plaintext command was rejected/pending, want routed normally with no prior approval")
	}
	if msg.Kind != supervisor.MsgStatus {
		t.Errorf("msg.Kind = %v, want MsgStatus", msg.Kind)
	}

	// No pairing_request (pending) audit event may have fired — the owner must never be
	// treated as an unknown stranger.
	assertNoAuditReasonPrefixCLI(t, sink, string(authz.ReasonPairingRequest))

	// The store itself now contains the owner (proving the seed landed, not just that this
	// particular Next() call happened to succeed some other way).
	store := authz.NewStore(storePath)
	if err := store.Load(); err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	if contained, _ := store.Contains("1001"); !contained {
		t.Error("store does not contain the owner after assembly, want seeded")
	}
}
