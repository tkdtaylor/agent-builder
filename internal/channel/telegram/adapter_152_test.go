package telegram_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/channel/telegram/authz"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- helpers for the pairing (task 152) adapter tests --------------------------------

// pairingUpdate is one scripted inbound update: text + sender ID (used for from.id and
// chat.id, mirroring a 1:1 Telegram DM where chat.id == user.id).
type pairingUpdate struct {
	text     string
	senderID int64
}

// scriptedServer returns a stub Telegram server that yields the given updates one per
// getUpdates poll (each on its own poll, in order), then empty batches. This lets a test
// drive several Next() calls with distinct updates (e.g. approve then status).
func scriptedServer(t *testing.T, updates ...pairingUpdate) *httptest.Server {
	t.Helper()
	call := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var result []any
		if call < len(updates) {
			u := updates[call]
			msg := map[string]any{"message_id": int64(7 + call), "text": u.text}
			if u.senderID != 0 {
				msg["from"] = map[string]any{"id": u.senderID}
				msg["chat"] = map[string]any{"id": u.senderID}
			}
			result = []any{map[string]any{"update_id": 100 + call, "message": msg}}
		}
		call++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pairingHarness bundles an adapter wired for pairing mode plus its observation sinks.
type pairingHarness struct {
	adapter  *telegram.Adapter
	store    *authz.Store
	sink     *audit.FakeSink
	notifier *telegram.FakeNotifier
	guard    *mode151Guard
}

// newPairingHarness builds a pairing-mode adapter over a scripted server. ownerID is the
// configured owner; storePath is the shared 0600 store file; seededIDs are pre-approved.
func newPairingHarness(t *testing.T, srv *httptest.Server, ownerID int64, storePath string, seededIDs ...string) pairingHarness {
	t.Helper()
	store := authz.NewStore(storePath)
	if err := store.Load(); err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	for _, id := range seededIDs {
		if err := store.Add(id); err != nil {
			t.Fatalf("seed Add(%s): %v", id, err)
		}
	}
	if len(seededIDs) > 0 {
		if err := store.Persist(); err != nil {
			t.Fatalf("seed Persist: %v", err)
		}
	}
	sink := audit.NewFakeSink()
	notifier := telegram.NewFakeNotifier()
	guard := &mode151Guard{}
	adapter := telegram.NewAdapter(telegram.Config{
		Ctx:          tc157Done(),
		BotToken:     "test-token",
		BaseURL:      srv.URL,
		HTTPClient:   srv.Client(),
		ContentGuard: guard,
		AuditSink:    sink,
		AuthMode:     authz.ModePairing,
		AuthStore:    store,
		OwnerID:      ownerID,
		OwnerChatID:  formatID(ownerID),
		Notifier:     notifier,
	})
	return pairingHarness{adapter: adapter, store: store, sink: sink, notifier: notifier, guard: guard}
}

func formatID(id int64) string {
	return strconv.FormatInt(id, 10)
}

// notifTo returns the texts sent to a given chat ID, in order.
func notifTo(n *telegram.FakeNotifier, chatID string) []string {
	var out []string
	for _, s := range n.Sent() {
		if s.ChatID == chatID {
			out = append(out, s.Text)
		}
	}
	return out
}

// --- TC-152-01 -----------------------------------------------------------------------

// TC-152-01: unknown sender in pairing mode → pairing_request audit + pending reply to
// the sender + owner notification carrying the sender ID and approve/deny instruction;
// NO supervisor.Message derived, armor never invoked.
func TestTC152_01_UnknownSenderPendingAndOwnerNotified(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t, pairingUpdate{text: "please let me in", senderID: 77})
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"))

	_, ok, err := h.adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("unknown sender yielded a message, want no message (pending path)")
	}
	// No armor on the pending path (never reaches processPlaintext).
	if h.guard.calls != 0 {
		t.Errorf("armor invoked %d times on pending path, want 0", h.guard.calls)
	}
	// Exactly one pairing_request audit event.
	if got := countAuditReason(h.sink, string(authz.ReasonPairingRequest)); got != 1 {
		t.Errorf("pairing_request audit count = %d, want 1", got)
	}
	// Pending reply to the sender (chat 77) with a recognizable pending marker.
	senderMsgs := notifTo(h.notifier, "77")
	if len(senderMsgs) != 1 {
		t.Fatalf("messages to sender chat 77 = %d, want 1", len(senderMsgs))
	}
	if !strings.Contains(strings.ToLower(senderMsgs[0]), "pending") {
		t.Errorf("sender reply %q missing 'pending' marker", senderMsgs[0])
	}
	// Owner notification (chat 1) containing the sender ID and approve/deny instruction.
	ownerMsgs := notifTo(h.notifier, "1")
	if len(ownerMsgs) != 1 {
		t.Fatalf("messages to owner chat 1 = %d, want 1", len(ownerMsgs))
	}
	on := ownerMsgs[0]
	if !strings.Contains(on, "77") || !strings.Contains(on, "approve 77") || !strings.Contains(on, "deny 77") {
		t.Errorf("owner notification %q missing sender ID or approve/deny instruction", on)
	}
	// Store unchanged — no self/auto approval.
	assertNotApproved(t, h.store, "77")
}

