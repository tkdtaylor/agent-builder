// Package router implements ADR 043's capability/cost-first model router. It
// selects a registry entry per dispatch (cheapest eligible at sufficient
// capability), applies the soft sensitivity hint as a tie-breaking weight, and
// drives the two distinct fallback axes:
//
//   - Gate failure → escalate UP the capability ladder (quality axis): the next
//     Select returns the next-stronger eligible entry.
//   - Quota exhaustion → fall SIDEWAYS to the next currently-available eligible
//     entry (availability axis): it does NOT climb the quality ladder.
//
// The router lives on the executor side of the supervisor injection boundary
// (it is a sibling of internal/executor and imports it). internal/supervisor
// never imports the router or the registry — F-003 is preserved and enforced by
// `make fitness-supervisor-isolation`.
//
// Task 093 adds:
//   - RecordDispatch: increments Usage; marks exhausted at Budget.Limit and sets
//     ResetAt = now + Budget.Window. Select auto-recovers when now > ResetAt.
//   - OnRateLimit: reactive 429 handler; derives ResetAt from Retry-After header
//     or configured cooldown.
//   - Clock seam: injected Clock interface so tests advance time without sleeping.
//   - SaveState / LoadState: plain-text (JSON) quota state persistence.
package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// ErrNoEligibleExecutor is returned by Select when no registry entry satisfies
// the dispatch's capability requirement, availability filter, and the set of
// entries already escalated past via OnGateFailure.
var ErrNoEligibleExecutor = errors.New("router: no eligible executor")

// DefaultCooldown is the fallback window used by OnRateLimit when no
// Retry-After header is present. Five minutes is a conservative default that
// avoids hammering a provider whose rate-limit reset time is unknown.
const DefaultCooldown = 5 * time.Minute

// Sensitivity is the soft routing hint. It is a tie-breaking weight among
// equal-cost eligible entries — never a hard filter (ADR 043). It mirrors
// recipe.Sensitivity at the router boundary so the router does not import the
// leaf recipe package.
type Sensitivity int

const (
	// SensitivityNone applies no tie-break preference.
	SensitivityNone Sensitivity = iota
	// SensitivitySensitive biases ties toward a local entry (no cloud egress).
	SensitivitySensitive
)

// RoutingSpec is the per-dispatch routing request: the minimum capability tier
// the work needs and the soft sensitivity hint. It mirrors recipe.RoutingSpec
// at the router boundary (the leaf recipe declares the spec; the assembler hands
// the equivalent values to the router) so the router stays decoupled from the
// recipe leaf.
type RoutingSpec struct {
	MinCapability   int
	SensitivityHint Sensitivity
}

// Router selects registry entries per dispatch and drives escalation. It owns
// the mutable availability state on the catalog's entries (in memory and,
// optionally, on disk via SaveState/LoadState).
//
// escalated holds the IDs the current dispatch has already tried and gate-failed.
// Select skips them so the next call climbs to the next-stronger eligible entry.
// ResetEscalation clears it at the start of a fresh dispatch.
//
// clock is the injected time source. Production code uses a real clock
// (time.Now()); tests inject a FakeClock that advances programmatically — no
// time.Sleep needed in any test.
//
// cooldown is the fallback window used by OnRateLimit when no Retry-After
// header is present.
type Router struct {
	catalog   *registry.Catalog
	escalated map[string]bool
	clock     Clock
	cooldown  time.Duration
}

// New constructs a Router over the given catalog with a real wall clock and
// DefaultCooldown. This is the production constructor; existing call sites do
// not need to change.
func New(catalog *registry.Catalog) *Router {
	return NewWithClock(catalog, realClock{}, DefaultCooldown)
}

// NewWithClock constructs a Router over the given catalog with an explicit
// clock and cooldown. This is the test-friendly constructor — pass a FakeClock
// and a small cooldown so tests do not sleep.
func NewWithClock(catalog *registry.Catalog, clock Clock, cooldown time.Duration) *Router {
	return &Router{
		catalog:   catalog,
		escalated: make(map[string]bool),
		clock:     clock,
		cooldown:  cooldown,
	}
}

// tryAutoRecover checks whether an exhausted entry's ResetAt has passed and,
// if so, flips it back to available and resets its Usage tally. Called at the
// start of Select for every entry that is currently exhausted, so recovery is
// automatic (no manual intervention) once the clock passes ResetAt.
func (r *Router) tryAutoRecover(e registry.RegistryEntry) registry.RegistryEntry {
	if e.Availability.Status != registry.AvailStatusExhausted {
		return e
	}
	if r.clock.Now().After(e.Availability.ResetAt) {
		e.Usage = 0
		e.Availability = registry.Availability{Status: registry.AvailStatusAvailable}
		r.catalog.UpdateEntry(e)
	}
	return e
}

