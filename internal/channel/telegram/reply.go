package telegram

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/envelope"
)

// ReplyAdapter is the Telegram outbound adapter. It implements supervisor.Reporter
// by sealing+signing the reply text as an internal/envelope.Envelope (ADR 045) and
// POSTing it via the bot sendMessage API.
//
// Key roles for outbound replies (orchestrator → operator):
//   - Ed25519 signer:        orchestrator's Ed25519 private key  (orchEdPriv)
//   - X25519 seal sender:    orchestrator's X25519 private key   (orchXPriv)
//   - X25519 seal recipient: operator's X25519 public key        (opXPub)
//
// This is the exact mirror of the inbound adapter's key roles:
//   inbound (operator → orchestrator): operator signs + seals; orchestrator opens.
//   outbound (orchestrator → operator): orchestrator signs + seals; operator opens.
type ReplyAdapter struct {
	botToken   string
	baseURL    string
	chatID     string
	httpClient *http.Client
	orchEdPriv ed25519.PrivateKey // orchestrator's Ed25519 private key (signs)
	orchXPriv  [32]byte           // orchestrator's X25519 private key (sender, seals)
	opXPub     [32]byte           // operator's X25519 public key (recipient, sealed to)
	logger     *slog.Logger
}

// ReplyConfig configures a ReplyAdapter.
type ReplyConfig struct {
	BotToken   string
	BaseURL    string
	ChatID     string
	HTTPClient *http.Client
	// OrchEdPriv is the orchestrator's Ed25519 private key, used to sign replies.
	OrchEdPriv ed25519.PrivateKey
	// OrchXPriv is the orchestrator's X25519 private key, used as the seal sender.
	OrchXPriv [32]byte
	// OpXPub is the operator's X25519 public key, the seal recipient.
	OpXPub [32]byte
	Logger *slog.Logger
}

// NewReplyAdapter constructs a ReplyAdapter from config.
func NewReplyAdapter(cfg ReplyConfig) *ReplyAdapter {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &ReplyAdapter{
		botToken:   cfg.BotToken,
		baseURL:    cfg.BaseURL,
		chatID:     cfg.ChatID,
		httpClient: cfg.HTTPClient,
		orchEdPriv: cfg.OrchEdPriv,
		orchXPriv:  cfg.OrchXPriv,
		opXPub:     cfg.OpXPub,
		logger:     cfg.Logger,
	}
}

// Report implements supervisor.Reporter. It seals the text with X25519/AEAD
// (orchestrator private key as sender, operator public key as recipient), signs
// the sealed envelope with the orchestrator's Ed25519 private key, and POSTs
// the result to the Telegram sendMessage endpoint as the message text.
//
// The plaintext never appears on the wire — only the sealed+signed envelope is
// transmitted. Private key material is never logged.
func (r *ReplyAdapter) Report(ctx context.Context, text string) error {
	// Step 1: Seal the plaintext.
	// Orchestrator is the sender (orchXPriv), operator is the recipient (opXPub).
	ciphertext, nonce, err := envelope.Seal([]byte(text), r.orchXPriv, r.opXPub)
	if err != nil {
		return fmt.Errorf("telegram reply: seal failed: %w", err)
	}

	// Step 2: Build the envelope.
	env := envelope.Envelope{
		From:    "orchestrator",
		To:      "operator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	// Step 3: Sign the envelope with the orchestrator's Ed25519 private key.
	env, err = envelope.Sign(env, r.orchEdPriv)
	if err != nil {
		return fmt.Errorf("telegram reply: sign failed: %w", err)
	}

	// Step 4: Marshal the envelope to JSON for the wire.
	envJSON, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("telegram reply: marshal envelope: %w", err)
	}

	r.logger.Debug("telegram reply: sending sealed envelope", "nonce_prefix", env.Nonce[:8])

	// Step 5: POST to sendMessage.
	return r.sendMessage(ctx, string(envJSON))
}

// sendMessageRequest is the Telegram Bot API sendMessage request body.
type sendMessageRequest struct {
	ChatID string `json:"chat_id"`
	Text   string `json:"text"`
}

// sendMessageResponse is the Telegram Bot API sendMessage response (minimal).
type sendMessageResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description,omitempty"`
}

// sendMessage POSTs the text as a Telegram sendMessage API call.
// The text parameter MUST already be the sealed+signed envelope JSON — the
// plaintext must never be passed here directly.
func (r *ReplyAdapter) sendMessage(ctx context.Context, text string) error {
	reqBody := sendMessageRequest{
		ChatID: r.chatID,
		Text:   text,
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("telegram reply: marshal sendMessage body: %w", err)
	}

	// Build URL: <baseURL>/bot<token>/sendMessage
	reqURL := r.baseURL + "/bot" + r.botToken + "/sendMessage"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram reply: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram reply: sendMessage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result sendMessageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result); err != nil {
		return fmt.Errorf("telegram reply: decode sendMessage response: %w", err)
	}

	if !result.OK {
		return fmt.Errorf("telegram reply: sendMessage failed: %s", result.Description)
	}

	r.logger.Debug("telegram reply: sendMessage succeeded")
	return nil
}
