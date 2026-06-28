package router

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// baseTime is a fixed, deterministic anchor for all quota tests. Using a fixed
// time makes failure messages deterministic and avoids relying on the wall clock.
var baseTime = time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)

// cloudEntry builds a non-local (cloud) entry with the given budget limit and
// window. SecretRef is non-empty so IsUnlimited() returns false for limit>0.
func cloudEntryBudget(id string, tier, cost, limit int, window time.Duration) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:             id,
		Harness:        registry.HarnessClaudeCLI,
		CapabilityTier: tier,
		CostWeight:     cost,
		SecretRef:      "some-secret-ref",
		Budget:         registry.QuotaBudget{Limit: limit, Window: window},
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
}

// localEntry builds an unlimited (local) entry — Budget.Limit == 0, SecretRef == "".
func localEntryQ(id string, tier, cost int) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:             id,
		Harness:        registry.HarnessClaudeCLI,
		CapabilityTier: tier,
		CostWeight:     cost,
		SecretRef:      "",
		Budget:         registry.QuotaBudget{}, // zero = unlimited
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
}

func catalogWithEntries(entries ...registry.RegistryEntry) *registry.Catalog {
	c := registry.NewCatalog()
	for _, e := range entries {
		c.RegisterEntry(e)
	}
	return c
}

// TC-093-01 — Usage tally increments on each dispatch; proactive budget check.
//
// Setup: cloud entry with Budget.Limit=3, Window=5h. Call RecordDispatch 3 times.
// After 3 dispatches: Usage==3, entry.Status==Exhausted, Select returns
// ErrNoEligibleExecutor.
func TestRecordDispatchIncrementsUsageAndExhaustsAtLimit(t *testing.T) { // TC-093-01
	fc := NewFakeClock(baseTime)
	cat := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 3, 5*time.Hour))
	r := NewWithClock(cat, fc, DefaultCooldown)

	// First dispatch: Usage=1, still available.
	r.RecordDispatch("claude-oauth")
	e, ok := cat.LookupEntry("claude-oauth")
	if !ok {
		t.Fatal("entry vanished after first RecordDispatch")
	}
	if e.Usage != 1 {
		t.Fatalf("after 1st dispatch: want Usage=1, got %d", e.Usage)
	}
	if e.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("after 1st dispatch: want status=available, got %q", e.Availability.Status)
	}

	// Second dispatch: Usage=2, still available.
	r.RecordDispatch("claude-oauth")
	e, _ = cat.LookupEntry("claude-oauth")
	if e.Usage != 2 {
		t.Fatalf("after 2nd dispatch: want Usage=2, got %d", e.Usage)
	}
	if e.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("after 2nd dispatch: want status=available, got %q", e.Availability.Status)
	}

	// Third dispatch: Usage=3 (== Limit) → proactively exhausted.
	r.RecordDispatch("claude-oauth")
	e, _ = cat.LookupEntry("claude-oauth")
	if e.Usage != 3 {
		t.Fatalf("after 3rd dispatch: want Usage=3, got %d", e.Usage)
	}
	if e.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("after 3rd dispatch (usage==limit): want status=exhausted, got %q", e.Availability.Status)
	}

	// ResetAt must be now + Budget.Window.
	wantResetAt := baseTime.Add(5 * time.Hour)
	if !e.Availability.ResetAt.Equal(wantResetAt) {
		t.Fatalf("want ResetAt=%v, got %v", wantResetAt, e.Availability.ResetAt)
	}

	// Select must now return ErrNoEligibleExecutor (proactive exclusion).
	_, err := r.Select(RoutingSpec{MinCapability: 1})
	if !errors.Is(err, ErrNoEligibleExecutor) {
		t.Fatalf("after exhaustion: want ErrNoEligibleExecutor, got %v", err)
	}
}

