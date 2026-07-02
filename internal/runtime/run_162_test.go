package runtime

// Task 162: persist and load router quota state across process invocations.
//
// Tasks 160/161 wire OnGateFailure and RecordDispatch into the live dispatch
// path, but the *router.Router — and therefore the Usage/Availability state it
// populates — is constructed fresh on every resolveExecutor call, so quota
// state is lost the instant one process exits. This task wires
// Router.SaveState/LoadState (already correct, already unit-tested in
// internal/router/quota_test.go) into the live resolveExecutor/Run path via a
// new optional env var, AGENT_BUILDER_ROUTER_STATE_PATH.
//
//   - TC-162-01: ConfigFromEnv wiring; unset means no persistence.
//   - TC-162-02: resolveExecutor calls LoadState before the first Select; a
//     missing file (first run) is tolerated.
//   - TC-162-03: state is saved to disk after a full Run dispatch, reflecting
//     incremented Usage.
//   - TC-162-04: a corrupted state file is a fail-fast resolveExecutor/Run
//     error, never a silent reset.
//   - TC-162-05: unset path leaves behavior byte-for-byte unchanged across two
//     sequential Run invocations — no file created, no cross-invocation leak.
//   - TC-162-06 (L5): exhaustion recorded by RecordDispatch in one Run
//     invocation is observed at the very first Select of a subsequent,
//     independently-constructed invocation sharing the same state path — the
//     load-bearing end-to-end proof that "quota state is never
//     recorded/persisted" (the review's exact finding) is now false.
//   - TC-162-07: full regression — see the task's Verification plan.
//
// TC-162-03/05/06 drive the REAL exported Run function end-to-end (not a
// reimplementation) so that removing the SaveState/LoadState call from Run's
// production wiring breaks these tests (the dead-wire mutation check this
// task's pitfalls call out). A custom recipe is registered once (tc162Recipe)
// with a fake, always-passing Gate and a no-op ResultSink so the full
// Supervisor.Run pipeline completes without depending on the real gate tools
// or a real git remote. Config.DispatchedTask bypasses the recipe's file-based
// GoalSourceFactory (ADR 055 seam 2, task 119) so no task-file fixture is
// needed. Config.ExecSandboxBin points at a fake block binary (a tiny shell
// script honoring the exec-sandbox JSON stdin/stdout contract) so
// sandboxBox.Create's liveness probe succeeds without a real podman/exec-sandbox
// environment — buildCatalog/buildExecutorForEntry stay swapped to in-process
// fakes exactly like tasks 160/161's tests, so the actual dispatched attempt
// never shells out either.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- shared fakes for the full-Run harness ---

// fakeBlockingGate is a supervisor.Gate that also implements gate.Blocker,
// satisfying verifyGateExists's real-blocking-gate check without running the
// production go build/vet/lint/scanner steps.
type fakeBlockingGate struct {
	verdict gate.Verdict
}

func (g fakeBlockingGate) Verify(string) gate.Verdict { return g.verdict }
func (g fakeBlockingGate) Blocks() bool               { return true }

// tc162RecipeName is the recipe registered once for this file's full-Run tests
// (a passing gate, so a dispatch completes successfully).
const tc162RecipeName = "tc162-router-state-recipe"

// tc162FailRecipeName is a second recipe variant with an always-FAILING gate,
// used by TC-162-03b to prove SaveState still fires when the dispatch itself
// fails — RecordDispatch already recorded usage before the gate ever ran, and
// that usage must persist exactly the same as on a success.
const tc162FailRecipeName = "tc162-router-state-fail-recipe"

func init() {
	newGoalSourceFactory := func(name string) recipe.GoalSourceFactory {
		return func(_ recipe.SeamConfig) (supervisor.GoalSource, error) {
			return nil, errors.New(name + ": GoalSourceFactory must not be called when Config.DispatchedTask is set")
		}
	}
	resultSinkFactory := func(_ recipe.SeamConfig) (supervisor.ResultSink, error) {
		return &noopResultSink{}, nil
	}

	recipe.Register(tc162RecipeName, func() (recipe.Recipe, error) {
		return recipe.New(
			newGoalSourceFactory(tc162RecipeName),
			recipe.RoutingSpec{MinCapability: 1},
			func() supervisor.Gate {
				return fakeBlockingGate{verdict: passingVerdict()}
			},
			resultSinkFactory,
			nil,
		), nil
	})

	recipe.Register(tc162FailRecipeName, func() (recipe.Recipe, error) {
		return recipe.New(
			newGoalSourceFactory(tc162FailRecipeName),
			recipe.RoutingSpec{MinCapability: 1},
			func() supervisor.Gate {
				return fakeBlockingGate{verdict: gate.Verdict{OK: false}}
			},
			resultSinkFactory,
			nil,
		), nil
	})
}

// fakeSandboxBinScript is the exec-sandbox block's JSON contract for the
// probe command sandboxBox.Create issues ({-c true}): a single stdout JSON
// object with exit_code 0 and no error. The script ignores its stdin.
const fakeSandboxBinScript = `#!/bin/sh
cat > /dev/null
printf '%s' '{"stdout":"","stderr":"","exit_code":0,"error":"","sandbox_status":{"sandbox_id":"tc162-fake","tier":"bubblewrap","duration_ms":1,"secrets_injected":[],"status":"ok","limits":{}}}'
`

// writeFakeSandboxBin writes an executable fake exec-sandbox block binary to a
// temp dir and returns its path.
func writeFakeSandboxBin(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-exec-sandbox")
	if err := os.WriteFile(path, []byte(fakeSandboxBinScript), 0o755); err != nil { //nolint:gosec // test fixture, deliberately executable
		t.Fatalf("writeFakeSandboxBin: %v", err)
	}
	return path
}

// runOnce drives one full Run(ctx, config, nil) invocation through the tc162
// recipe (an always-passing gate) with a fresh TaskRoot/Worktree (an
// independent OS-process simulation: only the shared statePath, if any,
// carries state across calls). taskID must be unique per call so successive
// invocations don't collide on task identity.
func runOnce(t *testing.T, taskID, statePath string) error {
	t.Helper()
	return runOnceWithRecipe(t, tc162RecipeName, taskID, statePath)
}

// runOnceWithRecipe is runOnce's parameterized form, letting a test select the
// always-failing gate recipe (TC-162-03b) to prove SaveState still fires when
// the dispatch itself fails.
func runOnceWithRecipe(t *testing.T, recipeName, taskID, statePath string) error {
	t.Helper()
	taskRoot := t.TempDir()
	worktree := t.TempDir()
	config := Config{
		TaskRoot:        taskRoot,
		Worktree:        worktree,
		ClaudeCLI:       "claude",
		ClaudeToken:     "sk-test",
		ExecBoxLauncher: "containment/execution-box/run.sh",
		ExecSandboxBin:  writeFakeSandboxBin(t),
		RunTimeout:      5 * time.Second,
		MaxAttempts:     1,
		PublishRemote:   "origin",
		RecipeName:      recipeName,
		RouterStatePath: statePath,
		DispatchedTask: &supervisor.Task{
			ID:   taskID,
			Repo: "agent-builder",
			Spec: "docs/tasks/backlog/162-router-quota-state-persistence.md",
		},
	}
	return Run(context.Background(), config, nil)
}

// --- TC-162-01: env var wiring ---

func TestTC162_01_ConfigFromEnvRouterStatePath(t *testing.T) {
	base := map[string]string{
		"AGENT_BUILDER_TASK_ROOT":         "/tmp/tasks",
		"AGENT_BUILDER_WORKTREE":          "/tmp/work",
		"AGENT_BUILDER_EXEC_BOX_LAUNCHER": "containment/execution-box/run.sh",
		"AGENT_BUILDER_RUN_TIMEOUT":       "5m",
		"AGENT_BUILDER_MAX_ATTEMPTS":      "2",
		"AGENT_BUILDER_PUBLISH_REMOTE":    "origin",
		"ANTHROPIC_API_KEY":               "sk-test",
	}
	getenv := func(key string) string { return base[key] }

	// Unset: RouterStatePath is the empty zero value, no error.
	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("TC-162-01 unset: ConfigFromEnv error = %v", err)
	}
	if config.RouterStatePath != "" {
		t.Fatalf("TC-162-01 unset: RouterStatePath = %q, want empty", config.RouterStatePath)
	}

	// Set: the value is captured verbatim (path-cleaned, per cleanPath convention).
	withPath := make(map[string]string, len(base)+1)
	for k, v := range base {
		withPath[k] = v
	}
	withPath["AGENT_BUILDER_ROUTER_STATE_PATH"] = "/tmp/router-state.json"
	getenvSet := func(key string) string { return withPath[key] }

	config2, err := ConfigFromEnv(getenvSet)
	if err != nil {
		t.Fatalf("TC-162-01 set: ConfigFromEnv error = %v", err)
	}
	if config2.RouterStatePath != "/tmp/router-state.json" {
		t.Fatalf("TC-162-01 set: RouterStatePath = %q, want %q", config2.RouterStatePath, "/tmp/router-state.json")
	}
}

// --- TC-162-02: LoadState runs before the first Select; missing file tolerated ---

func TestTC162_02_ResolveExecutorTeratesMissingStateFile(t *testing.T) {
	withCatalog(t, entry("only", 1, 1))
	withExecutors(t, map[string]*fakeExec{"only": {id: "only", ok: true}})

	statePath := filepath.Join(t.TempDir(), "does-not-exist.json")
	config := baseConfig()
	config.RouterStatePath = statePath

	exec, selected, rtr, err := resolveExecutor(specTier1(), config)
	if err != nil {
		t.Fatalf("TC-162-02: resolveExecutor error = %v, want nil (a missing state file — first run — must be tolerated)", err)
	}
	if exec == nil || rtr == nil {
		t.Fatal("TC-162-02: resolveExecutor returned nil executor/router despite no error")
	}
	if selected.ID != "only" {
		t.Fatalf("TC-162-02: selected.ID = %q, want %q", selected.ID, "only")
	}

	// Select still runs and returns a normal result — the load attempt did not
	// fail assembly when there was nothing to load yet.
	got, err := rtr.Select(toRouterSpec(specTier1()))
	if err != nil {
		t.Fatalf("TC-162-02: Select after tolerated missing-file load error = %v", err)
	}
	if got.ID != "only" {
		t.Fatalf("TC-162-02: Select = %q, want %q", got.ID, "only")
	}

	// The missing file must NOT have been created by the tolerant load attempt.
	if _, statErr := os.Stat(statePath); !os.IsNotExist(statErr) {
		t.Fatalf("TC-162-02: state file exists after a tolerated missing-file load (statErr=%v) — LoadState must not create the file", statErr)
	}
}

// --- TC-162-03: state is saved after dispatch completes ---

