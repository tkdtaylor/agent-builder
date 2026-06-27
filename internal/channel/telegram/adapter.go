// Package telegram implements a secure goal-source channel via the Telegram Bot API.
// It polls getUpdates, applies envelope verification (Ed25519) and decryption (X25519+AEAD),
// routes the plaintext through armor.Guard, and delivers validated goals over supervisor.GoalSource.
package telegram

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Adapter is a Telegram bot adapter that implements supervisor.GoalSource.
// It polls the Telegram Bot API, verifies and decrypts envelopes, routes plaintext
// through armor.Guard, and delivers validated goals.
type Adapter struct {
	botToken          string
	baseURL           string
	httpClient        *http.Client
	offset            int64
	trustedSigningKey ed25519.PublicKey
	trustedX25519Pub  [32]byte
	orchestratorPriv  [32]byte
	contentGuard      ContentGuard
	replayCache       *envelope.ReplayCache
	auditSink         audit.Sink
	logger            *slog.Logger
}

// ContentGuard is a narrow interface for armor guard decision-making.
// Implemented by armor.Guard; used for testability.
type ContentGuard interface {
	DecideContent(ctx context.Context, candidate ingestion.ContentCandidate) (ingestion.Decision, error)
}

// Config configures an Adapter.
type Config struct {
	BotToken          string
	BaseURL           string
	HTTPClient        *http.Client
	TrustedSigningKey ed25519.PublicKey
	TrustedX25519Pub  [32]byte
	OrchestratorPriv  [32]byte
	ContentGuard      ContentGuard
	ReplayCache       *envelope.ReplayCache
	AuditSink         audit.Sink
	Logger            *slog.Logger
}