// TC-093-02 — Rolling window reset: advance clock past ResetAt → auto-recovery.
//
// After the entry is exhausted (from TC-093-01 setup), advance the fake clock
// past ResetAt. The next Select must re-include the entry (no manual reset) and
// the entry's Usage must be 0.
func TestAutoRecoverWhenClockPastResetAt(t *testing.T) { // TC-093-02
	fc := NewFakeClock(baseTime)
	cat := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 3, 5*time.Hour))
	r := NewWithClock(cat, fc, DefaultCooldown)

	// Exhaust the entry.
	r.RecordDispatch("claude-oauth")
	r.RecordDispatch("claude-oauth")
	r.RecordDispatch("claude-oauth")

	// Confirm exhausted before advancing.
	e, _ := cat.LookupEntry("claude-oauth")
	if e.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("pre-advance: want exhausted, got %q", e.Availability.Status)
	}

	// Advance just BEFORE ResetAt — entry must still be excluded.
	// ResetAt = baseTime + 5h; advance to baseTime + 4h59m.
	fc.Advance(4*time.Hour + 59*time.Minute)
	_, err := r.Select(RoutingSpec{MinCapability: 1})
	if !errors.Is(err, ErrNoEligibleExecutor) {
		t.Fatalf("just before ResetAt: want ErrNoEligibleExecutor, got %v", err)
	}

	// Advance past ResetAt — now at baseTime + 5h01m.
	fc.Advance(2 * time.Minute) // total: +5h01m past baseTime

	// Select must now return the entry (auto-recovered).
	got, err := r.Select(RoutingSpec{MinCapability: 1})
	if err != nil {
		t.Fatalf("after ResetAt passed: want entry returned, got error %v", err)
	}
	if got.ID != "claude-oauth" {
		t.Fatalf("after recovery: want claude-oauth, got %q", got.ID)
	}

	// Entry must have been recovered: Usage==0, Status==available.
	e, _ = cat.LookupEntry("claude-oauth")
	if e.Usage != 0 {
		t.Fatalf("after recovery: want Usage=0, got %d", e.Usage)
	}
	if e.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("after recovery: want status=available, got %q", e.Availability.Status)
	}
}

// TC-093-03 — Reactive exhaustion: OnRateLimit parses Retry-After header.
//
// Part A: header "60" → ResetAt = now + 60s.
// Part B: empty header → ResetAt = now + configuredCooldown.
func TestOnRateLimitParsesRetryAfterHeader(t *testing.T) { // TC-093-03
	configuredCooldown := 5 * time.Minute

	t.Run("header_present", func(t *testing.T) {
		fc := NewFakeClock(baseTime)
		cat := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 100, time.Hour))
		r := NewWithClock(cat, fc, configuredCooldown)

		r.OnRateLimit("claude-oauth", "60")

		e, ok := cat.LookupEntry("claude-oauth")
		if !ok {
			t.Fatal("entry vanished after OnRateLimit")
		}
		if e.Availability.Status != registry.AvailStatusExhausted {
			t.Fatalf("want exhausted, got %q", e.Availability.Status)
		}
		wantResetAt := baseTime.Add(60 * time.Second)
		if !e.Availability.ResetAt.Equal(wantResetAt) {
			t.Fatalf("want ResetAt=%v (now+60s), got %v", wantResetAt, e.Availability.ResetAt)
		}
	})

	t.Run("header_missing", func(t *testing.T) {
		fc := NewFakeClock(baseTime)
		cat := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 100, time.Hour))
		r := NewWithClock(cat, fc, configuredCooldown)

		r.OnRateLimit("claude-oauth", "")

		e, ok := cat.LookupEntry("claude-oauth")
		if !ok {
			t.Fatal("entry vanished after OnRateLimit (no header)")
		}
		if e.Availability.Status != registry.AvailStatusExhausted {
			t.Fatalf("want exhausted, got %q", e.Availability.Status)
		}
		wantResetAt := baseTime.Add(configuredCooldown)
		if !e.Availability.ResetAt.Equal(wantResetAt) {
			t.Fatalf("want ResetAt=%v (now+cooldown), got %v", wantResetAt, e.Availability.ResetAt)
		}
	})
}

