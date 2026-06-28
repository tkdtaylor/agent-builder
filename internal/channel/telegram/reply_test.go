package telegram_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Compile-time assertions (TC-098-05): both concrete and fake satisfy supervisor.Reporter.
var _ supervisor.Reporter = (*telegram.ReplyAdapter)(nil)
var _ supervisor.Reporter = (*telegram.FakeReporter)(nil)

// TC-098-06: make fitness-envelope-isolation — internal/envelope must not appear in
// internal/supervisor's dependency graph after adding supervisor.Reporter.
// TC-098-07: make fitness-supervisor-isolation — supervisor graph must have no
// executor/LLM/web after the seam is added; make check must be green.
// These invariants are verified as L3 fitness targets, not as a Go test function;
// the comments here anchor the markers for the spec-verifier grep.
var _ = "TC-098-06 TC-098-07" // marker anchor for grep

// generateReplyKeys generates keys for outbound reply testing.
// Returns (orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv).
func generateReplyKeys(t *testing.T) (
	orchEdPub ed25519.PublicKey,
	orchEdPriv ed25519.PrivateKey,
	orchXPub [32]byte,
	orchXPriv [32]byte,
	opXPub [32]byte,
	opXPriv [32]byte,
) {
	t.Helper()
	var err error
	orchEdPub, orchEdPriv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate orchestrator Ed25519 key: %v", err)
	}
	orchXPub, orchXPriv, err = envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate orchestrator X25519 keypair: %v", err)
	}
	opXPub, opXPriv, err = envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate operator X25519 keypair: %v", err)
	}
	return
}

// stubSendMessageServer stands up an httptest server that accepts POST /bot<token>/sendMessage,
// records the last request body, and returns {"ok":true}.
func stubSendMessageServer(t *testing.T) (server *httptest.Server, bodyCapture *[]byte, callCount *int) {
	t.Helper()
	var lastBody []byte
	count := 0
	bodyCapture = &lastBody
	callCount = &count
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		b, _ := io.ReadAll(r.Body)
		lastBody = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	return
}

// TestTC098_02_ReportPostsSealedEnvelope verifies that Report POSTs exactly one
// sendMessage whose body carries a sealed envelope and NOT the plaintext.
func TestTC098_02_ReportPostsSealedEnvelope(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, _ := generateReplyKeys(t)
	// suppress unused variable warnings — these are part of the key material even if not all are used here.
	_ = orchEdPub
	_ = orchXPub

	server, bodyCapture, callCount := stubSendMessageServer(t)

	const reportedText = "Approve plan? 2 sub-goals: docs-fix, coding-agent"

	adapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   "TEST_TOKEN_098",
		BaseURL:    server.URL,
		ChatID:     "12345",
		HTTPClient: server.Client(),
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
	})

	err := adapter.Report(context.Background(), reportedText)
	if err != nil {
		t.Fatalf("Report returned error: %v", err)
	}

	// TC-098-02: exactly one POST to sendMessage.
	if *callCount != 1 {
		t.Errorf("expected 1 sendMessage POST, got %d", *callCount)
	}

	// TC-098-02: the request body contains an envelope.Envelope with non-empty fields.
	var outer struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(*bodyCapture, &outer); err != nil {
		t.Fatalf("failed to parse outer POST body: %v — body: %s", err, *bodyCapture)
	}

	var env envelope.Envelope
	if err := json.Unmarshal([]byte(outer.Text), &env); err != nil {
		t.Fatalf("Text field does not parse as envelope.Envelope: %v — text: %s", err, outer.Text)
	}

	if env.Nonce == "" {
		t.Error("envelope.Nonce is empty")
	}
	if env.TS == "" {
		t.Error("envelope.TS is empty")
	}
	if env.Payload == "" {
		t.Error("envelope.Payload is empty")
	}
	if env.Sig == "" {
		t.Error("envelope.Sig is empty")
	}

	// TC-098-02 / TC-098-08 (partial): the literal reported text must NOT appear in the raw body.
	if bytes.Contains(*bodyCapture, []byte(reportedText)) {
		t.Errorf("plaintext %q found in raw POST body — it must not appear on the wire", reportedText)
	}
}

