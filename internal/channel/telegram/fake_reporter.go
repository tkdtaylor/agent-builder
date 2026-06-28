package telegram

import (
	"context"
	"sync"
)

// FakeReporter is an in-memory test double for supervisor.Reporter.
// It records every text passed to Report in order so that test code can
// assert "approval solicited" / "summary reported" without a live bot.
//
// FakeReporter is safe for concurrent use.
type FakeReporter struct {
	mu       sync.Mutex
	reported []string
}

// NewFakeReporter constructs an empty FakeReporter.
func NewFakeReporter() *FakeReporter {
	return &FakeReporter{}
}

// Report records text and returns nil. It never fails, never makes network
// calls, and never touches crypto.
func (f *FakeReporter) Report(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reported = append(f.reported, text)
	return nil
}

// Reported returns a copy of all reported strings in the order they were
// passed to Report.
func (f *FakeReporter) Reported() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.reported))
	copy(out, f.reported)
	return out
}
