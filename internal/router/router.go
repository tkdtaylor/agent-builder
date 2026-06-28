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
// This package holds router state IN MEMORY only. Persistence of the quota tally
// and the injected clock seam for reset-window logic are task 093 (out of scope
// here). OnQuotaExhausted records an explicit ResetAt on the entry's Availability,
// but this router does not yet re-enable an exhausted entry when a clock passes
// ResetAt — that is the clock seam in task 093.
package router

import (
	"errors"
	"sort"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// ErrNoEligibleExecutor is returned by Select when no registry entry satisfies
// the dispatch's capability requirement, availability filter, and the set of
// entries already escalated past via OnGateFailure.
var ErrNoEligibleExecutor = errors.New("router: no eligible executor")

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
// the mutable availability state on the catalog's entries (in memory).
//
// escalated holds the IDs the current dispatch has already tried and gate-failed.
// Select skips them so the next call climbs to the next-stronger eligible entry.
// ResetEscalation clears it at the start of a fresh dispatch.
type Router struct {
	catalog   *registry.Catalog
	escalated map[string]bool
}

// New constructs a Router over the given catalog. The catalog supplies the
// entry set and carries the per-entry availability state the router mutates.
func New(catalog *registry.Catalog) *Router {
	return &Router{
		catalog:   catalog,
		escalated: make(map[string]bool),
	}
}

// Select returns the cheapest eligible entry for the dispatch.
//
// An entry is eligible when it (a) meets the capability floor
// (CapabilityTier >= spec.MinCapability), (b) is currently available
// (Availability.Status == AvailStatusAvailable), and (c) has not already been
// escalated past via OnGateFailure in this dispatch.
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
// NOT cleared — that is a separate axis with its own lifecycle (task 093's clock
// seam re-enables an exhausted entry once its ResetAt passes).
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