// TestTC098_03_RoundTrip is the load-bearing assertion: the emitted reply envelope
// is accepted by the inbound VerifyAndOpen path (complementary keys) and recovers
// the exact reported text byte-for-byte.
func TestTC098_03_RoundTrip(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	server, bodyCapture, _ := stubSendMessageServer(t)

	const reportedText = "Approve plan? 2 sub-goals: docs-fix, coding-agent"

	adapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   "TOKEN",
		BaseURL:    server.URL,
		ChatID:     "42",
		HTTPClient: server.Client(),
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
	})

	if err := adapter.Report(context.Background(), reportedText); err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	// Extract the emitted envelope from the captured POST body.
	var outer struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(*bodyCapture, &outer); err != nil {
		t.Fatalf("parse outer body: %v", err)
	}
	var emittedEnv envelope.Envelope
	if err := json.Unmarshal([]byte(outer.Text), &emittedEnv); err != nil {
		t.Fatalf("parse emitted envelope: %v", err)
	}

	// TC-098-03: feed the emitted envelope through the PRODUCTION inbound VerifyAndOpen
	// with complementary keys:
	//   trusted signing key  = orchestrator's Ed25519 pub (orchEdPub)
	//   recipient private    = operator's X25519 priv    (opXPriv)
	//   sender public        = orchestrator's X25519 pub  (orchXPub)
	plaintext, err := envelope.VerifyAndOpen(
		emittedEnv,
		orchEdPub,
		envelope.NewReplayCache(60*time.Second),
		opXPriv,
		orchXPub,
	)
	if err != nil {
		t.Fatalf("VerifyAndOpen(emittedEnv, complementary keys) returned error: %v", err)
	}
	// TC-098-03: string(plaintext) must be byte-equal to the reported text.
	if string(plaintext) != reportedText {
		t.Errorf("round-trip plaintext = %q, want %q", string(plaintext), reportedText)
	}
}

// TestTC098_04_TamperedEnvelopeFailsVerify proves that a payload-mutated envelope and
// an untrusted-signer envelope both fail VerifyAndOpen with the expected sentinels.
func TestTC098_04_TamperedEnvelopeFailsVerify(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	server, bodyCapture, _ := stubSendMessageServer(t)

	adapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   "TOKEN",
		BaseURL:    server.URL,
		ChatID:     "42",
		HTTPClient: server.Client(),
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
	})

	if err := adapter.Report(context.Background(), "secret plan"); err != nil {
		t.Fatalf("Report failed: %v", err)
	}

	var outer struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(*bodyCapture, &outer); err != nil {
		t.Fatalf("parse outer body: %v", err)
	}
	var emittedEnv envelope.Envelope
	if err := json.Unmarshal([]byte(outer.Text), &emittedEnv); err != nil {
		t.Fatalf("parse emitted envelope: %v", err)
	}

	// TC-098-04 Input A: mutate one byte of Payload (flip last two hex chars), keep Sig.
	// The signature covers the canonical body including Payload, so this fails verify.
	tampered := emittedEnv
	if len(tampered.Payload) >= 2 {
		last := tampered.Payload[len(tampered.Payload)-2:]
		switch last {
		case "00":
			tampered.Payload = tampered.Payload[:len(tampered.Payload)-2] + "ff"
		default:
			tampered.Payload = tampered.Payload[:len(tampered.Payload)-2] + "00"
		}
	}
	cacheA := envelope.NewReplayCache(60 * time.Second)
	_, errA := envelope.VerifyAndOpen(tampered, orchEdPub, cacheA, opXPriv, orchXPub)
	if errA == nil {
		t.Error("Input A (mutated Payload): expected non-nil error, got nil")
	} else if !errors.Is(errA, envelope.ErrBadSignature) {
		t.Errorf("Input A: expected errors.Is(err, ErrBadSignature), got: %v", errA)
	}

	// TC-098-04 Input B: unmodified envelope, wrong (unrelated) Ed25519 public key as trusted signer.
	unrelatedEdPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate unrelated Ed25519 key: %v", err)
	}
	cacheB := envelope.NewReplayCache(60 * time.Second)
	_, errB := envelope.VerifyAndOpen(emittedEnv, unrelatedEdPub, cacheB, opXPriv, orchXPub)
	if errB == nil {
		t.Error("Input B (wrong trusted signer): expected non-nil error, got nil")
	} else if !errors.Is(errB, envelope.ErrBadSignature) && !errors.Is(errB, envelope.ErrUnknownKey) {
		t.Errorf("Input B: expected ErrBadSignature or ErrUnknownKey via errors.Is, got: %v", errB)
	}

	// Suppress unused variable warnings for keys we generated but tested indirectly.
	_ = orchEdPub
	_ = orchXPub
	_ = orchXPriv
}

