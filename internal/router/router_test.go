package router

import (
	"errors"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// available constructs an entry that is currently available. secretRef is set
// for cloud entries (non-empty) and empty for local entries; budgetLimit drives
// IsUnlimited (0 = unlimited/local backstop).
func entry(id string, tier, cost, budgetLimit int, secretRef string) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:             id,
		Harness:        registry.HarnessClaudeCLI,
		CapabilityTier: tier,
		CostWeight:     cost,
		SecretRef:      secretRef,
		Budget:         registry.QuotaBudget{Limit: budgetLimit},
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
}

func catalogOf(entries ...registry.RegistryEntry) *registry.Catalog {
	c := registry.NewCatalog()
	for _, e := range entries {
		c.RegisterEntry(e)
	}
	return c
}

func mustSelect(t *testing.T, r *Router, spec RoutingSpec) registry.RegistryEntry {
	t.Helper()
	got, err := r.Select(spec)
	if err != nil {
		t.Fatalf("Select(%+v) returned unexpected error: %v", spec, err)
	}
	return got
}

// TC-092-01 — Router selects cheapest eligible entry.
func TestSelectCheapestEligible(t *testing.T) {
	r := New(catalogOf(
		entry("local", 1, 1, 0, ""), // cheapest
		entry("claude-oauth", 3, 10, 100, "claude-ref"),
		entry("codex", 2, 5, 100, "codex-ref"),
	))

	got := mustSelect(t, r, RoutingSpec{MinCapability: 1})

	if got.ID != "local" {
		t.Fatalf("expected cheapest eligible entry %q, got %q", "local", got.ID)
	}
}

// TC-092-02 — MinCapability filters ineligible entries; cheapest remaining wins.
func TestSelectMinCapabilityFilters(t *testing.T) {
	r := New(catalogOf(
		entry("local", 1, 1, 0, ""),
		entry("claude-oauth", 3, 10, 100, "claude-ref"),
		entry("codex", 2, 5, 100, "codex-ref"),
	))

	got := mustSelect(t, r, RoutingSpec{MinCapability: 2})

	if got.ID != "codex" {
		t.Fatalf("expected %q (tier>=2, cheapest eligible), got %q", "codex", got.ID)
	}
	if got.CapabilityTier < 2 {
		t.Fatalf("selected entry %q has tier %d < MinCapability 2", got.ID, got.CapabilityTier)
	}
}

// TC-092-03 — SensitivityHint biases toward local without excluding eligible
// non-local entries.
func TestSensitivityHintIsSoftWeight(t *testing.T) {
	// Part A: local is already cheapest — selected regardless of hint.
	cheapLocal := catalogOf(
		entry("local", 1, 1, 0, ""),
		entry("claude-oauth", 3, 10, 100, "claude-ref"),
	)
	rSensitive := New(cheapLocal)
	if got := mustSelect(t, rSensitive, RoutingSpec{MinCapability: 1, SensitivityHint: SensitivitySensitive}); got.ID != "local" {
		t.Fatalf("sensitive hint: expected %q, got %q", "local", got.ID)
	}
	rNone := New(catalogOf(
		entry("local", 1, 1, 0, ""),
		entry("claude-oauth", 3, 10, 100, "claude-ref"),
	))
	if got := mustSelect(t, rNone, RoutingSpec{MinCapability: 1, SensitivityHint: SensitivityNone}); got.ID != "local" {
		t.Fatalf("none hint: expected %q (cheapest regardless), got %q", "local", got.ID)
	}

	// Part B: tie on cost — the sensitive hint tips the tie to the local entry,
	// but the non-local entry remains eligible (a None hint picks it by stable ID).
	tied := func() *registry.Catalog {
		return catalogOf(
			// equal cost=5; "cloud" sorts before "local" by ID, so without the
			// hint the cloud entry wins the tie.
			entry("cloud", 2, 5, 100, "cloud-ref"),
			entry("local", 1, 5, 0, ""),
		)
	}
	rTieSensitive := New(tied())
	if got := mustSelect(t, rTieSensitive, RoutingSpec{MinCapability: 1, SensitivityHint: SensitivitySensitive}); got.ID != "local" {
		t.Fatalf("tie + sensitive hint: expected hint to pick %q, got %q", "local", got.ID)
	}
	rTieNone := New(tied())
	if got := mustSelect(t, rTieNone, RoutingSpec{MinCapability: 1, SensitivityHint: SensitivityNone}); got.ID != "cloud" {
		t.Fatalf("tie + none hint: expected %q (no bias, stable ID order), got %q", "cloud", got.ID)
	}
}

