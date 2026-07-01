package telegram

import (
	"context"
	"sync"
)

// FakeNotifier is an in-memory test double for PairingNotifier. It records every
// (chatID, text) pair passed to Notify in order, so pairing-flow tests can assert
// "pending replied to sender" and "owner notified with approve/deny instruction"
// without a live bot. Safe for concurrent use.
type FakeNotifier struct {
	mu   sync.Mutex
	sent []SentNotification
}

// SentNotification is one recorded plaintext notification.
type SentNotification struct {
	ChatID string
	Text   string
}

// NewFakeNotifier constructs an empty FakeNotifier.
func NewFakeNotifier() *FakeNotifier {
	return &FakeNotifier{}
}

// Notify records the (chatID, text) and returns nil. No network, no crypto.
func (f *FakeNotifier) Notify(_ context.Context, chatID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, SentNotification{ChatID: chatID, Text: text})
	return nil
}

// Sent returns a copy of all recorded notifications in send order.
func (f *FakeNotifier) Sent() []SentNotification {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]SentNotification, len(f.sent))
	copy(out, f.sent)
	return out
}