// TC-093-04 — Clock seam: injected FakeClock controls now(); no time.Sleep.
//
// Mark entry exhausted with ResetAt = T + 10s. At time T, Select excludes it.
// Advance clock to T + 11s. Select returns it.
func TestFakeClockSeamControlsRecovery(t *testing.T) { // TC-093-04
	T := baseTime
	fc := NewFakeClock(T)
	cat := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 100, time.Hour))
	r := NewWithClock(cat, fc, DefaultCooldown)

	// Mark exhausted with ResetAt = T + 10s using OnRateLimit (header="10").
	r.OnRateLimit("claude-oauth", "10")

	e, _ := cat.LookupEntry("claude-oauth")
	wantResetAt := T.Add(10 * time.Second)
	if !e.Availability.ResetAt.Equal(wantResetAt) {
		t.Fatalf("setup: want ResetAt=%v, got %v", wantResetAt, e.Availability.ResetAt)
	}

	// At time T: Select must exclude the entry.
	_, err := r.Select(RoutingSpec{MinCapability: 1})
	if !errors.Is(err, ErrNoEligibleExecutor) {
		t.Fatalf("at T: want ErrNoEligibleExecutor, got %v", err)
	}

	// Advance clock to T + 11s (past ResetAt).
	fc.Advance(11 * time.Second)

	// Select must now include the entry (auto-recovered).
	got, err := r.Select(RoutingSpec{MinCapability: 1})
	if err != nil {
		t.Fatalf("at T+11s: want entry returned, got error %v", err)
	}
	if got.ID != "claude-oauth" {
		t.Fatalf("at T+11s: want claude-oauth, got %q", got.ID)
	}
	// No time.Sleep was called anywhere in this test (the clock is purely fake).
}

// TC-093-05 — File persistence: quota state survives a save + load cycle.
//
// Exhaust an entry, SaveState, construct a fresh router, LoadState, verify:
//   - Usage and Status are preserved.
//   - Select excludes the exhausted entry.
//   - A corrupted file returns a descriptive error (not a silent zero value).
func TestSaveStateAndLoadState(t *testing.T) { // TC-093-05
	T := baseTime
	fc := NewFakeClock(T)

	// -- Save phase --
	cat1 := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 3, time.Hour))
	r1 := NewWithClock(cat1, fc, DefaultCooldown)

	// Exhaust the entry via 3 dispatches → Usage=3, Status=Exhausted, ResetAt=T+1h.
	r1.RecordDispatch("claude-oauth")
	r1.RecordDispatch("claude-oauth")
	r1.RecordDispatch("claude-oauth")

	e1, _ := cat1.LookupEntry("claude-oauth")
	if e1.Usage != 3 || e1.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("setup: want Usage=3 + exhausted, got Usage=%d status=%q", e1.Usage, e1.Availability.Status)
	}

	tmpFile := filepath.Join(t.TempDir(), "quota-state.json")
	if err := r1.SaveState(tmpFile); err != nil {
		t.Fatalf("SaveState: unexpected error: %v", err)
	}

	// Verify it is valid JSON (plain-text contract).
	raw, _ := os.ReadFile(tmpFile)
	if !json.Valid(raw) {
		t.Fatalf("SaveState produced non-JSON output: %s", raw)
	}

	// -- Load phase: fresh router + catalog --
	cat2 := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 3, time.Hour))
	r2 := NewWithClock(cat2, fc, DefaultCooldown)

	if err := r2.LoadState(tmpFile); err != nil {
		t.Fatalf("LoadState: unexpected error: %v", err)
	}

	// Restored entry must have Usage=3 and Status=Exhausted.
	e2, ok := cat2.LookupEntry("claude-oauth")
	if !ok {
		t.Fatal("entry not found in cat2 after LoadState")
	}
	if e2.Usage != 3 {
		t.Fatalf("after LoadState: want Usage=3, got %d", e2.Usage)
	}
	if e2.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("after LoadState: want exhausted, got %q", e2.Availability.Status)
	}

	// Select must still exclude the entry (still before ResetAt).
	_, err := r2.Select(RoutingSpec{MinCapability: 1})
	if !errors.Is(err, ErrNoEligibleExecutor) {
		t.Fatalf("after LoadState (before ResetAt): want ErrNoEligibleExecutor, got %v", err)
	}

	// -- Corrupted file path --
	corruptFile := filepath.Join(t.TempDir(), "corrupt.json")
	if err := os.WriteFile(corruptFile, []byte("{this is not valid json!}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cat3 := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 3, time.Hour))
	r3 := NewWithClock(cat3, fc, DefaultCooldown)
	loadErr := r3.LoadState(corruptFile)
	if loadErr == nil {
		t.Fatal("corrupted file: expected non-nil error, got nil")
	}
	// The error must be descriptive (contain meaningful context), not a silent zero.
	if loadErr.Error() == "" {
		t.Fatal("corrupted file: error message is empty — expected descriptive error")
	}
	// Verify it mentions "corrupted" or "parse" so it is diagnostic.
	msg := loadErr.Error()
	if !containsAny(msg, "corrupted", "parse", "corrupt") {
		t.Fatalf("corrupted file error should be descriptive; got: %q", msg)
	}
}

// TC-093-06 — Availability-axis fallback: exhausted cloud → local entry selected.
//
// Registry: "claude-oauth" (tier=3, cost=10, exhausted) + "local-qwen" (tier=1,
// cost=1, available, unlimited). Dispatch at MinCapability=1 must return
// "local-qwen".
func TestExhaustedCloudFallsToLocalEntry(t *testing.T) { // TC-093-06
	fc := NewFakeClock(baseTime)
	cat := catalogWithEntries(
		cloudEntryBudget("claude-oauth", 3, 10, 1, time.Hour),
		localEntryQ("local-qwen", 1, 1),
	)
	r := NewWithClock(cat, fc, DefaultCooldown)

	// Exhaust the cloud entry via RecordDispatch (Usage=1 == Limit=1).
	r.RecordDispatch("claude-oauth")

	e, _ := cat.LookupEntry("claude-oauth")
	if e.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("setup: want claude-oauth exhausted, got %q", e.Availability.Status)
	}

	// Verify local-qwen is still available and unlimited.
	local, _ := cat.LookupEntry("local-qwen")
	if local.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("setup: local-qwen must be available, got %q", local.Availability.Status)
	}
	if !local.IsUnlimited() {
		t.Fatal("setup: local-qwen must be unlimited (Budget.Limit==0)")
	}

	// Select must route to local-qwen — the always-available fallback.
	got, err := r.Select(RoutingSpec{MinCapability: 1})
	if err != nil {
		t.Fatalf("want local-qwen selected, got error %v", err)
	}
	if got.ID != "local-qwen" {
		t.Fatalf("want local-qwen (always-available fallback), got %q", got.ID)
	}
}