// Select returns the cheapest eligible entry for the dispatch.
//
// An entry is eligible when it (a) meets the capability floor
// (CapabilityTier >= spec.MinCapability), (b) is currently available
// (Availability.Status == AvailStatusAvailable) — with automatic recovery when
// now > ResetAt — and (c) has not already been escalated past via OnGateFailure
// in this dispatch.
//
// Among eligible entries the cheapest CostWeight wins. Ties break by the soft
// sensitivity hint (a sensitive hint prefers a local entry — one with no
// SecretRef and zero Budget), then by stable entry ID for determinism. The hint
// never excludes an otherwise-eligible entry.
//
// Returns ErrNoEligibleExecutor when no entry qualifies.
func (r *Router) Select(spec RoutingSpec) (registry.RegistryEntry, error) {
	eligible := make([]registry.RegistryEntry, 0)
	for _, e := range r.catalog.ListEntries() {
		// Auto-recover exhausted entries whose ResetAt has passed.
		e = r.tryAutoRecover(e)

		if r.escalated[e.ID] {
			continue
		}
		if e.CapabilityTier < spec.MinCapability {
			continue
		}
		if e.Availability.Status != registry.AvailStatusAvailable {
			continue
		}
		eligible = append(eligible, e)
	}
	if len(eligible) == 0 {
		return registry.RegistryEntry{}, ErrNoEligibleExecutor
	}

	sort.SliceStable(eligible, func(i, j int) bool {
		return less(eligible[i], eligible[j], spec.SensitivityHint)
	})
	return eligible[0], nil
}

// less reports whether entry a should sort before entry b for selection.
// Primary key: cheapest CostWeight. Tie-break 1 (soft sensitivity weight): when
// the hint is sensitive, a local entry sorts before a non-local one. Tie-break 2:
// stable entry ID for deterministic ordering. The sensitivity weight applies
// ONLY when cost is tied — it never reorders across different costs and never
// excludes an entry.
func less(a, b registry.RegistryEntry, hint Sensitivity) bool {
	if a.CostWeight != b.CostWeight {
		return a.CostWeight < b.CostWeight
	}
	if hint == SensitivitySensitive {
		aLocal := isLocal(a)
		bLocal := isLocal(b)
		if aLocal != bLocal {
			return aLocal // local sorts first under a sensitive hint
		}
	}
	return a.ID < b.ID
}

// isLocal reports whether an entry is a local, no-cloud-egress entry: it has no
// vault secret reference and an unlimited (zero) budget. These are the entries a
// sensitive hint biases toward (ADR 043: local = privacy/quota backstop).
func isLocal(e registry.RegistryEntry) bool {
	return e.SecretRef == "" && e.IsUnlimited()
}

// OnGateFailure records that the entry produced a gate-failing branch on this
// dispatch. It is the QUALITY axis: the next Select skips this entry and returns
// the next-stronger eligible entry (selection naturally climbs because the
// failed entry — typically the cheapest — is removed from the eligible set).
// Once every eligible entry has gate-failed, Select returns ErrNoEligibleExecutor.
//
// This does NOT touch the entry's availability — a gate failure is a quality
// signal, not an exhaustion signal. The escalation set is cleared by
// ResetEscalation at the start of a fresh dispatch.
func (r *Router) OnGateFailure(entryID string) {
	r.escalated[entryID] = true
}

// ResetEscalation clears the per-dispatch gate-failure set so a new dispatch
// starts from the full eligible set again. Availability state (exhaustion) is
// NOT cleared — that is a separate axis with its own lifecycle (auto-recovery
// in Select when the clock passes ResetAt).
func (r *Router) ResetEscalation() {
	r.escalated = make(map[string]bool)
}

// OnQuotaExhausted marks the entry exhausted until resetAt. It is the
// AVAILABILITY axis: the entry is filtered out of selection, so the next Select
// routes SIDEWAYS to the next cheapest still-available eligible entry at
// sufficient capability — it does NOT climb the quality ladder.
//
// An unlimited entry (Budget.Limit == 0 — every local entry) is NEVER marked
// exhausted: it is the always-available quota-free backstop (ADR 043). The call
// is silently ignored for such an entry, and for an unknown entry ID.
func (r *Router) OnQuotaExhausted(entryID string, resetAt time.Time) {
	entry, ok := r.catalog.LookupEntry(entryID)
	if !ok {
		return
	}
	if entry.IsUnlimited() {
		// Local / unlimited entry: never exhausted.
		return
	}
	entry.Availability = registry.Availability{
		Status:  registry.AvailStatusExhausted,
		ResetAt: resetAt,
	}
	r.catalog.UpdateEntry(entry)
}

