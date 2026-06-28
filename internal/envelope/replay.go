package envelope

import (
	"fmt"
	"sync"
	"time"
)

// ReplayCache guards against replay attacks by tracking timestamps and nonces.
// It maintains a time-freshness window and a bounded nonce set, with automatic
// eviction of nonces older than 2×Window.
//
// The cache is not thread-safe by design; the caller is responsible for
// synchronization if concurrent access is needed.
type ReplayCache struct {
	// Window is the freshness window (default 60s). Timestamps outside
	// [now-Window, now+Window] are rejected.
	Window time.Duration

	// nonces maps nonce strings to the timestamp they were first seen.
	// Used to detect replays and to track age for eviction.
	nonces map[string]time.Time

	// mu protects nonces.
	mu sync.Mutex
}

// NewReplayCache constructs a ReplayCache with the given window.
// The default window is 60 seconds if zero is passed.
func NewReplayCache(window time.Duration) *ReplayCache {
	if window == 0 {
		window = 60 * time.Second
	}
	return &ReplayCache{
		Window: window,
		nonces: make(map[string]time.Time),
	}
}

// Check validates a nonce and timestamp against the replay cache.
// Returns nil if the nonce is fresh and within the time window;
// returns a wrapped sentinel error if:
//   - The timestamp is older than Window (ErrStaleTimestamp)
//   - The timestamp is newer than Window (ErrStaleTimestamp)
//   - The nonce has been seen before (ErrReplay)
//
// On success, the nonce is recorded in the cache and will be retained for 2×Window.
func (rc *ReplayCache) Check(nonce string, ts time.Time) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	now := time.Now().UTC()

	// Check freshness: timestamp must be within [now-Window, now+Window].
	if ts.Before(now.Add(-rc.Window)) || ts.After(now.Add(rc.Window)) {
		return fmt.Errorf("timestamp outside freshness window: %w", ErrStaleTimestamp)
	}

	// Check for replay: nonce must not have been seen before.
	if _, seen := rc.nonces[nonce]; seen {
		return fmt.Errorf("nonce has been seen before: %w", ErrReplay)
	}

	// Evict old nonces (older than 2×Window).
	rc.evict(now)

	// Record the new nonce.
	rc.nonces[nonce] = ts

	return nil
}

// Len returns the current size of the nonce set. Useful for testing.
func (rc *ReplayCache) Len() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return len(rc.nonces)
}

// evict removes nonces older than 2×Window. Must be called with mu held.
func (rc *ReplayCache) evict(now time.Time) {
	horizon := now.Add(-2 * rc.Window)
	for nonce, ts := range rc.nonces {
		if ts.Before(horizon) {
			delete(rc.nonces, nonce)
		}
	}
}

// Evict explicitly triggers eviction of expired nonces. Useful for testing.
func (rc *ReplayCache) Evict() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.evict(time.Now().UTC())
}