// TestTC098_05_FakeReporterCapturesInOrder verifies that FakeReporter records
// reported strings in order. The compile-time interface assertions are at package
// level above (var _ supervisor.Reporter = ...).
func TestTC098_05_FakeReporterCapturesInOrder(t *testing.T) {
	fake := telegram.NewFakeReporter()
	if err := fake.Report(context.Background(), "first"); err != nil {
		t.Fatalf("fake.Report(first): %v", err)
	}
	if err := fake.Report(context.Background(), "second"); err != nil {
		t.Fatalf("fake.Report(second): %v", err)
	}

	got := fake.Reported()
	want := []string{"first", "second"}
	if len(got) != len(want) {
		t.Fatalf("Reported() len = %d, want %d; got %v", len(got), len(want), got)
	}
	for i, g := range got {
		if g != want[i] {
			t.Errorf("Reported()[%d] = %q, want %q", i, g, want[i])
		}
	}
}

// TestTC098_08_BotTokenAndKeysAbsentFromLogs verifies that the sentinel bot token,
// private key material (hex + base64), and PEM markers never appear in log output.
func TestTC098_08_BotTokenAndKeysAbsentFromLogs(t *testing.T) {
	const botTokenSentinel = "REPLY_BOT_TOKEN_SENTINEL_98765"

	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, _ := generateReplyKeys(t)
	_ = orchEdPub
	_ = orchXPub

	server, _, _ := stubSendMessageServer(t)

	var logBuffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuffer, &slog.HandlerOptions{Level: slog.LevelDebug}))

	adapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   botTokenSentinel,
		BaseURL:    server.URL,
		ChatID:     "99",
		HTTPClient: server.Client(),
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
		Logger:     logger,
	})

	_ = adapter.Report(context.Background(), "summary: 2 sub-goals complete")
	logOutput := logBuffer.String()

	// TC-098-08: bot token must not appear in logs.
	if strings.Contains(logOutput, botTokenSentinel) {
		t.Errorf("bot token sentinel %q found in logs", botTokenSentinel)
	}

	// TC-098-08: orchestrator X25519 private key bytes must not appear (hex or base64).
	xPrivHex := hex.EncodeToString(orchXPriv[:])
	if strings.Contains(logOutput, xPrivHex) {
		t.Errorf("orchestrator X25519 private key (hex) found in logs")
	}
	xPrivB64 := base64.StdEncoding.EncodeToString(orchXPriv[:])
	if strings.Contains(logOutput, xPrivB64) {
		t.Errorf("orchestrator X25519 private key (base64) found in logs")
	}

	// TC-098-08: orchestrator Ed25519 private key seed must not appear in logs (hex).
	edSeedHex := hex.EncodeToString(orchEdPriv.Seed())
	if strings.Contains(logOutput, edSeedHex) {
		t.Errorf("orchestrator Ed25519 private key seed (hex) found in logs")
	}

	// TC-098-08: no PEM block marker in logs.
	if strings.Contains(logOutput, "-----BEGIN") {
		t.Errorf("PEM block marker found in logs")
	}
}
