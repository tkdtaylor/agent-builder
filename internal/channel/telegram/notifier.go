package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// PlaintextNotifier is the production PairingNotifier (ADR 063 Decision 3). It POSTs a
// PLAINTEXT message to a Telegram chat via the bot sendMessage API — used for the pairing
// flow's "pending" reply to an unknown sender and the approve/deny notification to the
// owner's chat.
//
// This is deliberately distinct from ReplyAdapter: ReplyAdapter seals+signs an
// envelope.Envelope for the operator's goal-result channel, whereas pairing notifications
// are plaintext by construction (the unknown sender holds no envelope key). Both hit the
// same sendMessage endpoint; only PlaintextNotifier sends unsealed text, and only on the
// opt-in pairing path. It never touches key material.
type PlaintextNotifier struct {
	botToken   string
	baseURL    string
	httpClient *http.Client
	logger     *slog.Logger
}

// PlaintextNotifierConfig configures a PlaintextNotifier.
type PlaintextNotifierConfig struct {
	BotToken   string
	BaseURL    string
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// NewPlaintextNotifier constructs a PlaintextNotifier from config.
func NewPlaintextNotifier(cfg PlaintextNotifierConfig) *PlaintextNotifier {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &PlaintextNotifier{
		botToken:   cfg.BotToken,
		baseURL:    cfg.BaseURL,
		httpClient: cfg.HTTPClient,
		logger:     cfg.Logger,
	}
}

// Notify POSTs text as a plaintext Telegram sendMessage to chatID. A non-2xx / not-OK
// response is returned as an error (the adapter logs it and continues — a failed pairing
// notification is never fatal to the poll loop).
func (n *PlaintextNotifier) Notify(ctx context.Context, chatID, text string) error {
	reqBody := sendMessageRequest{ChatID: chatID, Text: text}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("telegram notify: marshal sendMessage body: %w", err)
	}

	reqURL := n.baseURL + "/bot" + n.botToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("telegram notify: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram notify: sendMessage request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result sendMessageResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&result); err != nil {
		return fmt.Errorf("telegram notify: decode sendMessage response: %w", err)
	}
	if !result.OK {
		return fmt.Errorf("telegram notify: sendMessage failed: %s", result.Description)
	}
	n.logger.Debug("telegram notify: sent plaintext pairing message", "chat_id", chatID)
	return nil
}
