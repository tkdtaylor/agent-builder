package runtime

// Task 161: wire Router.RecordDispatch into the live retry/escalation dispatch
// path (the AVAILABILITY axis's proactive half, distinct from task 160's
// quality-axis OnGateFailure escalation).
//
// These tests drive the REAL production wiring — resolveExecutor (which wraps
// the built executor in a dispatchRecordingExecutor), newRouterEscalationHook
// (which wraps the re-selected executor the same way), buildRetryPolicy, and
// agentloop.RetryingLoop.RunOnce — with the buildCatalog and buildExecutorForEntry
// seams swapped for fakes, exactly like run_160_test.go. buildRetryPolicy is the
// exact function runtime.Run calls, so Usage growth observed here proves the
// RecordDispatch call is live, not a dead wire (pre-task, Usage never moved
// regardless of dispatch volume).
//
//   - TC-161-01: every attempt records exactly one RecordDispatch for the entry
//     used, isolated from task 160's escalation logic (single eligible entry —
//     graceful degradation keeps the same entry across attempts).
//   - TC-161-02: RecordDispatch fires unconditionally of the attempt's outcome —
//     both a passing attempt (02a) and an outright executor error (02b).
//   - TC-161-03: a budgeted entry that reaches Budget.Limit via RecordDispatch is
//     proactively exhausted, and the next Select routes SIDEWAYS to the next
//     eligible entry — the availability axis, not a gate failure.
//   - TC-161-04: an unlimited entry is never exhausted by RecordDispatch, driven
//     through 10 live-wired attempts (regression of task 093's router-level
//     contract, now proven wired end-to-end).
//   - TC-161-05: the single-entry synthetic default Claude path is unaffected —
//     RecordDispatch on the unlimited synthetic entry stays a no-op.
//   - TC-161-06: full regression — see the task's Verification plan
//     (`go test -race -count=1 ./internal/runtime/... ./internal/router/...
//     ./internal/loop/...` and `make check`); covered by running the existing
//     ./internal/runtime, ./internal/router, and ./internal/loop suites alongside
//     this file, not by a dedicated Go test function here.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// withCatalogCapture swaps the buildCatalog seam like withCatalog, but also
// stashes the *registry.Catalog it constructs into *out so the test can inspect
// Usage/Availability after driving the retry loop — the only way to observe
// RecordDispatch's effect from outside internal/router, since Router.catalog is
// unexported.
func withCatalogCapture(t *testing.T, out **registry.Catalog, entries ...registry.RegistryEntry) {
	t.Helper()
	prev := buildCatalog
	t.Cleanup(func() { buildCatalog = prev })
	buildCatalog = func(_ Config) (*registry.Catalog, error) {
		c := registry.NewCatalog()
		for _, e := range entries {
			c.RegisterEntry(e)
		}
		*out = c
		return c, nil
	}
}

// budgetedEntry builds a non-local, budgeted registry entry (SecretRef non-empty,
// Budget.Limit > 0) so RecordDispatch actually increments Usage instead of
// no-opping (an unlimited/local entry never increments — task 093).
func budgetedEntry(id string, tier, cost, limit int) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:             id,
		Harness:        registry.HarnessClaudeCLI,
		CapabilityTier: tier,
		CostWeight:     cost,
		SecretRef:      "tok-" + id,
		Budget:         registry.QuotaBudget{Limit: limit, Window: time.Hour},
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
}

// errExec is a supervisor.Executor whose Run always errors outright (no gate
// ever reached), for TC-161-02b's "RecordDispatch fires on executor error" case.
type errExec struct {
	calls int
}

func (e *errExec) Run(_ context.Context, _ supervisor.Task) (supervisor.Result, error) {
	e.calls++
	return supervisor.Result{}, errors.New("errExec: simulated outright executor failure")
}