// TC-092-04 — Gate failure escalates UP the capability ladder (quality axis);
// exhausting all eligible entries returns ErrNoEligibleExecutor.
func TestGateFailureEscalatesQuality(t *testing.T) {
	r := New(catalogOf(
		entry("local", 1, 1, 0, ""),
		entry("claude-oauth", 3, 10, 100, "claude-ref"),
	))

	first := mustSelect(t, r, RoutingSpec{MinCapability: 1})
	if first.ID != "local" {
		t.Fatalf("first selection expected %q, got %q", "local", first.ID)
	}

	// Gate fails on "local" → escalate to next-stronger eligible.
	r.OnGateFailure("local")
	second := mustSelect(t, r, RoutingSpec{MinCapability: 1})
	if second.ID != "claude-oauth" {
		t.Fatalf("after gate failure on %q, expected escalation to %q, got %q", "local", "claude-oauth", second.ID)
	}

	// Gate fails on "claude-oauth" too → no eligible entry remains.
	r.OnGateFailure("claude-oauth")
	_, err := r.Select(RoutingSpec{MinCapability: 1})
	if !errors.Is(err, ErrNoEligibleExecutor) {
		t.Fatalf("after exhausting all entries, expected ErrNoEligibleExecutor, got %v", err)
	}

	// ResetEscalation restores the full eligible set for a fresh dispatch.
	r.ResetEscalation()
	again := mustSelect(t, r, RoutingSpec{MinCapability: 1})
	if again.ID != "local" {
		t.Fatalf("after ResetEscalation, expected %q again, got %q", "local", again.ID)
	}
}

// TC-092-05 — Quota exhaustion routes SIDEWAYS (availability axis); it does NOT
// climb the quality ladder.
func TestQuotaExhaustionRoutesSideways(t *testing.T) {
	r := New(catalogOf(
		entry("claude-oauth", 3, 10, 100, "claude-ref"), // only tier-3
		entry("codex", 2, 5, 100, "codex-ref"),
	))

	first := mustSelect(t, r, RoutingSpec{MinCapability: 3})
	if first.ID != "claude-oauth" {
		t.Fatalf("first selection at MinCapability=3 expected %q, got %q", "claude-oauth", first.ID)
	}

	resetAt := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	r.OnQuotaExhausted("claude-oauth", resetAt)

	// The entry must now be marked exhausted with the supplied ResetAt.
	got, ok := r.catalog.LookupEntry("claude-oauth")
	if !ok {
		t.Fatal("entry claude-oauth vanished from catalog after exhaustion")
	}
	if got.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("expected %q exhausted, got status %q", "claude-oauth", got.Availability.Status)
	}
	if !got.Availability.ResetAt.Equal(resetAt) {
		t.Fatalf("expected ResetAt %v, got %v", resetAt, got.Availability.ResetAt)
	}

	// Next selection at the LOWER MinCapability=2 routes sideways to codex (the
	// next cheapest still-available eligible entry). It does NOT climb to a
	// stronger entry — there is no stronger entry left, and the selection chose a
	// WEAKER tier-2 entry, proving the availability axis is independent of quality.
	second := mustSelect(t, r, RoutingSpec{MinCapability: 2})
	if second.ID != "codex" {
		t.Fatalf("after exhaustion, expected sideways route to %q, got %q", "codex", second.ID)
	}
	if second.CapabilityTier >= first.CapabilityTier {
		t.Fatalf("sideways route picked tier %d >= original tier %d — it climbed quality instead of routing sideways",
			second.CapabilityTier, first.CapabilityTier)
	}
}

// TC-092-06 — A Budget.Limit==0 entry is never marked exhausted (always-available
// local backstop).
func TestUnlimitedEntryNeverExhausted(t *testing.T) {
	r := New(catalogOf(entry("local", 1, 1, 0, "")))

	r.OnQuotaExhausted("local", time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC))

	got, ok := r.catalog.LookupEntry("local")
	if !ok {
		t.Fatal("local entry vanished from catalog")
	}
	if got.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("unlimited entry must stay available, got status %q", got.Availability.Status)
	}

	// A subsequent selection still returns the local entry.
	sel := mustSelect(t, r, RoutingSpec{MinCapability: 1})
	if sel.ID != "local" {
		t.Fatalf("expected %q still selectable after ignored exhaustion, got %q", "local", sel.ID)
	}
}

// OnQuotaExhausted on an unknown entry ID is a silent no-op.
func TestQuotaExhaustionUnknownEntryNoop(t *testing.T) {
	r := New(catalogOf(entry("codex", 2, 5, 100, "codex-ref")))

	r.OnQuotaExhausted("does-not-exist", time.Now())

	got, ok := r.catalog.LookupEntry("codex")
	if !ok || got.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("unknown-id exhaustion must not affect existing entries; codex status=%q ok=%v", got.Availability.Status, ok)
	}
}

// Exhausted entries are filtered out of selection (availability is a hard filter).
func TestExhaustedEntryFilteredFromSelection(t *testing.T) {
	r := New(catalogOf(
		entry("codex", 2, 5, 100, "codex-ref"),
		entry("gemini", 2, 7, 100, "gemini-ref"),
	))

	r.OnQuotaExhausted("codex", time.Now().Add(time.Hour))

	got := mustSelect(t, r, RoutingSpec{MinCapability: 2})
	if got.ID != "gemini" {
		t.Fatalf("exhausted %q must be filtered; expected %q, got %q", "codex", "gemini", got.ID)
	}
}

// Empty eligible set returns ErrNoEligibleExecutor.
func TestNoEligibleWhenCapabilityTooHigh(t *testing.T) {
	r := New(catalogOf(entry("local", 1, 1, 0, "")))

	_, err := r.Select(RoutingSpec{MinCapability: 5})
	if !errors.Is(err, ErrNoEligibleExecutor) {
		t.Fatalf("expected ErrNoEligibleExecutor when no entry meets capability, got %v", err)
	}
}
