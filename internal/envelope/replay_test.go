package envelope

import (
	"strings"
	"testing"
	"time"
)

// TestReplayCacheFreshNonce tests TC-096-06: Fresh nonce within window is accepted
func TestReplayCacheFreshNonce(t *testing.T) {
	cache := NewReplayCache(60 * time.Second)

	// TC-096-06 (fresh): Nonce within window should be accepted
	nonce := "abc123unique"
	ts := time.Now().UTC()
	err := cache.Check(nonce, ts)
	if err != nil {
		t.Fatalf("Check on fresh nonce failed: %v", err)
	}
}

// TestReplayCacheStaleTimestamp tests TC-096-06: Stale and future timestamps are rejected
func TestReplayCacheStaleTimestamp(t *testing.T) {
	cache := NewReplayCache(60 * time.Second)

	// TC-096-06 (stale timestamp): Timestamp older than Window should be rejected
	staleTS := time.Now().UTC().Add(-61 * time.Second)
	err := cache.Check("freshNonce", staleTS)
	if err == nil {
		t.Fatal("Check on stale timestamp should have failed but returned nil")
	}

	// TC-096-06: Error string must contain expected substring
	errStr := err.Error()
	if !strings.Contains(errStr, "stale") &&
		!strings.Contains(errStr, "timestamp") &&
		!strings.Contains(errStr, "window") {
		t.Errorf("error message missing expected substring: %q", errStr)
	}
}

// TestReplayCacheFutureTimestamp tests TC-096-06: Future timestamp outside window is rejected
func TestReplayCacheFutureTimestamp(t *testing.T) {
	cache := NewReplayCache(60 * time.Second)

	// TC-096-06 (future timestamp): Timestamp newer than Window should be rejected
	futureTS := time.Now().UTC().Add(61 * time.Second)
	err := cache.Check("futureNonce", futureTS)
	if err == nil {
		t.Fatal("Check on future timestamp should have failed but returned nil")
	}

	// TC-096-06: Error string must contain expected substring
	errStr := err.Error()
	if !strings.Contains(errStr, "stale") &&
		!strings.Contains(errStr, "timestamp") &&
		!strings.Contains(errStr, "window") {
		t.Errorf("error message missing expected substring: %q", errStr)
	}
}

// TestReplayCacheReplayDetection tests TC-096-07: Replayed nonce is rejected
func TestReplayCacheReplayDetection(t *testing.T) {
	cache := NewReplayCache(60 * time.Second)

	nonce := "nonce-XYZ"
	ts := time.Now().UTC()

	// TC-096-07 (step 1): First call should succeed
	err := cache.Check(nonce, ts)
	if err != nil {
		t.Fatalf("First Check on nonce should succeed, but got: %v", err)
	}

	// TC-096-07 (step 2): Second call with same nonce should fail (replay)
	err = cache.Check(nonce, ts)
	if err == nil {
		t.Fatal("Replay Check should have failed but returned nil")
	}

	// TC-096-07: Error string must contain expected substring
	errStr := err.Error()
	if !strings.Contains(errStr, "replay") &&
		!strings.Contains(errStr, "seen") &&
		!strings.Contains(errStr, "nonce") {
		t.Errorf("error message missing expected substring: %q", errStr)
	}
}