// TC-152-01 edge: a second still-pending message from the same sender re-triggers the
// pending path (idempotent, no crash).
func TestTC152_01_SecondPendingMessageReTriggers(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t,
		pairingUpdate{text: "hello", senderID: 77},
		pairingUpdate{text: "still waiting", senderID: 77},
	)
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"))

	for i := 0; i < 2; i++ {
		if _, ok, err := h.adapter.Next(); err != nil || ok {
			t.Fatalf("Next #%d: ok=%v err=%v, want no message", i, ok, err)
		}
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingRequest)); got != 2 {
		t.Errorf("pairing_request audit count = %d, want 2 (idempotent re-trigger)", got)
	}
	assertNotApproved(t, h.store, "77")
}

// --- TC-152-02 -----------------------------------------------------------------------

// TC-152-02: a still-pending sender's command-verb-shaped text ("status") still hits the
// pending path, never deriveMessage — proving the pairing branch intercepts before verb
// routing.
func TestTC152_02_PendingSenderStatusTextDoesNotRouteAsCommand(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t, pairingUpdate{text: "status", senderID: 77})
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"))

	_, ok, err := h.adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal(`"status" from a pending sender was routed as a command, want pending path (no message)`)
	}
	if h.guard.calls != 0 {
		t.Errorf("armor invoked %d times, want 0 (pending intercepts before armor/derive)", h.guard.calls)
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingRequest)); got != 1 {
		t.Errorf("pairing_request audit count = %d, want 1", got)
	}
	assertNotApproved(t, h.store, "77")
}

// --- TC-152-03 -----------------------------------------------------------------------

// TC-152-03: owner's "approve 77" adds 77 to the store, persists it, audits it, confirms
// to the owner, and derives no message.
func TestTC152_03_OwnerApproveAddsToStore(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "approved.json")
	srv := scriptedServer(t, pairingUpdate{text: "approve 77", senderID: 1})
	h := newPairingHarness(t, srv, 1, storePath)

	_, ok, err := h.adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("owner approve derived a message, want none (consumed by grammar)")
	}
	// 77 now approved in memory.
	assertApproved(t, h.store, "77")
	// Persisted: an independent fresh Load sees 77.
	fresh := authz.NewStore(storePath)
	if err := fresh.Load(); err != nil {
		t.Fatalf("fresh Load: %v", err)
	}
	if c, _ := fresh.Contains("77"); !c {
		t.Error("approval not persisted to disk (fresh Load does not contain 77)")
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingApproved)); got != 1 {
		t.Errorf("pairing_approved audit count = %d, want 1", got)
	}
	ownerMsgs := notifTo(h.notifier, "1")
	if len(ownerMsgs) != 1 || !strings.Contains(strings.ToLower(ownerMsgs[0]), "approved") {
		t.Errorf("owner confirmation = %v, want one 'approved' message", ownerMsgs)
	}
}

