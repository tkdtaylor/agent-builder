package worker_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/worker"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// keyset holds the full set of Ed25519 + X25519 key material for both ends.
type keyset struct {
	orchEdPub  ed25519.PublicKey
	orchEdPriv ed25519.PrivateKey
	orchXPub   [32]byte
	orchXPriv  [32]byte

	workerEdPub  ed25519.PublicKey
	workerEdPriv ed25519.PrivateKey
	workerXPub   [32]byte
	workerXPriv  [32]byte
}

func newKeyset(t *testing.T) keyset {
	t.Helper()
	orchEdPub, orchEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("orch ed25519 keygen: %v", err)
	}
	orchXPub, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("orch x25519 keygen: %v", err)
	}
	workerEdPub, workerEdPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("worker ed25519 keygen: %v", err)
	}
	workerXPub, workerXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("worker x25519 keygen: %v", err)
	}
	return keyset{
		orchEdPub: orchEdPub, orchEdPriv: orchEdPriv, orchXPub: orchXPub, orchXPriv: orchXPriv,
		workerEdPub: workerEdPub, workerEdPriv: workerEdPriv, workerXPub: workerXPub, workerXPriv: workerXPriv,
	}
}

func (k keyset) workItemSender() *worker.Sender {
	return worker.NewWorkItemSender(worker.SenderConfig{
		EdPriv:   k.orchEdPriv,
		XPriv:    k.orchXPriv,
		RecipPub: k.workerXPub,
	})
}

func (k keyset) workItemReceiver(sink audit.Sink, cache *envelope.ReplayCache) *worker.Receiver {
	return worker.NewWorkItemReceiver(worker.ReceiverConfig{
		SignPub:     k.orchEdPub,
		RecipPriv:   k.workerXPriv,
		SenderPub:   k.orchXPub,
		ReplayCache: cache,
		AuditSink:   sink,
	})
}

func (k keyset) resultSender() *worker.Sender {
	return worker.NewResultSender(worker.SenderConfig{
		EdPriv:   k.workerEdPriv,
		XPriv:    k.workerXPriv,
		RecipPub: k.orchXPub,
	})
}

func (k keyset) resultReceiver(sink audit.Sink) *worker.Receiver {
	return worker.NewResultReceiver(worker.ReceiverConfig{
		SignPub:   k.workerEdPub,
		RecipPriv: k.orchXPriv,
		SenderPub: k.workerXPub,
		AuditSink: sink,
	})
}

func rejectReasons(sink *audit.FakeSink) []string {
	var reasons []string
	for _, ev := range sink.Events() {
		if ev.Action == audit.ActionChannelReject {
			reasons = append(reasons, ev.Detail.Reason)
		}
	}
	return reasons
}

// TC-083-01: Orchestrator sends a work-item as a signed+sealed envelope; the worker
// verifies, the From/To roles are correct, the open round-trips byte-exact, the nonce
// is fresh per dispatch, and the plaintext never appears in the envelope JSON.
func TestTC083_01_WorkItemSignedSealedRoundTrip(t *testing.T) {
	k := newKeyset(t)
	sender := k.workItemSender()

	task := supervisor.Task{ID: "sg-1", Repo: "exec-sandbox", Spec: "add rate limiter"}

	env, err := sender.DispatchWorkItem(task)
	if err != nil {
		t.Fatalf("DispatchWorkItem: %v", err)
	}

	// Signature valid under the orchestrator's Ed25519 public key.
	if err := envelope.Verify(env, k.orchEdPub); err != nil {
		t.Fatalf("Verify(received, orchEdPub) = %v, want nil", err)
	}

	// Roles set correctly.
	if env.From != "orchestrator" || env.To != "worker" {
		t.Fatalf("roles: from=%q to=%q, want orchestrator/worker", env.From, env.To)
	}

	// Open with worker X25519 priv + orchestrator X25519 pub → byte-exact task.
	nonceBytes, err := hex.DecodeString(env.Nonce)
	if err != nil {
		t.Fatalf("nonce hex decode: %v", err)
	}
	var nonce [24]byte
	copy(nonce[:], nonceBytes)
	ciphertext, err := hex.DecodeString(env.Payload)
	if err != nil {
		t.Fatalf("payload hex decode: %v", err)
	}
	plaintext, err := envelope.Open(ciphertext, nonce, k.workerXPriv, k.orchXPub)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	var got supervisor.Task
	if err := json.Unmarshal(plaintext, &got); err != nil {
		t.Fatalf("unmarshal opened payload: %v", err)
	}
	if !reflect.DeepEqual(got, task) {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, task)
	}

	// Nonce unique per dispatch (fresh crypto/rand nonce, never reused).
	env2, err := sender.DispatchWorkItem(task)
	if err != nil {
		t.Fatalf("second DispatchWorkItem: %v", err)
	}
	if env.Nonce == "" {
		t.Fatal("nonce is empty")
	}
	if env.Nonce == env2.Nonce {
		t.Fatalf("nonce reused across dispatches: %q", env.Nonce)
	}

	// Plaintext spec must NOT appear anywhere in the marshalled envelope JSON.
	envJSON, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal env: %v", err)
	}
	if bytes.Contains(envJSON, []byte("add rate limiter")) {
		t.Fatalf("plaintext spec leaked into envelope JSON: %s", envJSON)
	}
}

