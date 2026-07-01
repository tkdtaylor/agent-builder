package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The real PlaintextNotifier POSTs UNSEALED plaintext to the Telegram sendMessage
// endpoint at the given chat ID — the runtime-observable wire behavior of the pairing
// flow's owner/sender notifications. This test captures the actual HTTP request the
// notifier emits (path, chat_id, and that the body text is the plaintext, not an
// envelope) so the wire contract is asserted, not just the FakeNotifier stand-in.
func TestPlaintextNotifier_PostsPlaintextToSendMessage(t *testing.T) {
	var gotPath string
	var gotBody sendMessageRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	n := NewPlaintextNotifier(PlaintextNotifierConfig{
		BotToken:   "test-token",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	})

	const plaintext = `User 77 requests access — reply "approve 77" or "deny 77"`
	if err := n.Notify(context.Background(), "1", plaintext); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	if gotPath != "/bottest-token/sendMessage" {
		t.Errorf("POST path = %q, want /bottest-token/sendMessage", gotPath)
	}
	if gotBody.ChatID != "1" {
		t.Errorf("chat_id = %q, want 1 (owner chat)", gotBody.ChatID)
	}
	// The wire text is the PLAINTEXT verbatim — NOT a sealed envelope JSON.
	if gotBody.Text != plaintext {
		t.Errorf("wire text = %q, want the verbatim plaintext %q", gotBody.Text, plaintext)
	}
}

// A not-OK Telegram response surfaces as an error (the adapter logs it and continues —
// a failed pairing notification is never fatal to the poll loop).
func TestPlaintextNotifier_NotOKResponseIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "description": "chat not found"})
	}))
	defer srv.Close()

	n := NewPlaintextNotifier(PlaintextNotifierConfig{BotToken: "t", BaseURL: srv.URL, HTTPClient: srv.Client()})
	if err := n.Notify(context.Background(), "1", "hi"); err == nil {
		t.Fatal("Notify returned nil error on not-OK response, want error")
	}
}