// TC-152-03 edge: malformed owner grammar ("approve", "approve foo") is rejected without
// crashing, no store mutation, a malformed audit event, and no fall-through to routing.
func TestTC152_03_MalformedOwnerGrammarRejected(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"approve", "approve foo", "deny", "deny xyz"} {
		t.Run(bad, func(t *testing.T) {
			srv := scriptedServer(t, pairingUpdate{text: bad, senderID: 1})
			h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved-"+strings.ReplaceAll(bad, " ", "_")+".json"))
			_, ok, err := h.adapter.Next()
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if ok {
				t.Fatalf("malformed %q derived a message, want none", bad)
			}
			if h.store.Len() != 0 {
				t.Errorf("malformed %q mutated store (len=%d), want 0", bad, h.store.Len())
			}
			if got := countAuditReason(h.sink, string(authz.ReasonPairingMalformed)); got != 1 {
				t.Errorf("pairing_malformed audit count = %d, want 1", got)
			}
		})
	}
}

// --- TC-152-04 -----------------------------------------------------------------------

// TC-152-04: owner's "deny 77" does NOT add 77, audits the denial, confirms to the owner,
// and a later message from 77 still re-enters the pending path.
func TestTC152_04_OwnerDenyDoesNotApproveAndAllowsReRequest(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "approved.json")
	srv := scriptedServer(t,
		pairingUpdate{text: "deny 77", senderID: 1},         // owner denies
		pairingUpdate{text: "let me back in", senderID: 77}, // denied sender re-requests
	)
	h := newPairingHarness(t, srv, 1, storePath)

	// (1) owner deny
	if _, ok, err := h.adapter.Next(); err != nil || ok {
		t.Fatalf("owner deny Next: ok=%v err=%v, want no message", ok, err)
	}
	assertNotApproved(t, h.store, "77")
	if got := countAuditReason(h.sink, string(authz.ReasonPairingDenied)); got != 1 {
		t.Errorf("pairing_denied audit count = %d, want 1", got)
	}
	ownerMsgs := notifTo(h.notifier, "1")
	if len(ownerMsgs) != 1 || !strings.Contains(strings.ToLower(ownerMsgs[0]), "denied") {
		t.Errorf("owner deny confirmation = %v, want one 'denied' message", ownerMsgs)
	}

	// (2) denied sender re-requests → back in the pending path (not blocked, not approved).
	if _, ok, err := h.adapter.Next(); err != nil || ok {
		t.Fatalf("re-request Next: ok=%v err=%v, want no message (pending)", ok, err)
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingRequest)); got != 1 {
		t.Errorf("pairing_request audit count after re-request = %d, want 1", got)
	}
	assertNotApproved(t, h.store, "77")
}

// --- TC-152-05 (LOAD-BEARING) --------------------------------------------------------

// TC-152-05: a stranger's "approve <own-id>" CANNOT self-approve. It routes as ordinary
// pending input (owner notified, sender told pending); the store is unchanged and NO
// "approved" reply is ever sent to the stranger.
//
// Mutation resistance: if the owner gate were removed (a stranger reaching the approve
// branch), 77 WOULD be added to the store and an "approved" confirmation sent — both are
// asserted false here, so that mutation FAILS this test.
func TestTC152_05_StrangerCannotSelfApprove(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t, pairingUpdate{text: "approve 77", senderID: 77}) // stranger 77, owner is 1
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"))

	_, ok, err := h.adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal("stranger approve derived a message, want pending (no message)")
	}
	// CRUX: store unchanged — the stranger did not self-approve.
	assertNotApproved(t, h.store, "77")
	if h.store.Len() != 0 {
		t.Errorf("store len = %d after stranger self-approve attempt, want 0", h.store.Len())
	}
	// It fired the ordinary pending path: pairing_request audit, owner notified, sender
	// told pending — NOT the approval path.
	if got := countAuditReason(h.sink, string(authz.ReasonPairingRequest)); got != 1 {
		t.Errorf("pairing_request audit count = %d, want 1 (routed as ordinary pending)", got)
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingApproved)); got != 0 {
		t.Errorf("pairing_approved audit count = %d, want 0 (must NOT self-approve)", got)
	}
	// The stranger (chat 77) got a "pending" reply — never an "approved" confirmation.
	for _, m := range notifTo(h.notifier, "77") {
		if strings.Contains(strings.ToLower(m), "approved") {
			t.Errorf("stranger received an 'approved' reply %q — self-approval leaked", m)
		}
	}
	if senderMsgs := notifTo(h.notifier, "77"); len(senderMsgs) != 1 || !strings.Contains(strings.ToLower(senderMsgs[0]), "pending") {
		t.Errorf("stranger reply = %v, want one 'pending' message", senderMsgs)
	}
	// The owner (chat 1) was notified of the pending request (proving ordinary path).
	if ownerMsgs := notifTo(h.notifier, "1"); len(ownerMsgs) != 1 {
		t.Errorf("owner notifications = %d, want 1 (stranger routed as pending request)", len(ownerMsgs))
	}
}

