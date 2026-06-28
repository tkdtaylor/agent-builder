package audit

import "sync"

// Compile-time assertion: *FakeSink must satisfy the Sink interface.
var _ Sink = (*FakeSink)(nil)

// FakeSink is an in-process Sink for tests. It records appended events in order,
// records Seal calls, validates each event before recording it, and performs zero
// I/O. It mirrors the shape of sandbox.FakeRunner.
//
// FakeSink is safe for concurrent use: the orchestrator's concurrent dispatch
// (task 086) appends to one shared sink from N worker goroutines, and the audit
// chain must be appended serially. The mutex guards every Append/Seal/read so the
// race detector stays clean under concurrent fleet-audit writes.
type FakeSink struct {
	mu        sync.Mutex
	events    []AuditEvent
	sealCount int
}

// NewFakeSink returns an empty FakeSink ready for use.
func NewFakeSink() *FakeSink {
	return &FakeSink{
		events: make([]AuditEvent, 0),
	}
}

// Append validates ev and, if valid, records it in order. It returns ErrAfterSeal
// when Seal has already been called, or a *ValidationError when the event is invalid.
func (f *FakeSink) Append(ev AuditEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sealCount > 0 {
		return ErrAfterSeal
	}
	if err := Validate(ev); err != nil {
		return err
	}
	// Copy by value — AuditEvent contains only value types, so struct copy is safe.
	f.events = append(f.events, ev)
	return nil
}

// Seal records that the sink was sealed. Callers can assert Sealed() is true in
// tests to confirm the supervisor sealed the sink before teardown.
func (f *FakeSink) Seal() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sealCount++
	return nil
}

// Events returns the events recorded by Append in append order. It returns a
// copy of the internal slice so callers cannot corrupt the fake's record through
// mutation.
func (f *FakeSink) Events() []AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	copied := make([]AuditEvent, len(f.events))
	copy(copied, f.events)
	return copied
}

// Sealed reports whether Seal has been called at least once.
func (f *FakeSink) Sealed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sealCount > 0
}

// SealCount returns the number of times Seal has been called. It is normally
// 1; a value other than 1 indicates a supervisor sealing the sink more or fewer
// times than expected.
func (f *FakeSink) SealCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sealCount
}
