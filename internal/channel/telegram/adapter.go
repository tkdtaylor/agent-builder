// Package telegram implements a secure inbound channel via the Telegram Bot API.
// It polls getUpdates, applies envelope verification (Ed25519) and decryption (X25519+AEAD),
// routes the plaintext through armor.Guard, and delivers typed supervisor.Messages
// (new-goal / status / info / cancel) over supervisor.MessageSource.
//
// Kind/GoalID derivation happens at the adapter edge — after envelope-verify + armor — on
// already-trusted plaintext. The control plane only ever sees Message.GoalID (ADR 054 §2).
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
	"strings"
	"sync"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/ingestion"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Adapter is a Telegram bot adapter that implements supervisor.MessageSource.
// It polls the Telegram Bot API, verifies and decrypts envelopes, routes plaintext
// through armor.Guard, derives the message kind and goalID at the adapter edge, and
// delivers typed supervisor.Messages (new-goal/status/info/cancel) to the control plane.
//
// Per-message goal IDs are derived from the Telegram chat/message identity (ADR 054 §2):
//   - A new-goal gets a fresh goalID from its chat+message ID ("tg-<chatID>-<msgID>").
//   - A status/info/cancel reply-to threads the EXISTING goalID stored in the adapter's
//     internalIDCache, keyed by the original message ID.
//
// Security invariant: kind derivation runs ONLY on verified, armor-passed plaintext.
// The envelope-verify + armor pipeline is unchanged (tasks 080/097/098).
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

	// goalIDCache maps a Telegram message ID (the original new-goal message ID as
	// a string) to the derived goalID. Used to thread reply-to commands to the
	// correct goal actor. Guarded by mu (ADR 054 §2: the control loop is the single
	// Next() caller — the mutex is defensive for the reply-to lookup path).
	mu          sync.Mutex
	goalIDCache map[string]string
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
		goalIDCache:       make(map[string]string),
	}
}