// TC-152-05 edge: a stranger's "deny 77" (denying its own request) is likewise ordinary
// pending input, not the owner grammar.
func TestTC152_05_StrangerDenyIsOrdinaryPending(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t, pairingUpdate{text: "deny 77", senderID: 77})
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"))

	if _, ok, err := h.adapter.Next(); err != nil || ok {
		t.Fatalf("Next: ok=%v err=%v, want no message", ok, err)
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingRequest)); got != 1 {
		t.Errorf("pairing_request audit count = %d, want 1", got)
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingDenied)); got != 0 {
		t.Errorf("pairing_denied audit count = %d, want 0 (stranger deny is not owner grammar)", got)
	}
}

// --- TC-152-06 -----------------------------------------------------------------------

// TC-152-06: an already-approved sender's plaintext command routes normally (armor →
// deriveMessage → MsgStatus), never through the pairing/pending/owner-notify machinery.
func TestTC152_06_ApprovedSenderRoutesNormally(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t, pairingUpdate{text: "status", senderID: 77})
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"), "77") // 77 pre-approved

	msg, ok, err := h.adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if !ok {
		t.Fatal("approved sender's status was not routed, want MsgStatus")
	}
	if msg.Kind != supervisor.MsgStatus {
		t.Errorf("msg.Kind = %v, want MsgStatus", msg.Kind)
	}
	// Armor ran on the accepted plaintext (task 151 pipeline reached).
	if h.guard.calls != 1 {
		t.Errorf("armor calls = %d, want 1 (approved-sender plaintext runs armor)", h.guard.calls)
	}
	// No pairing machinery fired.
	if got := countAuditReason(h.sink, string(authz.ReasonPairingRequest)); got != 0 {
		t.Errorf("pairing_request audit count = %d, want 0 (approved sender bypasses pairing)", got)
	}
	if len(h.notifier.Sent()) != 0 {
		t.Errorf("notifications sent = %d, want 0 for approved sender", len(h.notifier.Sent()))
	}
	assertHasAuditReason(t, h.sink, string(authz.ReasonPlaintextAccepted))
}

// --- TC-152-07 (LOAD-BEARING ORDERING) -----------------------------------------------

// TC-152-07: the owner's "approve 123" is consumed by the grammar (no message), while the
// owner's SEPARATE later "status" routes normally as MsgStatus — proving interception is
// per-message and grammar-gated (owner-ID AND approve/deny text), not a blanket
// "everything from the owner skips routing" rule.
func TestTC152_07_ApproveConsumedButOwnerStatusRoutes(t *testing.T) {
	dir := t.TempDir()
	// The owner (1) approves 123, then — now itself approved via... no: the owner must be
	// approved to route "status". Approve the OWNER's own ID first is contrived; instead
	// seed the owner as approved so its non-grammar commands route (owner identity ≠
	// approved by itself). This isolates the ordering property under test.
	srv := scriptedServer(t,
		pairingUpdate{text: "approve 123", senderID: 1}, // consumed by grammar
		pairingUpdate{text: "status", senderID: 1},      // routes normally
	)
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"), "1") // owner pre-approved

	// (1) approve 123 → consumed, no message.
	if _, ok, err := h.adapter.Next(); err != nil || ok {
		t.Fatalf("approve 123 Next: ok=%v err=%v, want no message (consumed by grammar)", ok, err)
	}
	if c, _ := h.store.Contains("123"); !c {
		t.Error("approve 123 did not add 123 to the store")
	}
	// (2) owner "status" → routes normally as MsgStatus (owner is approved, non-grammar text).
	msg, ok, err := h.adapter.Next()
	if err != nil {
		t.Fatalf("status Next: %v", err)
	}
	if !ok || msg.Kind != supervisor.MsgStatus {
		t.Fatalf("owner status routed as ok=%v kind=%v, want MsgStatus", ok, msg.Kind)
	}
}