// RecordDispatch increments the Usage tally for the named entry. When Usage
// reaches or exceeds Budget.Limit, the entry is proactively marked exhausted
// with ResetAt = now + Budget.Window, so subsequent Select calls skip it.
//
// A local entry (Budget.Limit == 0) is never marked exhausted and its Usage is
// never incremented — it is the always-available quota-free backstop (REQ-093-05).
//
// An unknown entry ID is a silent no-op.
func (r *Router) RecordDispatch(entryID string) {
	entry, ok := r.catalog.LookupEntry(entryID)
	if !ok {
		return
	}
	if entry.IsUnlimited() {
		// Local entry: never increment or exhaust.
		return
	}
	entry.Usage++
	if entry.Usage >= entry.Budget.Limit {
		entry.Availability = registry.Availability{
			Status:  registry.AvailStatusExhausted,
			ResetAt: r.clock.Now().Add(entry.Budget.Window),
		}
	}
	r.catalog.UpdateEntry(entry)
}

// OnRateLimit marks the named entry exhausted in response to a provider 429 /
// rate-limit signal (the REACTIVE path). ResetAt is derived from the
// retryAfterHeader when it is present and parseable, else from the configured
// cooldown (DefaultCooldown or the value passed to NewWithClock).
//
// The header value is parsed as a plain integer number of seconds (the most
// common form: "Retry-After: 60"). HTTP-date form is not supported; if parsing
// fails the cooldown is used.
//
// A local entry (Budget.Limit == 0) is never marked exhausted (REQ-093-05).
// An unknown entry ID is a silent no-op.
func (r *Router) OnRateLimit(entryID string, retryAfterHeader string) {
	entry, ok := r.catalog.LookupEntry(entryID)
	if !ok {
		return
	}
	if entry.IsUnlimited() {
		// Local entry: never exhausted.
		return
	}

	resetAt := r.clock.Now().Add(r.cooldown)
	if retryAfterHeader != "" {
		secs, err := strconv.ParseFloat(strings.TrimSpace(retryAfterHeader), 64)
		if err == nil && secs >= 0 {
			resetAt = r.clock.Now().Add(time.Duration(secs * float64(time.Second)))
		}
	}

	entry.Availability = registry.Availability{
		Status:  registry.AvailStatusExhausted,
		ResetAt: resetAt,
	}
	r.catalog.UpdateEntry(entry)
}

// quotaState is the on-disk JSON representation of per-entry quota state. Only
// Usage and Availability are persisted — the static config fields (Harness,
// Budget, etc.) come from the registry on load.
type quotaState struct {
	// Entries maps entry ID → saved per-entry state.
	Entries map[string]entryState `json:"entries"`
}

type entryState struct {
	Usage      int          `json:"usage"`
	Exhausted  bool         `json:"exhausted"`
	ResetAt    time.Time    `json:"reset_at,omitempty"`
	AvailStatus string      `json:"avail_status"`
}

// SaveState persists the current Usage and Availability for all entries to a
// plain-text (JSON) file at path. The file is created or overwritten. Returns
// a non-nil error when the write fails.
func (r *Router) SaveState(path string) error {
	state := quotaState{
		Entries: make(map[string]entryState),
	}
	for _, e := range r.catalog.ListEntries() {
		state.Entries[e.ID] = entryState{
			Usage:       e.Usage,
			Exhausted:   e.Availability.Status == registry.AvailStatusExhausted,
			ResetAt:     e.Availability.ResetAt,
			AvailStatus: string(e.Availability.Status),
		}
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("router.SaveState: marshal: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("router.SaveState: write %q: %w", path, err)
	}
	return nil
}

// LoadState restores Usage and Availability for all entries from the plain-text
// (JSON) file at path. A corrupted or malformed file returns a descriptive error
// — it is never silently treated as a zero-value reset (fail loud on state
// corruption, per REQ-093-04).
//
// Entry IDs present in the file but not in the catalog are silently skipped.
// Entry IDs in the catalog but not in the file retain their current in-memory
// state.
func (r *Router) LoadState(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("router.LoadState: read %q: %w", path, err)
	}

	var state quotaState
	if jsonErr := json.Unmarshal(data, &state); jsonErr != nil {
		return fmt.Errorf("router.LoadState: parse %q: %w (state file may be corrupted)", path, jsonErr)
	}
	if state.Entries == nil {
		return fmt.Errorf("router.LoadState: %q has no 'entries' field (state file may be corrupted or empty)", path)
	}

	for id, saved := range state.Entries {
		entry, ok := r.catalog.LookupEntry(id)
		if !ok {
			// ID from state file not present in this catalog — skip it.
			continue
		}
		entry.Usage = saved.Usage
		avail := registry.AvailStatus(saved.AvailStatus)
		switch avail {
		case registry.AvailStatusAvailable, registry.AvailStatusExhausted:
			entry.Availability = registry.Availability{
				Status:  avail,
				ResetAt: saved.ResetAt,
			}
		default:
			// Unknown status value — treat as corrupted.
			return fmt.Errorf("router.LoadState: %q: entry %q has unknown avail_status %q (state file may be corrupted)", path, id, saved.AvailStatus)
		}
		r.catalog.UpdateEntry(entry)
	}
	return nil
}
