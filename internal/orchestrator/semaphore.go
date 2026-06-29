package orchestrator

import "context"

// Semaphore is the fleet-wide worker concurrency bound (ADR 054 §1). It is a
// weighted counting semaphore acquired INSIDE dispatchPlan's per-sub-goal
// goroutine (Acquire(1) before o.dispatch, Release(1) deferred after), so the
// total number of live workers across ALL concurrent goals never exceeds the
// bound. It is deliberately implemented on a buffered channel rather than pulling
// in golang.org/x/sync/semaphore: the agent-builder dispatch path only ever
// acquires/releases a single permit, so a 1-permit-per-slot channel is the
// smallest correct primitive and adds no dependency.
//
// A Semaphore is safe for concurrent use. The zero value is NOT usable; construct
// with NewSemaphore.
type Semaphore struct {
	// slots holds one token per available permit. Acquire receives a token
	// (blocking when none are free); Release sends one back. cap(slots) is the
	// bound N; len(slots) is the number of currently-free permits.
	slots chan struct{}
}

// NewSemaphore constructs a Semaphore bounding concurrency at n permits. n is
// clamped to a minimum of 1 (a zero/negative bound would deadlock every dispatch);
// the CLI assembly is responsible for surfacing a misconfigured bound to the
// operator, but the primitive itself never produces a useless zero-permit
// semaphore.
func NewSemaphore(n int) *Semaphore {
	if n < 1 {
		n = 1
	}
	return &Semaphore{slots: make(chan struct{}, n)}
}

// Acquire takes one permit, blocking until one is free or ctx is cancelled. It
// returns ctx.Err() if the context is done before a permit is acquired (so a
// cancelled goal does not park forever on a saturated fleet); on success it
// returns nil and the caller MUST pair it with exactly one Release.
func (s *Semaphore) Acquire(ctx context.Context) error {
	select {
	case s.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquireN attempts to take n permits without blocking. It returns true and
// holds the permits on success, or false (holding none) if fewer than n are
// currently free. It is the test seam for "no permit leaked after drain"
// (TC-112-07): after every goal terminates, TryAcquireN(cap) must succeed. On
// success the caller MUST ReleaseN(n) to return them.
func (s *Semaphore) TryAcquireN(n int) bool {
	taken := 0
	for taken < n {
		select {
		case s.slots <- struct{}{}:
			taken++
		default:
			// Roll back the partial acquisition so the semaphore is left unchanged
			// on failure.
			for i := 0; i < taken; i++ {
				<-s.slots
			}
			return false
		}
	}
	return true
}

// Release returns one permit. It must be called exactly once per successful
// Acquire — the dispatchPlan goroutine defers it so the permit is released on
// every path (success, dispatch error, or panic recovery), never leaked.
func (s *Semaphore) Release() {
	<-s.slots
}

// ReleaseN returns n permits (the inverse of a successful TryAcquireN(n)).
func (s *Semaphore) ReleaseN(n int) {
	for i := 0; i < n; i++ {
		<-s.slots
	}
}

// Cap reports the semaphore's permit bound N.
func (s *Semaphore) Cap() int {
	return cap(s.slots)
}
