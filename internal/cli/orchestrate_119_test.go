package cli

// Tests for task 119: Route the dispatched sub-goal task to the worker
// (ADR 055 seam 2).
//
// TC-001: dispatched sub.Task drives the worker
// TC-003: blank/invalid dispatched task is a hard error, not a silent no-op

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// tc119KeyMaterial generates the Ed25519 key and ReplayCaches needed by
// newTransportDispatch. Window=0 matches the pattern in orchestrate_test.go.
func tc119KeyMaterial(t *testing.T) (ed25519.PrivateKey, *envelope.ReplayCache, *envelope.ReplayCache) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("tc119KeyMaterial: ed25519: %v", err)
	}
	return priv, envelope.NewReplayCache(0), envelope.NewReplayCache(0)
}

// workerCallRecord captures what the spy runWorker received.
type workerCallRecord struct {
	cfg runtimewiring.Config
}

// tc119SpyRunWorker replaces the runWorker seam for the duration of one test.
// The spy records every Config it sees; the caller defers restore().
func tc119SpyRunWorker(t *testing.T) (restore func(), records *[]workerCallRecord) {
	t.Helper()
	rec := make([]workerCallRecord, 0, 1)
	prev := runWorker
	runWorker = func(_ context.Context, cfg runtimewiring.Config, _ io.Writer) error {
		rec = append(rec, workerCallRecord{cfg: cfg})
		return nil
	}
	return func() { runWorker = prev }, &rec
}

// =============================================================================
// TC-001: dispatched sub.Task drives the worker
// =============================================================================

// TestTC119_01_DispatchedTaskDrivesWorker verifies that when the orchestrate
// dispatch seam dispatches a SubGoal, the Config passed to the worker has
// DispatchedTask set to the exact Task from sub.Task (ID, Spec, Repo), not nil
// and not a TaskRoot file.
func TestTC119_01_DispatchedTaskDrivesWorker(t *testing.T) {
	restore, records := tc119SpyRunWorker(t)
	defer restore()

	sigKey, workCache, resultCache := tc119KeyMaterial(t)
	sink := audit.NewFakeSink()

	dispatch, err := newTransportDispatch(sigKey, workCache, resultCache, sink, discardLogger(), nil)
	if err != nil {
		t.Fatalf("TC-001: newTransportDispatch: %v", err)
	}

	want := supervisor.Task{
		ID:   "goal-1-0",
		Spec: "add a Reverse function",
		Repo: "exec-sandbox",
	}
	sub := orchestrator.SubGoal{
		RecipeName: "coding-agent",
		Task:       want,
	}
	base := runtimewiring.Config{
		TaskRoot: t.TempDir(),
		Worktree: t.TempDir(),
	}

	if err := dispatch(context.Background(), sub, base); err != nil {
		t.Fatalf("TC-001: dispatch error: %v", err)
	}

	if len(*records) != 1 {
		t.Fatalf("TC-001: runWorker called %d times, want 1", len(*records))
	}
	got := (*records)[0].cfg

	// Assert DispatchedTask carries the exact sub.Task — not nil, not a file.
	if got.DispatchedTask == nil {
		t.Fatal("TC-001: worker cfg.DispatchedTask is nil — worker would read task files instead of the dispatched goal")
	}
	if got.DispatchedTask.ID != want.ID {
		t.Errorf("TC-001: DispatchedTask.ID = %q, want %q", got.DispatchedTask.ID, want.ID)
	}
	if got.DispatchedTask.Spec != want.Spec {
		t.Errorf("TC-001: DispatchedTask.Spec = %q, want %q", got.DispatchedTask.Spec, want.Spec)
	}
	if got.DispatchedTask.Repo != want.Repo {
		t.Errorf("TC-001: DispatchedTask.Repo = %q, want %q", got.DispatchedTask.Repo, want.Repo)
	}
	// RecipeName must also be forwarded (unchanged from existing behaviour).
	if got.RecipeName != sub.RecipeName {
		t.Errorf("TC-001: RecipeName = %q, want %q", got.RecipeName, sub.RecipeName)
	}
}

// =============================================================================
// TC-003: blank/invalid dispatched task is a hard error, not a silent no-op
// =============================================================================

// TestTC119_03_BlankTaskIDIsHardError verifies that dispatching a SubGoal with
// a blank Task.ID returns a descriptive error and never calls runWorker.
func TestTC119_03_BlankTaskIDIsHardError(t *testing.T) {
	restore, records := tc119SpyRunWorker(t)
	defer restore()

	sigKey, workCache, resultCache := tc119KeyMaterial(t)
	dispatch, err := newTransportDispatch(sigKey, workCache, resultCache, audit.NewFakeSink(), discardLogger(), nil)
	if err != nil {
		t.Fatalf("TC-003 (blank ID): newTransportDispatch: %v", err)
	}

	sub := orchestrator.SubGoal{
		RecipeName: "coding-agent",
		Task:       supervisor.Task{ID: "", Spec: "something valid"},
	}
	base := runtimewiring.Config{TaskRoot: t.TempDir(), Worktree: t.TempDir()}

	dispatchErr := dispatch(context.Background(), sub, base)
	if dispatchErr == nil {
		t.Fatal("TC-003 (blank ID): expected error for blank Task.ID, got nil")
	}
	if !strings.Contains(strings.ToLower(dispatchErr.Error()), "blank") &&
		!strings.Contains(strings.ToLower(dispatchErr.Error()), "empty") {
		t.Errorf("TC-003 (blank ID): error %q does not describe the blank-ID condition", dispatchErr.Error())
	}
	if len(*records) != 0 {
		t.Errorf("TC-003 (blank ID): runWorker called %d times, want 0 (worker must not run on empty goal)", len(*records))
	}
}

// TestTC119_03_BlankTaskSpecIsHardError verifies that dispatching a SubGoal with
// a blank Task.Spec (but valid ID) returns a descriptive error and never calls
// runWorker.
func TestTC119_03_BlankTaskSpecIsHardError(t *testing.T) {
	restore, records := tc119SpyRunWorker(t)
	defer restore()

	sigKey, workCache, resultCache := tc119KeyMaterial(t)
	dispatch, err := newTransportDispatch(sigKey, workCache, resultCache, audit.NewFakeSink(), discardLogger(), nil)
	if err != nil {
		t.Fatalf("TC-003 (blank Spec): newTransportDispatch: %v", err)
	}

	sub := orchestrator.SubGoal{
		RecipeName: "coding-agent",
		Task:       supervisor.Task{ID: "goal-x", Spec: ""},
	}
	base := runtimewiring.Config{TaskRoot: t.TempDir(), Worktree: t.TempDir()}

	dispatchErr := dispatch(context.Background(), sub, base)
	if dispatchErr == nil {
		t.Fatal("TC-003 (blank Spec): expected error for blank Task.Spec, got nil")
	}
	if !strings.Contains(strings.ToLower(dispatchErr.Error()), "blank") &&
		!strings.Contains(strings.ToLower(dispatchErr.Error()), "empty") {
		t.Errorf("TC-003 (blank Spec): error %q does not describe the blank-Spec condition", dispatchErr.Error())
	}
	if len(*records) != 0 {
		t.Errorf("TC-003 (blank Spec): runWorker called %d times, want 0 (worker must not run on empty goal)", len(*records))
	}
}