// TC-083-02: Worker result returned as a signed envelope; orchestrator verifies before
// incorporating. Happy path round-trips; a tampered Sig is NOT incorporated and an audit
// rejection event is emitted with the specific sentinel.
func TestTC083_02_ResultVerifiedBeforeIncorporation(t *testing.T) {
	k := newKeyset(t)
	sender := k.resultSender()

	result := supervisor.Result{Branch: "task/083", OK: true}

	env, err := sender.DispatchResult(result)
	if err != nil {
		t.Fatalf("DispatchResult: %v", err)
	}

	// Happy path: orchestrator receiver returns byte-exact result.
	sink := audit.NewFakeSink()
	recv := k.resultReceiver(sink)
	got, err := recv.ReceiveResult(env)
	if err != nil {
		t.Fatalf("ReceiveResult (happy): %v", err)
	}
	if !reflect.DeepEqual(got, result) {
		t.Fatalf("result round-trip mismatch: got %+v, want %+v", got, result)
	}
	if reasons := rejectReasons(sink); len(reasons) != 0 {
		t.Fatalf("happy path emitted reject events: %v", reasons)
	}

	// Envelope roles set correctly (TC-083-02 explicit role assertion).
	if env.From != "worker" || env.To != "orchestrator" {
		t.Fatalf("result envelope roles: from=%q to=%q, want worker→orchestrator", env.From, env.To)
	}

	// Tamper path: flip one byte of the Sig field.
	tampered := env
	sigBytes, err := hex.DecodeString(tampered.Sig)
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	sigBytes[0] ^= 0xFF
	tampered.Sig = hex.EncodeToString(sigBytes)

	tamperSink := audit.NewFakeSink()
	tamperRecv := k.resultReceiver(tamperSink)
	gotTampered, err := tamperRecv.ReceiveResult(tampered)
	if err == nil {
		t.Fatal("ReceiveResult on tampered Sig returned nil error, want bad-signature error")
	}
	if !errors.Is(err, envelope.ErrBadSignature) {
		t.Fatalf("ReceiveResult tampered error = %v, want errors.Is ErrBadSignature", err)
	}
	// Result NOT incorporated → zero value.
	if !reflect.DeepEqual(gotTampered, supervisor.Result{}) {
		t.Fatalf("tampered result was incorporated: %+v, want zero value", gotTampered)
	}
	// Audit event emitted with a rejection reason.
	reasons := rejectReasons(tamperSink)
	if len(reasons) != 1 {
		t.Fatalf("tamper path emitted %d reject events, want 1: %v", len(reasons), reasons)
	}
	if reasons[0] != "bad_signature" && reasons[0] != "unknown_key" {
		t.Fatalf("tamper reject reason = %q, want bad_signature/unknown_key group", reasons[0])
	}
}

// TC-083-03: Replayed work-item (same nonce) rejected at the receiver via ReplayCache;
// audit event emitted with "replay"; the replayed work-item is NOT processed.
func TestTC083_03_ReplayRejectedAtReceiver(t *testing.T) {
	k := newKeyset(t)
	sender := k.workItemSender()

	task := supervisor.Task{ID: "sg-1", Repo: "exec-sandbox", Spec: "add rate limiter"}
	env, err := sender.DispatchWorkItem(task)
	if err != nil {
		t.Fatalf("DispatchWorkItem: %v", err)
	}

	// Shared replay cache across both deliveries.
	cache := envelope.NewReplayCache(0)
	sink := audit.NewFakeSink()
	recv := k.workItemReceiver(sink, cache)

	// First delivery: accepted, byte-exact task.
	got, err := recv.ReceiveWorkItem(env)
	if err != nil {
		t.Fatalf("first ReceiveWorkItem: %v", err)
	}
	if !reflect.DeepEqual(got, task) {
		t.Fatalf("first delivery round-trip mismatch: got %+v, want %+v", got, task)
	}

	// Second delivery (same nonce): rejected as replay.
	gotReplay, err := recv.ReceiveWorkItem(env)
	if err == nil {
		t.Fatal("second ReceiveWorkItem (replay) returned nil error, want replay error")
	}
	if !errors.Is(err, envelope.ErrReplay) {
		t.Fatalf("replay error = %v, want errors.Is ErrReplay", err)
	}
	// Replayed work-item NOT processed → zero value.
	if !reflect.DeepEqual(gotReplay, supervisor.Task{}) {
		t.Fatalf("replayed work-item was processed: %+v, want zero value", gotReplay)
	}
	// Audit event with "replay" in the reason.
	var sawReplay bool
	for _, reason := range rejectReasons(sink) {
		if strings.Contains(reason, "replay") {
			sawReplay = true
		}
	}
	if !sawReplay {
		t.Fatalf("no audit reject event containing %q: %v", "replay", rejectReasons(sink))
	}
}