// Next implements supervisor.MessageSource.Next.
// It polls getUpdates, processes each update through the envelope → armor pipeline,
// derives the MessageKind and GoalID at the adapter edge from the plaintext and
// reply-to context, and returns the next typed supervisor.Message.
// Offset is advanced on every update (even rejected ones).
func (a *Adapter) Next() (supervisor.Message, bool, error) {
	updates, err := a.getUpdates()
	if err != nil {
		a.logger.Error("getUpdates failed", "error", err)
		return supervisor.Message{}, false, err
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
		// SECURITY: kind derivation runs ONLY on plaintext that passes this step.
		plaintext, err := envelope.VerifyAndOpen(
			env,
			a.trustedSigningKey,
			a.replayCache,
			a.orchestratorPriv,
			a.trustedX25519Pub,
		)
		if err != nil {
			// Reject before armor invocation (unknown key, replay, decryption failure, or stale timestamp)
			a.logger.Debug("envelope verification/decryption failed", "error", err)
			// Classify rejection reason using errors.Is for sentinel matching
			reason := "envelope_rejected"
			if errors.Is(err, envelope.ErrUnknownKey) || errors.Is(err, envelope.ErrBadSignature) {
				reason = "unknown_key" // Group unknown key and bad signature
			} else if errors.Is(err, envelope.ErrReplay) {
				reason = "replay_detected"
			} else if errors.Is(err, envelope.ErrStaleTimestamp) {
				reason = "replay_detected" // Stale timestamps are grouped with replay for audit purposes
			} else if errors.Is(err, envelope.ErrDecryptionFailed) {
				reason = "decryption_failed"
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

		// SECURITY: plaintext has passed envelope-verify + armor. Now derive kind/GoalID
		// at the adapter edge from the trusted plaintext and message identity.
		msg := a.deriveMessage(update, plaintext)
		a.logger.Debug("message derived", "kind", msg.Kind, "goal_id", msg.GoalID)
		return msg, true, nil
	}

	// No valid message from this batch.
	a.logger.Debug("no valid message from updates")
	return supervisor.Message{}, false, nil
}

// deriveMessage maps a verified, armor-passed Telegram update to a typed supervisor.Message
// at the adapter edge (ADR 054 §2). Kind/GoalID derivation happens here — the control
// plane only ever sees Message.GoalID.
//
// Derivation rules (applied to the trusted plaintext):
//   - "status" (bare or with goalID text) → MsgStatus; reply-to threads the goalID.
//   - "info <text...>" → MsgInfo; reply-to is required for the goalID (bare → empty GoalID).
//   - "cancel" → MsgCancel; reply-to threads the goalID.
//   - Anything else (including multi-word goals, code specs, etc.) → MsgNewGoal with a
//     fresh goalID derived from "tg-<chatID>-<msgID>", stored in goalIDCache for future
//     reply-to threading.
//
// The goalIDCache maps the Telegram message ID (string) to the GoalID, enabling reply-to
// commands to thread the correct goal without the control plane ever touching Telegram IDs.
func (a *Adapter) deriveMessage(update Update, plaintext []byte) supervisor.Message {
	text := string(plaintext)
	msgID := strconv.FormatInt(update.Message.MessageID, 10)

	// Derive the chatID from the nested Chat object (Telegram API shape).
	chatID := ""
	if update.Message.Chat != nil {
		chatID = strconv.FormatInt(update.Message.Chat.ID, 10)
	}

	// Determine the reply-to goalID (threaded from a prior new-goal message).
	replyToGoalID := ""
	if update.Message.ReplyToMessage != nil {
		replyMsgID := strconv.FormatInt(update.Message.ReplyToMessage.MessageID, 10)
		a.mu.Lock()
		replyToGoalID = a.goalIDCache[replyMsgID]
		a.mu.Unlock()
	}

	// Parse command verb (first word) from plaintext.
	verb, rest := splitVerb(text)

	switch strings.ToLower(verb) {
	case "status":
		// "status" → MsgStatus; reply-to threads goalID (bare "status" → fleet, GoalID="").
		goalID := replyToGoalID
		if goalID == "" && rest != "" {
			// "status <goalID>" text form (env/stdin grammar compat, no reply-to)
			goalID = rest
		}
		return supervisor.Message{Kind: supervisor.MsgStatus, GoalID: goalID}

	case "info":
		// "info <text...>" → MsgInfo; reply-to provides the goalID.
		return supervisor.Message{Kind: supervisor.MsgInfo, GoalID: replyToGoalID, Text: rest}

	case "cancel":
		// "cancel" → MsgCancel; reply-to provides the goalID.
		return supervisor.Message{Kind: supervisor.MsgCancel, GoalID: replyToGoalID}

	case "confirm", "go", "proceed":
		// "confirm", "go", "proceed" → MsgConfirm; reply-to provides the goalID.
		// If sent without a reply-to (or with an unknown cache entry), it is treated
		// as MsgNewGoal (falls through to default).
		if replyToGoalID != "" {
			return supervisor.Message{Kind: supervisor.MsgConfirm, GoalID: replyToGoalID}
		}
		fallthrough

	default:
		// Any other plaintext (including multi-word goals) → MsgNewGoal.
		// Derive a fresh goalID from the chat+message identity (no collision across
		// concurrent goals from different chats or messages in the same chat).
		goalID := fmt.Sprintf("tg-%s-%s", chatID, msgID)

		// Cache the message ID → goalID mapping so future reply-to commands can thread it.
		a.mu.Lock()
		a.goalIDCache[msgID] = goalID
		a.mu.Unlock()

		return supervisor.Message{
			Kind:   supervisor.MsgNewGoal,
			GoalID: goalID,
			Goal:   supervisor.Task{ID: goalID, Spec: text},
		}
	}
}

// splitVerb splits a command string into its first word (verb) and the remainder.
// The remainder is trimmed of leading whitespace. If the text is a single word,
// rest is empty. This is used to distinguish command verbs (status/info/cancel) from
// bare goal text.
func splitVerb(text string) (verb, rest string) {
	// Find the first whitespace boundary.
	for i, r := range text {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			verb = text[:i]
			rest = strings.TrimLeft(text[i+1:], " \t\n\r")
			return verb, rest
		}
	}
	// No whitespace found — the entire text is the verb.
	return text, ""
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
	MessageID      int64    `json:"message_id"`
	Text           string   `json:"text"`
	Chat           *Chat    `json:"chat,omitempty"`
	ReplyToMessage *Message `json:"reply_to_message,omitempty"`
}

// Chat is a Telegram chat (minimal — only the ID is used for goal ID derivation).
type Chat struct {
	ID int64 `json:"id"`
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