// Local entry: RecordDispatch is a no-op (never increments or exhausts).
func TestRecordDispatchNoOpOnLocalEntry(t *testing.T) {
	fc := NewFakeClock(baseTime)
	cat := catalogWithEntries(localEntryQ("local-qwen", 1, 1))
	r := NewWithClock(cat, fc, DefaultCooldown)

	r.RecordDispatch("local-qwen")
	r.RecordDispatch("local-qwen")

	e, _ := cat.LookupEntry("local-qwen")
	if e.Usage != 0 {
		t.Fatalf("local entry: want Usage=0 (never incremented), got %d", e.Usage)
	}
	if e.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("local entry: want available, got %q", e.Availability.Status)
	}
}

// Local entry: OnRateLimit is a no-op (never marks exhausted).
func TestOnRateLimitNoOpOnLocalEntry(t *testing.T) {
	fc := NewFakeClock(baseTime)
	cat := catalogWithEntries(localEntryQ("local-qwen", 1, 1))
	r := NewWithClock(cat, fc, DefaultCooldown)

	r.OnRateLimit("local-qwen", "60")

	e, _ := cat.LookupEntry("local-qwen")
	if e.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("local entry: want available after OnRateLimit, got %q", e.Availability.Status)
	}
}

// SaveState + LoadState with an entry absent from the catalog (state file has
// an ID that no longer exists in the catalog). Should be silently skipped.
func TestLoadStateSkipsUnknownEntries(t *testing.T) {
	fc := NewFakeClock(baseTime)
	cat1 := catalogWithEntries(cloudEntryBudget("claude-oauth", 3, 10, 3, time.Hour))
	r1 := NewWithClock(cat1, fc, DefaultCooldown)
	r1.RecordDispatch("claude-oauth")

	tmpFile := filepath.Join(t.TempDir(), "state.json")
	if err := r1.SaveState(tmpFile); err != nil {
		t.Fatal(err)
	}

	// Fresh catalog without "claude-oauth" — load must not error.
	cat2 := catalogWithEntries(cloudEntryBudget("codex", 2, 5, 10, time.Hour))
	r2 := NewWithClock(cat2, fc, DefaultCooldown)
	if err := r2.LoadState(tmpFile); err != nil {
		t.Fatalf("LoadState with unknown ID: unexpected error %v", err)
	}
	// codex entry must be unaffected.
	e, _ := cat2.LookupEntry("codex")
	if e.Usage != 0 {
		t.Fatalf("codex must be unaffected by unknown-ID state, got Usage=%d", e.Usage)
	}
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 {
			idx := 0
			for idx <= len(s)-len(sub) {
				if s[idx:idx+len(sub)] == sub {
					return true
				}
				idx++
			}
		}
	}
	return false
}