// TestReplayCacheEviction tests TC-096-08: Nonce set is bounded by 2×Window; eviction works
func TestReplayCacheEviction(t *testing.T) {
	// Use a short window for testing (100ms).
	window := 100 * time.Millisecond
	cache := NewReplayCache(window)

	baseTime := time.Now().UTC()

	// Step 1: Accept "nonce-A" at baseTime.
	nonce := "nonce-A"
	err := cache.Check(nonce, baseTime)
	if err != nil {
		t.Fatalf("Check on fresh nonce failed: %v", err)
	}

	// Step 2: Replay "nonce-A" immediately — should fail.
	err = cache.Check(nonce, baseTime)
	if err == nil {
		t.Fatal("Replay Check should have failed")
	}

	// Record the size after first two checks.
	sizeAfterFirstNonce := cache.Len()
	if sizeAfterFirstNonce != 1 {
		t.Errorf("cache size after first nonce: expected 1, got %d", sizeAfterFirstNonce)
	}

	// Step 3: Sleep 2×Window + 10ms to push "nonce-A" beyond eviction horizon.
	time.Sleep(2*window + 10*time.Millisecond)

	// Step 4: Insert 1000 fresh nonces (to grow the cache).
	for i := 0; i < 1000; i++ {
		nonceStr := "nonce-fresh-" + string(rune(i))
		err := cache.Check(nonceStr, time.Now().UTC())
		if err != nil {
			t.Fatalf("Check on fresh nonce %d failed: %v", i, err)
		}
	}

	sizeAfter1000Fresh := cache.Len()
	if sizeAfter1000Fresh > 1000 {
		t.Errorf("cache size after 1000 fresh nonces: expected ≤ 1000, got %d", sizeAfter1000Fresh)
	}

	// Step 5: Attempt replay of the 1000 nonces. They should fail (still in window).
	for i := 0; i < 1000; i++ {
		nonceStr := "nonce-fresh-" + string(rune(i))
		err := cache.Check(nonceStr, time.Now().UTC())
		if err == nil {
			t.Errorf("Replay of nonce %d should have failed", i)
			break
		}
	}

	// Step 6: Evict explicitly, then sleep 2×Window + 10ms, and insert 1000 new nonces.
	cache.Evict()
	time.Sleep(2*window + 10*time.Millisecond)

	for i := 1000; i < 2000; i++ {
		nonceStr := "nonce-fresh-" + string(rune(i))
		err := cache.Check(nonceStr, time.Now().UTC())
		if err != nil {
			t.Fatalf("Check on fresh nonce %d failed: %v", i, err)
		}
	}

	sizeAfter2000Nonces := cache.Len()
	if sizeAfter2000Nonces > 1000 {
		t.Errorf("cache size after eviction and 1000 new nonces: expected ≤ 1000, got %d", sizeAfter2000Nonces)
	}

	// Verify that nonce-A can now be reused (it was evicted).
	// Create a new timestamp that is fresh from now.
	freshTS := time.Now().UTC()
	err = cache.Check("nonce-A", freshTS)
	if err != nil {
		t.Fatalf("nonce-A should be reusable after eviction, but got: %v", err)
	}
}

// TestReplayCacheBoundedSize tests TC-096-08 (simplified): Nonce set does not grow unbounded
func TestReplayCacheBoundedSize(t *testing.T) {
	window := 50 * time.Millisecond
	cache := NewReplayCache(window)

	// Insert 100 nonces.
	for i := 0; i < 100; i++ {
		nonce := "nonce-" + string(rune(i))
		ts := time.Now().UTC()
		err := cache.Check(nonce, ts)
		if err != nil {
			t.Fatalf("Check on fresh nonce %d failed: %v", i, err)
		}
	}

	size1 := cache.Len()

	// Sleep 2×Window + margin.
	time.Sleep(2*window + 20*time.Millisecond)

	// Insert 100 new nonces.
	for i := 100; i < 200; i++ {
		nonce := "nonce-" + string(rune(i))
		ts := time.Now().UTC()
		err := cache.Check(nonce, ts)
		if err != nil {
			t.Fatalf("Check on fresh nonce %d failed: %v", i, err)
		}
	}

	size2 := cache.Len()

	// The size should remain bounded (roughly ≤ 100 + overhead of recent eviction timing).
	if size2 > 150 {
		t.Errorf("cache size after second batch: expected ≤ 150, got %d (suggests unbounded growth)", size2)
	}

	t.Logf("cache bounded: size1=%d, size2=%d", size1, size2)
}