// TC-152-07 edge: owner prose starting with "approve" but not approve<numeric-id>-shaped
// ("approve of this plan") does NOT match the grammar as a valid approval — it is a
// malformed owner command (consumed, audited malformed), NOT routed as a new goal and NOT
// a valid approval. This asserts the grammar is structural (verb + numeric arg).
func TestTC152_07_OwnerProseNotAValidApproval(t *testing.T) {
	dir := t.TempDir()
	srv := scriptedServer(t, pairingUpdate{text: "approve of this plan", senderID: 1})
	h := newPairingHarness(t, srv, 1, filepath.Join(dir, "approved.json"), "1")

	_, ok, err := h.adapter.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ok {
		t.Fatal(`"approve of this plan" derived a message, want malformed (consumed)`)
	}
	// It is NOT a valid approval — nothing new added.
	if h.store.Len() != 1 { // only the seeded owner "1"
		t.Errorf("store len = %d, want 1 (prose is not a valid approval)", h.store.Len())
	}
	if got := countAuditReason(h.sink, string(authz.ReasonPairingMalformed)); got != 1 {
		t.Errorf("pairing_malformed audit count = %d, want 1", got)
	}
}

// --- TC-152-08 (LOAD-BEARING: restart survival) --------------------------------------

// TC-152-08: an approval made by one adapter/store instance is visible to an
// independently constructed second instance sharing ONLY the store file path — the crux
// fix over OpenClaw's non-persistent dmPolicy.
//
// Negative control (proves on-disk persistence, not a shared in-memory reference): a
// second instance pointed at a DIFFERENT (empty) path rejects sender 77 as pending.
func TestTC152_08_ApprovalSurvivesSimulatedRestart(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "approved.json")

	// Instance #1: drive the owner approve flow to completion.
	srv1 := scriptedServer(t, pairingUpdate{text: "approve 77", senderID: 1})
	h1 := newPairingHarness(t, srv1, 1, storePath)
	if _, ok, err := h1.adapter.Next(); err != nil || ok {
		t.Fatalf("instance#1 approve Next: ok=%v err=%v", ok, err)
	}
	assertApproved(t, h1.store, "77")

	// Discard instance #1 entirely. Instance #2: a brand-new object graph sharing ONLY the
	// file path (fresh Load at construction — exactly a restarted process).
	srv2 := scriptedServer(t, pairingUpdate{text: "status", senderID: 77})
	h2 := newPairingHarness(t, srv2, 1, storePath) // Load() reads the persisted approval

	msg, ok, err := h2.adapter.Next()
	if err != nil {
		t.Fatalf("instance#2 status Next: %v", err)
	}
	if !ok || msg.Kind != supervisor.MsgStatus {
		t.Fatalf("instance#2 routed 77 as ok=%v kind=%v, want MsgStatus (approval survived restart)", ok, msg.Kind)
	}

	// Negative control: an instance at a different (empty) path must NOT accept 77.
	srv3 := scriptedServer(t, pairingUpdate{text: "status", senderID: 77})
	h3 := newPairingHarness(t, srv3, 1, filepath.Join(dir, "other-empty.json"))
	if _, ok, err := h3.adapter.Next(); err != nil {
		t.Fatalf("negative-control Next: %v", err)
	} else if ok {
		t.Fatal("negative control accepted 77 from an empty store — test is not exercising on-disk persistence")
	}
}

// --- shared assertion helpers --------------------------------------------------------

func countAuditReason(sink *audit.FakeSink, reason string) int {
	n := 0
	for _, ev := range sink.Events() {
		if ev.Detail.Reason == reason {
			n++
		}
	}
	return n
}

func assertApproved(t *testing.T, store *authz.Store, id string) {
	t.Helper()
	c, err := store.Contains(id)
	if err != nil {
		t.Fatalf("Contains(%s): %v", id, err)
	}
	if !c {
		t.Errorf("store does not contain %s, want approved", id)
	}
}

func assertNotApproved(t *testing.T, store *authz.Store, id string) {
	t.Helper()
	c, err := store.Contains(id)
	if err != nil {
		t.Fatalf("Contains(%s): %v", id, err)
	}
	if c {
		t.Errorf("store contains %s, want NOT approved", id)
	}
}