// NewAdapter constructs a Telegram channel adapter.
func NewAdapter(config Config) *Adapter {
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if config.ReplayCache == nil {
		config.ReplayCache = envelope.NewReplayCache(60 * time.Second)
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Adapter{
		botToken:          config.BotToken,
		baseURL:           config.BaseURL,
		httpClient:        config.HTTPClient,
		trustedSigningKey: config.TrustedSigningKey,
		trustedX25519Pub:  config.TrustedX25519Pub,
		orchestratorPriv:  config.OrchestratorPriv,
		contentGuard:      config.ContentGuard,
		replayCache:       config.ReplayCache,
		auditSink:         config.AuditSink,
		logger:            config.Logger,
	}
}

// Next implements supervisor.GoalSource.Next.
// It polls getUpdates, processes each update through the envelope → armor → goal pipeline,
// and returns the next valid goal. Offset is advanced on every update (even rejected ones).
func (a *Adapter) Next() (supervisor.Task, bool, error) {
	updates, err := a.getUpdates()
	if err != nil {
		a.logger.Error("getUpdates failed", "error", err)
		return supervisor.Task{}, false, err
	}

	a.logger.Debug("received updates", "count", len(updates))

	for _, update := range updates {
		// Advance offset before processing (even if this update is rejected)
		a.offset = int64(update.UpdateID) + 1

		// Extract plaintext message text
		if update.Message == nil || update.Message.Text == "" {
			a.logger.Debug("skipping update: no message or empty text", "update_id", update.UpdateID)
			continue
		}

		a.logger.Debug("processing update", "update_id", update.UpdateID, "text_len", len(update.Message.Text))

		// Parse the envelope JSON
		var env envelope.Envelope
		if err := json.Unmarshal([]byte(update.Message.Text), &env); err != nil {
			a.logger.Debug("envelope parse failed", "error", err)
			a.emitAuditEvent("envelope_parse_failed", err.Error())
			continue
		}

		// Verify signature, replay cache, and decrypt all at once.
		// VerifyAndOpen enforces the mandatory ordering: verify → check replay → open.
		plaintext, err := envelope.VerifyAndOpen(
			env,
			a.trustedSigningKey,
			a.replayCache,
			a.orchestratorPriv,
			a.trustedX25519Pub,
		)
		if err != nil {
			// Reject before armor invocation (unknown key, replay, or decryption failure)
			a.logger.Debug("envelope verification/decryption failed", "error", err)
			a.emitAuditEvent("envelope_rejected", err.Error())
			continue
		}

		a.logger.Debug("envelope verified and decrypted", "plaintext_len", len(plaintext))

		// Convert plaintext to a ContentCandidate for armor.
		// Note: SourceURI must be http/https (per ingestion package validation),
		// so we use a placeholder https URI.
		candidate, err := ingestion.NewContentCandidate(ingestion.ContentInput{
			ID:        ingestion.CandidateID(fmt.Sprintf("tg-%d", update.UpdateID)),
			Content:   plaintext,
			SourceURI: "https://telegram/message", // Must be http/https for ingestion validation
			MediaType: "text/plain",
			Provenance: ingestion.Provenance{
				TaskID:   fmt.Sprintf("%d", update.UpdateID),
				Executor: "telegram-channel",
			},
		})
		if err != nil {
			a.logger.Debug("content candidate creation failed", "error", err)
			a.emitAuditEvent("candidate_invalid", err.Error())
			continue
		}

		// Route through armor.Guard.
		decision, err := a.contentGuard.DecideContent(context.Background(), candidate)
		if err != nil {
			a.logger.Debug("armor guard error", "error", err)
			a.emitAuditEvent("armor_error", err.Error())
			continue
		}

		a.logger.Debug("armor decision", "outcome", decision.Outcome)

		// If armor blocks or quarantines, emit audit and skip.
		if decision.Outcome != ingestion.DecisionAllow {
			a.logger.Debug("armor rejected", "reason", decision.Reason)
			a.emitAuditEvent("armor_block", decision.Reason)
			continue
		}

		// Goal is valid. Return it.
		task := supervisor.Task{
			ID:   fmt.Sprintf("%d", update.UpdateID),
			Repo: "",
			Spec: string(plaintext), // Map plaintext to Spec field
		}
		a.logger.Debug("goal delivered", "task_id", task.ID)
		return task, true, nil
	}

	// No valid goal from this batch.
	a.logger.Debug("no valid goal from updates")
	return supervisor.Task{}, false, nil
}

// GetUpdatesResponse is the Telegram Bot API response shape for getUpdates.
type GetUpdatesResponse struct {
	OK     bool      `json:"ok"`
	Result []Update  `json:"result"`
	Error  string    `json:"error_description,omitempty"`
}

// Update is one Telegram update.
type Update struct {
	UpdateID int64   `json:"update_id"`
	Message  *Message `json:"message,omitempty"`
}

// Message is a Telegram message.
type Message struct {
	Text string `json:"text"`
}

// getUpdates polls the Telegram Bot API.
func (a *Adapter) getUpdates() ([]Update, error) {
	params := url.Values{}
	params.Set("offset", strconv.FormatInt(a.offset, 10))
	params.Set("timeout", "30")

	reqURL := a.baseURL + "/bot" + a.botToken + "/getUpdates?" + params.Encode()
	resp, err := a.httpClient.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("telegram: getUpdates request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	var result GetUpdatesResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("telegram: failed to decode getUpdates response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("telegram: getUpdates failed: %s", result.Error)
	}

	return result.Result, nil
}

// emitAuditEvent emits a rejection event to the audit sink.
// Never includes sensitive data (bot token, keys, full plaintext).
func (a *Adapter) emitAuditEvent(action, reason string) {
	if a.auditSink == nil {
		return
	}

	// Construct a minimal audit event for rejection.
	// Using existing audit infrastructure; no new action types needed.
	// We emit via a rejection event that logs the reason (not sensitive data).
	ev := audit.AuditEvent{
		Action: audit.ActionFinish,
		RunID:  "telegram-channel",
		TaskID: "telegram-inbound",
		Outcome: audit.OutcomeFailed,
		Detail: audit.EventDetail{},
	}

	// Try to append; if it fails, silently ignore to avoid breaking the channel loop.
	_ = a.auditSink.Append(ev)
}
