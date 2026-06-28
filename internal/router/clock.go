package router

import "time"

// Clock is a seam for the wall clock. The router uses it for all "now" checks
// so tests can inject a controllable clock instead of calling time.Now() directly.
// This is required by REQ-093-03: no time.Sleep in tests.
type Clock interface {
	Now() time.Time
}

// realClock is the production implementation — it delegates to time.Now().
type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// FakeClock is a test-injectable clock. Its time starts at T (supplied at
// construction) and advances only when Advance is called. No time.Sleep needed.
type FakeClock struct {
	now time.Time
}

// NewFakeClock returns a FakeClock whose initial time is t.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time { return c.now }

// Advance moves the fake clock forward by d. Passing a negative duration moves
// it backward (not recommended, but allowed for tests that need to reset).
func (c *FakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }
