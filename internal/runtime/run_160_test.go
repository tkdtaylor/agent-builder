package runtime

// Task 160: wire Router.OnGateFailure into the live retry/escalation hook.
//
// These tests drive the REAL production wiring chain — resolveExecutor (which now
// retains the *router.Router), newRouterEscalationHook, buildRetryPolicy, and
// agentloop.RetryingLoop.RunOnce — with the buildCatalog and buildExecutorForEntry
// seams swapped for fakes. buildRetryPolicy is the exact function runtime.Run
// calls, so a passing test here proves the router-backed hook is live, not a dead
// wire (TC-160-02's cheap→strong escalation is IMPOSSIBLE under the pre-task
// constant BootstrapEscalationHook).
//
//   - TC-160-01: resolveExecutor returns the constructed *router.Router.
//   - TC-160-02: the live dispatch path escalates across a gate failure
//     (cheap fails → stronger succeeds).
//   - TC-160-03: a three-entry ladder climbs monotonically cheap → mid → strong.
//   - TC-160-04: exhausted escalation degrades to needs-human, not a hard error.
//   - TC-160-05: escalation state does not leak across independent dispatches.
//   - TC-160-08: the single-entry (zero-registry) path is behaviorally unaffected.

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// fakeExec is a supervisor.Executor that records that it ran (via a shared order
// recorder and its own call counter) and returns a fixed Result. It is keyed by
// entry ID so a test can assert WHICH executor served each retry attempt.
type fakeExec struct {
	id    string
	ok    bool
	calls int
	order *[]string
}

func (e *fakeExec) Run(_ context.Context, _ supervisor.Task) (supervisor.Result, error) {
	e.calls++
	if e.order != nil {
		*e.order = append(*e.order, e.id)
	}
	return supervisor.Result{Branch: "task/" + e.id, OK: e.ok}, nil
}

// seqGate returns a fixed sequence of verdicts (one per Verify call), clamping to
// the last entry. It lets a test drive OK:true executors into a gate failure so
// the retry loop's escalation hook (the router-backed hook) fires on a genuine
// FailureGate, not merely on an incomplete executor.
type seqGate struct {
	verdicts []gate.Verdict
	calls    int
}

func (g *seqGate) Verify(_ string) gate.Verdict {
	i := g.calls
	g.calls++
	if i >= len(g.verdicts) {
		i = len(g.verdicts) - 1
	}
	return g.verdicts[i]
}

// recordingWriter160 records needs-human escalation writes.
type recordingWriter160 struct {
	writes []string
}

func (w *recordingWriter160) WriteStatus(taskID string, status tasksource.WritableStatus) (tasksource.StatusWriteResult, error) {
	w.writes = append(w.writes, taskID+":"+string(status))
	return tasksource.StatusWriteResult{Path: "docs/tasks/backlog/" + taskID + ".md", Changed: true}, nil
}

// withExecutors swaps the buildExecutorForEntry seam so it returns the fake
// executor registered for each entry ID, and restores it on cleanup.
func withExecutors(t *testing.T, execs map[string]*fakeExec) {
	t.Helper()
	prev := buildExecutorForEntry
	t.Cleanup(func() { buildExecutorForEntry = prev })
	buildExecutorForEntry = func(entry registry.RegistryEntry, _ Config) (supervisor.Executor, error) {
		e, ok := execs[entry.ID]
		if !ok {
			t.Fatalf("withExecutors: no fake executor registered for entry %q", entry.ID)
		}
		return e, nil
	}
}

// entry builds a registry entry at the given cost/tier, always-available.
func entry(id string, tier, cost int) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:             id,
		Harness:        registry.HarnessClaudeCLI,
		CapabilityTier: tier,
		CostWeight:     cost,
		SecretRef:      "tok-" + id, // non-local so the sensitivity hint never reorders
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
}

// driveRetry constructs the retry loop through the real buildRetryPolicy wiring
// and runs it once, returning the outcome.
func driveRetry(t *testing.T, spec recipe.RoutingSpec, config Config, verifier supervisor.Gate, writer agentloop.StatusWriter) (agentloop.RetryOutcome, error) {
	t.Helper()
	policy, exec, err := buildRetryPolicy(spec, config)
	if err != nil {
		t.Fatalf("buildRetryPolicy error = %v", err)
	}
	src := &oneTaskSource{task: supervisor.Task{ID: "160", Repo: "agent-builder", Spec: "docs/tasks/backlog/160.md"}}
	loop, err := agentloop.NewRetryingLoop(src, exec, verifier, "/tmp/work", writer, policy)
	if err != nil {
		t.Fatalf("NewRetryingLoop error = %v", err)
	}
	return loop.RunOnce(context.Background())
}

type oneTaskSource struct {
	task supervisor.Task
}

func (s *oneTaskSource) Next() (supervisor.Task, bool, error) { return s.task, true, nil }

func specTier1() recipe.RoutingSpec {
	return recipe.RoutingSpec{MinCapability: 1}
}

func baseConfig() Config {
	return Config{ClaudeCLI: "claude", Worktree: "/tmp/work", ClaudeToken: "sk-test", MaxAttempts: 3}
}

// TC-160-01: resolveExecutor returns the actual *router.Router it constructed —
// calling Select on the returned router yields the same entry resolveExecutor
// itself selected, proving it is the real instance, not a discarded throwaway.
func TestTC160_01_ResolveExecutorReturnsRouter(t *testing.T) {
	cheap := entry("cheap", 1, 1)
	strong := entry("strong", 2, 10)
	withCatalog(t, strong, cheap)
	withExecutors(t, map[string]*fakeExec{
		"cheap":  {id: "cheap", ok: true},
		"strong": {id: "strong", ok: true},
	})

	_, selected, rtr, err := resolveExecutor(specTier1(), baseConfig())
	if err != nil {
		t.Fatalf("resolveExecutor error = %v", err)
	}
	if rtr == nil {
		t.Fatal("TC-160-01: resolveExecutor returned a nil *router.Router — the router must be retained for escalation")
	}
	if selected.ID != "cheap" {
		t.Fatalf("TC-160-01: initial selection = %q, want cheapest %q", selected.ID, "cheap")
	}
	got, err := rtr.Select(toRouterSpec(specTier1()))
	if err != nil {
		t.Fatalf("TC-160-01: Select on returned router error = %v", err)
	}
	if got.ID != selected.ID {
		t.Fatalf("TC-160-01: returned router Select = %q, want same entry resolveExecutor selected (%q) — not the actual router instance", got.ID, selected.ID)
	}
}

// TC-160-02: the live dispatch path escalates across a gate failure. Attempt 1
// uses the cheap entry and gate-fails; attempt 2 uses the STRONGER entry and
// succeeds. Under the pre-task BootstrapEscalationHook attempt 2 would re-run the
// cheap entry (strong.calls would be 0) — the strong.calls==1 assertion is the
// load-bearing distinguishing proof.
func TestTC160_02_LiveDispatchEscalatesCheapToStrong(t *testing.T) {
	cheap := &fakeExec{id: "cheap", ok: true}
	strong := &fakeExec{id: "strong", ok: true}
	withCatalog(t, entry("cheap", 1, 1), entry("strong", 2, 10))
	withExecutors(t, map[string]*fakeExec{"cheap": cheap, "strong": strong})

	config := baseConfig()
	config.MaxAttempts = 2
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: false}, {OK: true}}}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-160-02 RunOnce error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeDone {
		t.Fatalf("TC-160-02 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeDone)
	}
	if cheap.calls != 1 {
		t.Fatalf("TC-160-02 cheap.calls = %d, want 1 (attempt 1 only)", cheap.calls)
	}
	if strong.calls != 1 {
		t.Fatalf("TC-160-02 strong.calls = %d, want 1 — attempt 2 must escalate to the stronger entry (IMPOSSIBLE under BootstrapEscalationHook)", strong.calls)
	}
	if outcome.Branch != "task/strong" {
		t.Fatalf("TC-160-02 Branch = %q, want task/strong (the stronger entry served the successful attempt)", outcome.Branch)
	}
	if outcome.Attempts != 2 {
		t.Fatalf("TC-160-02 Attempts = %d, want 2", outcome.Attempts)
	}
}

// TC-160-03: a three-entry ladder (cheap < mid < strong by cost, all tier 1)
// climbs monotonically cheap → mid → strong across three attempts as each cheaper
// entry gate-fails and is removed from the eligible set.
func TestTC160_03_ThreeEntryLadderClimbsMonotonically(t *testing.T) {
	var order []string
	cheap := &fakeExec{id: "cheap", ok: true, order: &order}
	mid := &fakeExec{id: "mid", ok: true, order: &order}
	strong := &fakeExec{id: "strong", ok: true, order: &order}
	withCatalog(t, entry("strong", 1, 10), entry("cheap", 1, 1), entry("mid", 1, 5))
	withExecutors(t, map[string]*fakeExec{"cheap": cheap, "mid": mid, "strong": strong})

	config := baseConfig()
	config.MaxAttempts = 3
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: false}, {OK: false}, {OK: true}}}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-160-03 RunOnce error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeDone {
		t.Fatalf("TC-160-03 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeDone)
	}
	want := []string{"cheap", "mid", "strong"}
	if len(order) != 3 || order[0] != want[0] || order[1] != want[1] || order[2] != want[2] {
		t.Fatalf("TC-160-03 execution order = %v, want %v (monotonic cost climb)", order, want)
	}
	if cheap.calls != 1 || mid.calls != 1 || strong.calls != 1 {
		t.Fatalf("TC-160-03 calls = cheap:%d mid:%d strong:%d, want 1/1/1", cheap.calls, mid.calls, strong.calls)
	}
	if outcome.Attempts != 3 || outcome.Branch != "task/strong" {
		t.Fatalf("TC-160-03 Attempts/Branch = %d/%q, want 3/task/strong", outcome.Attempts, outcome.Branch)
	}
}

