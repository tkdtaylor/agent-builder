// Package telegram implements a secure goal-source channel via the Telegram Bot API.
// It polls getUpdates, applies envelope verification (Ed25519) and decryption (X25519+AEAD),
// routes the plaintext through armor.Guard, and delivers validated goals over supervisor.GoalSource.
package telegram

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
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
	maxBodyBytes      int64
	maxMessageBytes   int64
	guardTimeout      time.Duration
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
	// MaxBodyBytes is the maximum size in bytes for the getUpdates response body.
	// Default: 4 MB if not set.
	MaxBodyBytes int64
	// MaxMessageBytes is the maximum size in bytes for a single message's Text field.
	// Default: 64 KB if not set.
	MaxMessageBytes int64
	// GuardTimeout is the timeout for armor guard DecideContent calls.
	// Default: 5 seconds if not set.
	GuardTimeout time.Duration
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
	if config.MaxBodyBytes <= 0 {
		config.MaxBodyBytes = 4 * 1024 * 1024 // 4 MB default
	}
	if config.MaxMessageBytes <= 0 {
		config.MaxMessageBytes = 64 * 1024 // 64 KB default
	}
	if config.GuardTimeout <= 0 {
		config.GuardTimeout = 5 * time.Second // 5 seconds default
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
		maxBodyBytes:      config.MaxBodyBytes,
		maxMessageBytes:   config.MaxMessageBytes,
		guardTimeout:      config.GuardTimeout,
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

		// Check message text length to prevent processing oversized messages (SEC-001)
		if int64(len(update.Message.Text)) > a.maxMessageBytes {
			a.logger.Debug("message text exceeds max length", "update_id", update.UpdateID, "text_len", len(update.Message.Text), "max", a.maxMessageBytes)
			a.emitAuditEvent("text_too_long")
			continue
		}

		// Parse the envelope JSON
		var env envelope.Envelope
		if err := json.Unmarshal([]byte(update.Message.Text), &env); err != nil {
			a.logger.Debug("envelope parse failed", "error", err)
			a.emitAuditEvent("envelope_parse_failed: " + err.Error())
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
			// Classify rejection reason using errors.Is for sentinel matching
			reason := "envelope_rejected"
			if errors.Is(err, envelope.ErrUnknownKey) || errors.Is(err, envelope.ErrBadSignature) {
				reason = "unknown_key" // Group unknown key and bad signature
			} else if errors.Is(err, envelope.ErrReplay) {
				reason = "replay_detected"
			} else if errors.Is(err, envelope.ErrStaleTimestamp) {
				reason = "replay_detected" // Stale timestamps are grouped with replay for audit purposes
			}
			a.emitAuditEvent(reason)
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
			a.emitAuditEvent("candidate_invalid")
			continue
		}

		// Route through armor.Guard with a bounded timeout context (SEC-002)
		ctx, cancel := context.WithTimeout(context.Background(), a.guardTimeout)
		decision, err := a.contentGuard.DecideContent(ctx, candidate)
		cancel()

		if err != nil {
			a.logger.Debug("armor guard error", "error", err)
			a.emitAuditEvent("armor_error")
			continue
		}

		a.logger.Debug("armor decision", "outcome", decision.Outcome)

		// If armor blocks or quarantines, emit audit and skip.
		if decision.Outcome != ingestion.DecisionAllow {
			a.logger.Debug("armor rejected", "reason", decision.Reason)
			a.emitAuditEvent("armor_blocked: " + decision.Reason)
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
// Wraps the response body in a LimitReader to prevent OOM from oversized responses.
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

	// Wrap response body with a size limit to prevent OOM from oversized bodies
	limitedBody := io.LimitReader(resp.Body, a.maxBodyBytes)

	var result GetUpdatesResponse
	if err := json.NewDecoder(limitedBody).Decode(&result); err != nil {
		return nil, fmt.Errorf("telegram: failed to decode getUpdates response: %w", err)
	}

	if !result.OK {
		return nil, fmt.Errorf("telegram: getUpdates failed: %s", result.Error)
	}

	return result.Result, nil
}

// emitAuditEvent emits a channel-reject event to the audit sink with the given reason.
// Never includes sensitive data (bot token, keys, full plaintext).
func (a *Adapter) emitAuditEvent(reason string) {
	if a.auditSink == nil {
		return
	}

	// Emit a channel-reject audit event with the reason.
	ev := audit.AuditEvent{
		Action: audit.ActionChannelReject,
		RunID:  "telegram-channel",
		TaskID: "telegram-inbound",
		Detail: audit.EventDetail{
			Reason: reason,
		},
	}

	// Try to append; if it fails, silently ignore to avoid breaking the channel loop.
	_ = a.auditSink.Append(ev)
}