// TC-161-01: every attempt records exactly one RecordDispatch call for the
// entry actually used. A second, ineligible entry (tier 0 < spec MinCapability 1)
// keeps the catalog at two entries while ensuring only "cheap-entry" is ever
// selectable — attempt 1 gate-fails, the escalation hook finds no eligible
// alternative (router.ErrNoEligibleExecutor) and gracefully degrades to the SAME
// entry for attempt 2, isolating this test from task 160's escalation/switch
// behavior exactly as the test spec requires.
func TestTC161_01_EveryAttemptRecordsExactlyOneDispatch(t *testing.T) {
	cheap := &fakeExec{id: "cheap-entry", ok: true}
	var cat *registry.Catalog
	withCatalogCapture(t, &cat,
		budgetedEntry("cheap-entry", 1, 1, 100),
		entry("ineligible-entry", 0, 1), // tier 0 < MinCapability 1: never eligible
	)
	withExecutors(t, map[string]*fakeExec{"cheap-entry": cheap})

	config := baseConfig()
	config.MaxAttempts = 2
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: false}, {OK: true}}}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-161-01 RunOnce error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeDone {
		t.Fatalf("TC-161-01 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeDone)
	}
	if outcome.Attempts != 2 {
		t.Fatalf("TC-161-01 Attempts = %d, want 2", outcome.Attempts)
	}
	if cheap.calls != 2 {
		t.Fatalf("TC-161-01 cheap.calls = %d, want 2 (both attempts use the same entry)", cheap.calls)
	}

	e, ok := cat.LookupEntry("cheap-entry")
	if !ok {
		t.Fatal("TC-161-01 entry cheap-entry vanished from catalog")
	}
	// Usage==2 is only reachable if RecordDispatch fired exactly once per attempt:
	// 0 (pre-task dead wire) or 4 (double-firing) would both fail this assertion.
	if e.Usage != 2 {
		t.Fatalf("TC-161-01 Usage = %d, want 2 (RecordDispatch exactly once per attempt for the entry used)", e.Usage)
	}
}

// TC-161-02a: RecordDispatch fires on a passing attempt.
func TestTC161_02a_RecordDispatchFiresOnPassingAttempt(t *testing.T) {
	single := &fakeExec{id: "pass-entry", ok: true}
	var cat *registry.Catalog
	withCatalogCapture(t, &cat, budgetedEntry("pass-entry", 1, 1, 100))
	withExecutors(t, map[string]*fakeExec{"pass-entry": single})

	config := baseConfig()
	config.MaxAttempts = 1
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: true}}}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-161-02a RunOnce error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeDone {
		t.Fatalf("TC-161-02a Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeDone)
	}
	e, ok := cat.LookupEntry("pass-entry")
	if !ok {
		t.Fatal("TC-161-02a entry pass-entry vanished from catalog")
	}
	if e.Usage != 1 {
		t.Fatalf("TC-161-02a Usage = %d, want 1 (RecordDispatch fires on a passing attempt)", e.Usage)
	}
}

// TC-161-02b: RecordDispatch fires even when the executor itself errors outright
// (no gate ever reached) — usage accounting is unconditional on outcome.
func TestTC161_02b_RecordDispatchFiresOnExecutorError(t *testing.T) {
	var cat *registry.Catalog
	withCatalogCapture(t, &cat, budgetedEntry("err-entry", 1, 1, 100))

	failing := &errExec{}
	prevBuild := buildExecutorForEntry
	t.Cleanup(func() { buildExecutorForEntry = prevBuild })
	buildExecutorForEntry = func(_ registry.RegistryEntry, _ Config) (supervisor.Executor, error) {
		return failing, nil
	}

	config := baseConfig()
	config.MaxAttempts = 1
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: true}}} // never reached
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-161-02b RunOnce error = %v, want nil (executor error is a soft OutcomeFail)", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-161-02b Kind = %q, want %q (needs-human after MaxAttempts)", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}
	if failing.calls != 1 {
		t.Fatalf("TC-161-02b failing.calls = %d, want 1", failing.calls)
	}
	e, ok := cat.LookupEntry("err-entry")
	if !ok {
		t.Fatal("TC-161-02b entry err-entry vanished from catalog")
	}
	if e.Usage != 1 {
		t.Fatalf("TC-161-02b Usage = %d, want 1 (RecordDispatch fires even on outright executor error)", e.Usage)
	}
}