// TC-083-05: Missing key material → worker-transport startup fails loudly with a NAMED
// error before accepting work. Covers (a) env unset and (b) file absent.
func TestTC083_05_MissingKeyMaterialFailsAtStartup(t *testing.T) {
	var orchXPriv, workerXPub [32]byte // zero seal keys are fine; signing key is what's checked

	// (a) env unset.
	t.Setenv(worker.EnvWorkerSigningKey, "")
	if err := os.Unsetenv(worker.EnvWorkerSigningKey); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	sender, err := worker.NewWorkItemSenderFromEnv(orchXPriv, workerXPub)
	if err == nil {
		t.Fatal("NewWorkItemSenderFromEnv with unset key returned nil error, want named startup error")
	}
	if !errors.Is(err, worker.ErrMissingSigningKey) {
		t.Fatalf("unset-key error = %v, want errors.Is ErrMissingSigningKey", err)
	}
	if !strings.Contains(err.Error(), worker.EnvWorkerSigningKey) {
		t.Fatalf("unset-key error %q does not name %q", err.Error(), worker.EnvWorkerSigningKey)
	}
	if sender != nil {
		t.Fatalf("unset-key returned non-nil sender: %+v", sender)
	}

	// (b) file absent.
	absent := filepath.Join(t.TempDir(), "does-not-exist.key")
	t.Setenv(worker.EnvWorkerSigningKey, absent)
	sender, err = worker.NewWorkItemSenderFromEnv(orchXPriv, workerXPub)
	if err == nil {
		t.Fatal("NewWorkItemSenderFromEnv with absent file returned nil error, want named startup error")
	}
	if !errors.Is(err, worker.ErrMissingSigningKey) {
		t.Fatalf("absent-file error = %v, want errors.Is ErrMissingSigningKey", err)
	}
	if !strings.Contains(err.Error(), worker.EnvWorkerSigningKey) {
		t.Fatalf("absent-file error %q does not name %q", err.Error(), worker.EnvWorkerSigningKey)
	}
	if sender != nil {
		t.Fatalf("absent-file returned non-nil sender: %+v", sender)
	}
}

// TC-083-05 (positive): a well-formed key file loads and constructs a usable sender.
func TestTC083_05_ValidKeyMaterialLoads(t *testing.T) {
	_, edPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "signing.key")
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(edPriv)), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	t.Setenv(worker.EnvWorkerSigningKey, keyPath)

	var orchXPriv, workerXPub [32]byte
	sender, err := worker.NewWorkItemSenderFromEnv(orchXPriv, workerXPub)
	if err != nil {
		t.Fatalf("NewWorkItemSenderFromEnv with valid key: %v", err)
	}
	if sender == nil {
		t.Fatal("valid key returned nil sender")
	}
}

// SEC carry-forward (task 098 SEC-001): a valid-but-wrong-direction envelope (correct
// keys, wrong From/To) is rejected on role assertion even though Verify+Open succeed.
func TestRoleMismatchRejected(t *testing.T) {
	k := newKeyset(t)

	// Build an envelope that the work-item receiver can cryptographically open (sealed
	// orch→worker, signed by orch) but whose declared roles are wrong (operator→worker).
	plaintext, err := json.Marshal(supervisor.Task{ID: "x", Spec: "y"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	ciphertext, nonce, err := envelope.Seal(plaintext, k.orchXPriv, k.workerXPub)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	env := envelope.Envelope{
		From:    "operator", // wrong: receiver expects "orchestrator"
		To:      "worker",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
	}
	env, err = envelope.Sign(env, k.orchEdPriv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	sink := audit.NewFakeSink()
	recv := k.workItemReceiver(sink, envelope.NewReplayCache(0))
	got, err := recv.ReceiveWorkItem(env)
	if err == nil {
		t.Fatal("ReceiveWorkItem on role-mismatched envelope returned nil error")
	}
	if !errors.Is(err, worker.ErrRoleMismatch) {
		t.Fatalf("role-mismatch error = %v, want errors.Is ErrRoleMismatch", err)
	}
	if !reflect.DeepEqual(got, supervisor.Task{}) {
		t.Fatalf("role-mismatched work-item was processed: %+v", got)
	}
	var sawRoleReject bool
	for _, reason := range rejectReasons(sink) {
		if reason == "role_mismatch" {
			sawRoleReject = true
		}
	}
	if !sawRoleReject {
		t.Fatalf("no role_mismatch audit event: %v", rejectReasons(sink))
	}
}