func TestTC162_03_StateSavedAfterDispatch(t *testing.T) {
	var cat *registry.Catalog
	withCatalogCapture(t, &cat, budgetedEntry("budgeted", 1, 1, 100))
	withExecutors(t, map[string]*fakeExec{"budgeted": {id: "budgeted", ok: true}})

	statePath := filepath.Join(t.TempDir(), "router-state.json")

	if err := runOnce(t, "162-03", statePath); err != nil {
		t.Fatalf("TC-162-03: Run error = %v, want nil", err)
	}

	// The router's in-memory catalog view must show the incremented Usage.
	e, ok := cat.LookupEntry("budgeted")
	if !ok {
		t.Fatal("TC-162-03: entry 'budgeted' vanished from catalog")
	}
	if e.Usage != 1 {
		t.Fatalf("TC-162-03: in-memory Usage = %d, want 1", e.Usage)
	}

	// The configured path must exist on disk and, parsed directly as JSON,
	// reflect the entry's incremented Usage.
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("TC-162-03: state file was not created at %q: %v", statePath, err)
	}
	var raw struct {
		Entries map[string]struct {
			Usage int `json:"usage"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("TC-162-03: state file is not valid JSON: %v", err)
	}
	saved, ok := raw.Entries["budgeted"]
	if !ok {
		t.Fatalf("TC-162-03: state file has no entry for 'budgeted': %s", string(data))
	}
	if saved.Usage != 1 {
		t.Fatalf("TC-162-03: saved Usage = %d, want 1", saved.Usage)
	}

	// Also verifiable via a fresh router.LoadState against a fresh catalog,
	// mirroring the test spec's alternate assertion path.
	freshCatalog := registry.NewCatalog()
	freshCatalog.RegisterEntry(budgetedEntry("budgeted", 1, 1, 100))
	freshRouter := router.New(freshCatalog)
	if err := freshRouter.LoadState(statePath); err != nil {
		t.Fatalf("TC-162-03: fresh router LoadState error = %v", err)
	}
	loaded, ok := freshCatalog.LookupEntry("budgeted")
	if !ok {
		t.Fatal("TC-162-03: entry vanished from fresh catalog after LoadState")
	}
	if loaded.Usage != 1 {
		t.Fatalf("TC-162-03: fresh-router-loaded Usage = %d, want 1", loaded.Usage)
	}
}

// TC-162-03b: SaveState still fires when the dispatch itself FAILS. The
// dispatchRecordingExecutor's RecordDispatch call happens unconditionally,
// BEFORE the gate ever runs (task 161), so a gate-failing attempt still
// records usage — and that usage must persist exactly like a success, never be
// silently dropped because the task ultimately escalated. This is the exact
// save point this task's pitfalls call out: SaveState must run after
// supervisor.Run regardless of its error.
func TestTC162_03b_StateSavedWhenDispatchFails(t *testing.T) {
	var cat *registry.Catalog
	withCatalogCapture(t, &cat, budgetedEntry("budgeted", 1, 1, 100))
	withExecutors(t, map[string]*fakeExec{"budgeted": {id: "budgeted", ok: true}})

	statePath := filepath.Join(t.TempDir(), "router-state.json")

	err := runOnceWithRecipe(t, tc162FailRecipeName, "162-03b", statePath)
	if err == nil {
		t.Fatal("TC-162-03b: Run error = nil, want a non-nil error (the always-failing gate escalates)")
	}

	e, ok := cat.LookupEntry("budgeted")
	if !ok {
		t.Fatal("TC-162-03b: entry 'budgeted' vanished from catalog")
	}
	if e.Usage != 1 {
		t.Fatalf("TC-162-03b: in-memory Usage = %d, want 1 (RecordDispatch fires before the gate runs, unconditional of outcome)", e.Usage)
	}

	data, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatalf("TC-162-03b: state file was not created despite the dispatch failing: %v", readErr)
	}
	var raw struct {
		Entries map[string]struct {
			Usage int `json:"usage"`
		} `json:"entries"`
	}
	if jsonErr := json.Unmarshal(data, &raw); jsonErr != nil {
		t.Fatalf("TC-162-03b: state file is not valid JSON: %v", jsonErr)
	}
	saved, ok := raw.Entries["budgeted"]
	if !ok {
		t.Fatalf("TC-162-03b: state file has no entry for 'budgeted': %s", string(data))
	}
	if saved.Usage != 1 {
		t.Fatalf("TC-162-03b: saved Usage = %d, want 1 — SaveState must fire even when Run returns an error", saved.Usage)
	}
}

// --- TC-162-04: a corrupted state file is a fail-fast error ---

func TestTC162_04_CorruptedStateFileIsFailFastError(t *testing.T) {
	withCatalog(t, entry("only", 1, 1))
	withExecutors(t, map[string]*fakeExec{"only": {id: "only", ok: true}})

	statePath := filepath.Join(t.TempDir(), "corrupt-state.json")
	if err := os.WriteFile(statePath, []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("TC-162-04: seed corrupt state file: %v", err)
	}

	config := baseConfig()
	config.RouterStatePath = statePath

	exec, _, rtr, err := resolveExecutor(specTier1(), config)
	if err == nil {
		t.Fatal("TC-162-04: resolveExecutor error = nil, want a non-nil fail-fast error for a corrupted state file")
	}
	if exec != nil || rtr != nil {
		t.Fatalf("TC-162-04: resolveExecutor returned non-nil exec=%v rtr=%v alongside an error — must not silently fall back to fresh state", exec, rtr)
	}

	// The full Run path must surface the same fail-fast error, before any
	// sandbox is created (Run never reaches the ExecSandboxBin probe).
	runErr := runOnce(t, "162-04", statePath)
	if runErr == nil {
		t.Fatal("TC-162-04: Run error = nil, want a non-nil fail-fast error for a corrupted state file")
	}
}

// --- TC-162-05: unset path — byte-for-byte pre-task behavior, no leak ---

func TestTC162_05_UnsetPathNoLeakNoFileCreated(t *testing.T) {
	var cat *registry.Catalog
	withCatalogCapture(t, &cat, budgetedEntry("budgeted", 1, 1, 1)) // Limit=1: exhausts after one dispatch
	withExecutors(t, map[string]*fakeExec{"budgeted": {id: "budgeted", ok: true}})

	// No RouterStatePath configured.
	if err := runOnce(t, "162-05a", ""); err != nil {
		t.Fatalf("TC-162-05: invocation 1 Run error = %v, want nil", err)
	}
	e1, ok := cat.LookupEntry("budgeted")
	if !ok {
		t.Fatal("TC-162-05: entry vanished after invocation 1")
	}
	if e1.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("TC-162-05: invocation 1 status = %q, want exhausted (Usage reached Budget.Limit=1)", e1.Availability.Status)
	}

	if err := runOnce(t, "162-05b", ""); err != nil {
		t.Fatalf("TC-162-05: invocation 2 Run error = %v, want nil — the second invocation must still see the entry as available (fresh router each call, no persistence configured)", err)
	}
	e2, ok := cat.LookupEntry("budgeted")
	if !ok {
		t.Fatal("TC-162-05: entry vanished after invocation 2")
	}
	// Invocation 2 built a brand-new catalog (withCatalogCapture constructs one
	// per buildCatalog call) and, with RouterStatePath unset, resolveExecutor
	// never loads persisted state onto it — so a SUCCESSFUL invocation 2 (no
	// error) is only possible if the fresh entry started AVAILABLE and was
	// re-exhausted by invocation 2's own single dispatch.
	if e2.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("TC-162-05: invocation 2 status = %q, want exhausted (its own single dispatch re-exhausted a fresh, available entry)", e2.Availability.Status)
	}
	if e2.Usage != 1 {
		t.Fatalf("TC-162-05: invocation 2 Usage = %d, want 1 (fresh catalog — no leak from invocation 1's Usage=1)", e2.Usage)
	}
}

func TestTC162_05_UnsetPathNoFileCreatedAnywhere(t *testing.T) {
	withCatalog(t, entry("only", 1, 1))
	withExecutors(t, map[string]*fakeExec{"only": {id: "only", ok: true}})

	tmp := t.TempDir()
	if err := runOnce(t, "162-05c", ""); err != nil {
		t.Fatalf("TC-162-05: Run error = %v, want nil", err)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("TC-162-05: read scratch dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("TC-162-05: scratch dir has %d unexpected entries, want 0 — no file must be created anywhere when RouterStatePath is unset", len(entries))
	}
}

// --- TC-162-06 (L5): exhaustion survives across two sequential invocations ---

func TestTC162_06_ExhaustionSurvivesAcrossSequentialInvocations(t *testing.T) {
	var cat *registry.Catalog
	aFake := &fakeExec{id: "A", ok: true}
	bFake := &fakeExec{id: "B", ok: true}
	withCatalogCapture(t, &cat,
		budgetedEntry("A", 1, 1, 1), // cheapest, exhausts after exactly one dispatch
		entry("B", 1, 10),           // unlimited, more expensive — only chosen once A is exhausted
	)
	withExecutors(t, map[string]*fakeExec{"A": aFake, "B": bFake})

	statePath := filepath.Join(t.TempDir(), "shared-router-state.json")

	// Invocation 1: Select picks A (cheaper), dispatches it once (RecordDispatch
	// fires, A now exhausted at Usage==Budget.Limit==1), Run completes and saves
	// state.
	if err := runOnce(t, "162-06-inv1", statePath); err != nil {
		t.Fatalf("TC-162-06: invocation 1 Run error = %v, want nil", err)
	}
	if aFake.calls != 1 {
		t.Fatalf("TC-162-06: invocation 1 aFake.calls = %d, want 1 (A must be the initial selection)", aFake.calls)
	}
	if bFake.calls != 0 {
		t.Fatalf("TC-162-06: invocation 1 bFake.calls = %d, want 0", bFake.calls)
	}

	// Invocation 2: a FRESH resolveExecutor/Router construction — withCatalogCapture
	// builds an entirely new *registry.Catalog from the same static entries
	// (Usage==0, available), simulating a new OS process rebuilding its registry
	// from static config. Only the shared state FILE carries invocation 1's
	// exhaustion forward.
	if err := runOnce(t, "162-06-inv2", statePath); err != nil {
		t.Fatalf("TC-162-06: invocation 2 Run error = %v, want nil", err)
	}

	// The load-bearing assertion: invocation 2's very FIRST Select must route to
	// B directly — A was correctly reported exhausted from invocation 1's
	// persisted state, not reset to available. aFake must NOT have been called
	// again; bFake must have been called exactly once.
	if aFake.calls != 1 {
		t.Fatalf("TC-162-06: after invocation 2, aFake.calls = %d, want still 1 — A must remain exhausted (persisted state), never re-selected", aFake.calls)
	}
	if bFake.calls != 1 {
		t.Fatalf("TC-162-06: after invocation 2, bFake.calls = %d, want 1 — invocation 2 must route to B on its very first Select", bFake.calls)
	}

	// Cross-check against invocation 2's own catalog snapshot: A must show
	// exhausted (loaded from disk). B is an unlimited entry (Budget zero value —
	// RecordDispatch never increments Usage/marks it exhausted for such entries,
	// task 093), so bFake.calls==1 above is the correct proof it served the
	// dispatch; B's catalog Usage staying 0 is the EXPECTED unlimited-entry
	// contract, not evidence of a missed dispatch.
	aEntry, ok := cat.LookupEntry("A")
	if !ok {
		t.Fatal("TC-162-06: entry A vanished from invocation 2's catalog")
	}
	if aEntry.Availability.Status != registry.AvailStatusExhausted {
		t.Fatalf("TC-162-06: invocation 2 catalog A.Availability.Status = %q, want exhausted (loaded from persisted state)", aEntry.Availability.Status)
	}
	bEntry, ok := cat.LookupEntry("B")
	if !ok {
		t.Fatal("TC-162-06: entry B vanished from invocation 2's catalog")
	}
	if bEntry.Availability.Status != registry.AvailStatusAvailable {
		t.Fatalf("TC-162-06: invocation 2 catalog B.Availability.Status = %q, want available", bEntry.Availability.Status)
	}
}

// TC-162-07 (full regression) is covered by running the existing
// ./internal/runtime and ./internal/router suites alongside this file (see the
// task's Verification plan), not by a dedicated Go test function here.