// TC-160-04: when every eligible entry has gate-failed, the hook returns the
// current executor unchanged (graceful degradation) and RunOnce escalates to
// needs-human after MaxAttempts — NOT a hard error out of RunOnce. All attempts
// use the single available executor, matching BootstrapEscalationHook's behavior
// for this degenerate case.
func TestTC160_04_ExhaustedEscalationDegradesGracefully(t *testing.T) {
	only := &fakeExec{id: "only", ok: true}
	withCatalog(t, entry("only", 1, 1))
	withExecutors(t, map[string]*fakeExec{"only": only})

	config := baseConfig()
	config.MaxAttempts = 3
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: false}}}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-160-04 RunOnce error = %v, want nil (graceful degradation, no hard error)", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-160-04 Kind = %q, want %q (needs-human)", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}
	if only.calls != 3 {
		t.Fatalf("TC-160-04 only.calls = %d, want 3 (same executor every attempt once eligible set exhausted)", only.calls)
	}
	if len(writer.writes) != 1 || writer.writes[0] != "160:needs-human" {
		t.Fatalf("TC-160-04 status writes = %v, want exactly one 160:needs-human", writer.writes)
	}
	if outcome.Attempts != 3 {
		t.Fatalf("TC-160-04 Attempts = %d, want 3", outcome.Attempts)
	}
}

// TC-160-05: escalation state is per-dispatch. Two independent buildRetryPolicy
// dispatches over the same catalog each get a FRESH *router.Router, so the second
// dispatch's first attempt uses the CHEAP entry again — it does not carry over the
// first dispatch's exhausted escalation set (which would make it start at strong).
func TestTC160_05_EscalationStateNotLeakedAcrossDispatches(t *testing.T) {
	withCatalog(t, entry("cheap", 1, 1), entry("strong", 2, 10))

	config := baseConfig()
	config.MaxAttempts = 2

	// Dispatch 1: cheap fails, strong succeeds.
	var order1 []string
	withExecutors(t, map[string]*fakeExec{
		"cheap":  {id: "cheap", ok: true, order: &order1},
		"strong": {id: "strong", ok: true, order: &order1},
	})
	if _, err := driveRetry(t, specTier1(), config, &seqGate{verdicts: []gate.Verdict{{OK: false}, {OK: true}}}, &recordingWriter160{}); err != nil {
		t.Fatalf("TC-160-05 dispatch 1 error = %v", err)
	}
	if len(order1) == 0 || order1[0] != "cheap" {
		t.Fatalf("TC-160-05 dispatch 1 first attempt = %v, want cheap first", order1)
	}

	// Dispatch 2: independent. Its first attempt MUST use cheap again (fresh router).
	var order2 []string
	withExecutors(t, map[string]*fakeExec{
		"cheap":  {id: "cheap", ok: true, order: &order2},
		"strong": {id: "strong", ok: true, order: &order2},
	})
	outcome, err := driveRetry(t, specTier1(), config, &seqGate{verdicts: []gate.Verdict{{OK: false}, {OK: true}}}, &recordingWriter160{})
	if err != nil {
		t.Fatalf("TC-160-05 dispatch 2 error = %v", err)
	}
	if len(order2) == 0 || order2[0] != "cheap" {
		t.Fatalf("TC-160-05 dispatch 2 first attempt = %v, want cheap first — escalation state leaked from dispatch 1 (would start at strong)", order2)
	}
	if outcome.Kind != agentloop.RetryOutcomeDone {
		t.Fatalf("TC-160-05 dispatch 2 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeDone)
	}
}

// TC-160-08: the single-entry (zero-registry, synthetic default Claude entry)
// path is behaviorally unaffected — there is nothing to escalate TO, so the
// router-backed hook behaves exactly like BootstrapEscalationHook: the same
// executor serves every attempt and the run escalates to needs-human on
// exhaustion. This models the most common single-provider deployment.
func TestTC160_08_SingleEntryPathUnaffected(t *testing.T) {
	single := &fakeExec{id: defaultClaudeEntryID, ok: true}
	withCatalog(t, defaultClaudeEntry(baseConfig()))
	withExecutors(t, map[string]*fakeExec{defaultClaudeEntryID: single})

	config := baseConfig()
	config.MaxAttempts = 2
	verifier := &seqGate{verdicts: []gate.Verdict{{OK: false}}}
	writer := &recordingWriter160{}

	outcome, err := driveRetry(t, specTier1(), config, verifier, writer)
	if err != nil {
		t.Fatalf("TC-160-08 RunOnce error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-160-08 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}
	if single.calls != 2 {
		t.Fatalf("TC-160-08 single.calls = %d, want 2 (same executor every attempt — zero-drift with BootstrapEscalationHook)", single.calls)
	}
}