// TC-161-03: a budgeted entry that reaches Budget.Limit via RecordDispatch is
// proactively marked exhausted, and the NEXT Select routes SIDEWAYS to the next
// eligible entry — the availability axis, distinct from task 160's quality-axis
// OnGateFailure climbing (entry A never gate-fails in this test).
//
// Driving this through a full RetryingLoop attempt sequence is not possible when
// the gate always passes (the loop returns Done after attempt 1, so the
// escalation hook — which only re-Selects on gate FAILURE — never runs). Instead
// this calls exec.Run directly, twice: exec is resolveExecutor's returned,
// ALREADY-WRAPPED dispatchRecordingExecutor — the exact Run method
// RetryingLoop.RunOnce invokes for every attempt — so this exercises the live
// wiring code path, not a reimplementation of RecordDispatch's own semantics.
func TestTC161_03_BudgetExhaustionRoutesSideways(t *testing.T) {
	var cat *registry.Catalog
	withCatalogCapture(t, &cat,
		budgetedEntry("A", 1, 1, 2),
		entry("B", 1, 10),
	)
	withExecutors(t, map[string]*fakeExec{
		"A": {id: "A", ok: true},
		"B": {id: "B", ok: true},
	})

	exec, selected, rtr, err := resolveExecutor(specTier1(), baseConfig())
	if err != nil {
		t.Fatalf("TC-161-03 resolveExecutor error = %v", err)
	}
	if selected.ID != "A" {
		t.Fatalf("TC-161-03 initial selection = %q, want cheapest %q", selected.ID, "A")
	}

	// Two live-wired dispatch attempts against A: Usage 0->1->2, hitting Budget.Limit.
	if _, err := exec.Run(context.Background(), supervisor.Task{ID: "161"}); err != nil {
		t.Fatalf("TC-161-03 exec.Run #1 error = %v", err)
	}
	if _, err := exec.Run(context.Background(), supervisor.Task{ID: "161"}); err != nil {
		t.Fatalf("TC-161-03 exec.Run #2 error = %v", err)
	}

	aEntry, ok := cat.LookupEntry("A")
	if !ok {
		t.Fatal("TC-161-03 entry A vanished from catalog")
	}
	if aEntry.Usage != 2 {
		t.Fatalf("TC-161-03 A.Usage = %d, want 2", aEntry.Usage)
	}
	if aEntry.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("TC-161-03 A.Availability.Status = %q, want exhausted (Usage reached Budget.Limit)", aEntry.Availability.Status)
	}

	got, err := rtr.Select(toRouterSpec(specTier1()))
	if err != nil {
		t.Fatalf("TC-161-03 Select after exhaustion error = %v", err)
	}
	if got.ID != "B" {
		t.Fatalf("TC-161-03 Select after exhaustion = %q, want %q (availability axis routes sideways)", got.ID, "B")
	}
}

// TC-161-04: an unlimited entry (Budget.Limit == 0) is never marked exhausted by
// RecordDispatch, driven through 10 live-wired attempts. The gate always fails,
// so the escalation hook's r.OnGateFailure marks the entry escalated; since it is
// the only entry, the next r.Select returns router.ErrNoEligibleExecutor and the
// hook gracefully degrades to the SAME (already-wrapped) executor for every
// remaining attempt — so RecordDispatch still fires once per attempt throughout.
func TestTC161_04_UnlimitedEntryNeverExhausted(t *testing.T) {
	only := &fakeExec{id: "only-unlimited", ok: true}
	var cat *registry.Catalog
	withCatalogCapture(t, &cat, entry("only-unlimited", 1, 1)) // Budget zero value => unlimited
	withExecutors(t, map[string]*fakeExec{"only-unlimited": only})

	config := baseConfig()
	config.MaxAttempts = 10
	verdicts := make([]gate.Verdict, 10)
	for i := range verdicts {
		verdicts[i] = gate.Verdict{OK: false}
	}
	verifier := &seqGate{verdicts: verdicts}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-161-04 RunOnce error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-161-04 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}
	if only.calls != 10 {
		t.Fatalf("TC-161-04 only.calls = %d, want 10", only.calls)
	}

	e, ok := cat.LookupEntry("only-unlimited")
	if !ok {
		t.Fatal("TC-161-04 entry only-unlimited vanished from catalog")
	}
	if e.Usage != 0 {
		t.Fatalf("TC-161-04 Usage = %d, want 0 (unlimited entry — RecordDispatch never increments Usage)", e.Usage)
	}
	if e.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("TC-161-04 Availability.Status = %q, want available (unlimited entry is never exhausted)", e.Availability.Status)
	}
}

// TC-161-05: the single-entry synthetic default Claude path is unaffected —
// RecordDispatch on the unlimited synthetic entry (Budget zero value) is a
// no-op, matching pre-task behavior exactly for the most common single-provider
// deployment shape. This drives the same live wiring as TC-160-08, adding the
// RecordDispatch-specific Usage/Availability assertions task 160's test did not
// need.
func TestTC161_05_SyntheticDefaultClaudePathUnaffected(t *testing.T) {
	single := &fakeExec{id: defaultClaudeEntryID, ok: true}
	var cat *registry.Catalog
	withCatalogCapture(t, &cat, defaultClaudeEntry(baseConfig()))
	withExecutors(t, map[string]*fakeExec{defaultClaudeEntryID: single})

	config := baseConfig()
	config.MaxAttempts = 2
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: false}}}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-161-05 RunOnce error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-161-05 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}
	if single.calls != 2 {
		t.Fatalf("TC-161-05 single.calls = %d, want 2 (same executor every attempt — zero-drift with BootstrapEscalationHook)", single.calls)
	}

	e, ok := cat.LookupEntry(defaultClaudeEntryID)
	if !ok {
		t.Fatalf("TC-161-05 entry %q vanished from catalog", defaultClaudeEntryID)
	}
	if e.Usage != 0 {
		t.Fatalf("TC-161-05 Usage = %d, want 0 (synthetic default Claude entry is unlimited)", e.Usage)
	}
	if e.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("TC-161-05 Availability.Status = %q, want available", e.Availability.Status)
	}
}
