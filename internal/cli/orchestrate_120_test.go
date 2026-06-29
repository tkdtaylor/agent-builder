package cli

// Tests for task 120: Propagate the worker's real result (ADR 055 seam 3).
//
// TC-001: successful run → OK true, reporter receives success report
// TC-002: failed run → OK false, error surfaced (not swallowed), reporter receives failure report naming sub-goal
// TC-003: idle/no-op run → NOT OK, reporter receives failure/not-done report

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// =============================================================================
// Test helpers
// =============================================================================

// recordingReporter records every Report call for assertion.
type recordingReporter struct {
	mu      sync.Mutex
	reports []string
}

func (r *recordingReporter) Report(_ context.Context, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reports = append(r.reports, text)
	return nil
}

func (r *recordingReporter) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.reports))
	copy(out, r.reports)
	return out
}

// tc120SpyRunWorker replaces runWorker for one test's duration and returns a
// given error on every call.
func tc120SpyRunWorker(t *testing.T, workerErr error) (restore func(), records *[]workerCallRecord) {
	t.Helper()
	rec := make([]workerCallRecord, 0, 1)
	prev := runWorker
	runWorker = func(_ context.Context, cfg runtimewiring.Config, _ io.Writer) error {
		rec = append(rec, workerCallRecord{cfg: cfg})
		return workerErr
	}
	return func() { runWorker = prev }, &rec
}

// tc120Sub builds a canonical SubGoal for TC-120 tests.
func tc120Sub() orchestrator.SubGoal {
	return orchestrator.SubGoal{
		RecipeName: "coding-agent",
		Task: supervisor.Task{
			ID:   "goal-120-sub0",
			Spec: "implement the feature",
			Repo: "exec-sandbox",
		},
	}
}

// tc120Base builds a base runtime.Config for TC-120 tests.
func tc120Base(t *testing.T) runtimewiring.Config {
	t.Helper()
	return runtimewiring.Config{
		TaskRoot: t.TempDir(),
		Worktree: t.TempDir(),
	}
}

// =============================================================================
// TC-001: successful run → OK true, reporter receives success report
// =============================================================================

// TestTC120_01_SuccessfulRunReportsOK verifies that when the worker runner
// returns nil (success), the dispatch:
//   - seals Result{OK: true} (the dispatch function returns nil, signalling success)
//   - the reporter receives a message identifying the sub-goal as completed
func TestTC120_01_SuccessfulRunReportsOK(t *testing.T) {
	restore, _ := tc120SpyRunWorker(t, nil) // spy returns nil = success
	defer restore()

	sigKey, workCache, resultCache := tc119KeyMaterial(t)
	rep := &recordingReporter{}

	dispatch, err := newTransportDispatch(sigKey, workCache, resultCache, audit.NewFakeSink(), discardLogger(), rep)
	if err != nil {
		t.Fatalf("TC-001: newTransportDispatch: %v", err)
	}

	sub := tc120Sub()
	base := tc120Base(t)

	dispatchErr := dispatch(context.Background(), sub, base)

	// TC-001: dispatch returns nil (OK == true signal to the orchestrator).
	if dispatchErr != nil {
		t.Errorf("TC-001: dispatch returned error %v, want nil (success must propagate as nil)", dispatchErr)
	}

	// TC-001: reporter receives a success message mentioning the sub-goal.
	reports := rep.all()
	if len(reports) == 0 {
		t.Fatal("TC-001: reporter received no reports; want a success report for the sub-goal")
	}
	last := reports[len(reports)-1]
	if !strings.Contains(last, sub.Task.ID) {
		t.Errorf("TC-001: success report %q does not mention sub-goal ID %q", last, sub.Task.ID)
	}
	// The report should indicate success/completion, not failure.
	lower := strings.ToLower(last)
	if strings.Contains(lower, "fail") || strings.Contains(lower, "error") {
		t.Errorf("TC-001: success report %q mentions failure; want a success/completion message", last)
	}
}

// =============================================================================
// TC-002: failed run → OK false, error surfaced, reporter receives failure report
// =============================================================================

// TestTC120_02_FailedRunReportsNotOK verifies that when the worker runner
// returns an error (gate/executor failure), the dispatch:
//   - seals Result{OK: false} (the dispatch function returns the error, signalling failure)
//   - does NOT swallow the error into a hardcoded OK response
//   - the reporter receives a failure message that names the sub-goal
func TestTC120_02_FailedRunReportsNotOK(t *testing.T) {
	gateFailErr := errors.New("gate failure: tests did not pass")
	restore, _ := tc120SpyRunWorker(t, gateFailErr)
	defer restore()

	sigKey, workCache, resultCache := tc119KeyMaterial(t)
	rep := &recordingReporter{}

	dispatch, err := newTransportDispatch(sigKey, workCache, resultCache, audit.NewFakeSink(), discardLogger(), rep)
	if err != nil {
		t.Fatalf("TC-002: newTransportDispatch: %v", err)
	}

	sub := tc120Sub()
	base := tc120Base(t)

	dispatchErr := dispatch(context.Background(), sub, base)

	// TC-002: dispatch must return non-nil error — the worker's failure must NOT be
	// swallowed and reported as OK.
	if dispatchErr == nil {
		t.Fatal("TC-002: dispatch returned nil (OK); a failed worker must NOT be reported as success")
	}

	// TC-002: the returned error must carry the worker's failure (error text surfaced).
	if !errors.Is(dispatchErr, gateFailErr) && !strings.Contains(dispatchErr.Error(), gateFailErr.Error()) {
		t.Errorf("TC-002: dispatch error %q does not carry the worker's gate failure %q", dispatchErr, gateFailErr)
	}

	// TC-002: reporter receives a failure message naming the sub-goal.
	reports := rep.all()
	if len(reports) == 0 {
		t.Fatal("TC-002: reporter received no reports; want a failure report for the sub-goal")
	}
	last := reports[len(reports)-1]
	if !strings.Contains(last, sub.Task.ID) {
		t.Errorf("TC-002: failure report %q does not mention sub-goal ID %q", last, sub.Task.ID)
	}
	// Report should signal failure.
	lower := strings.ToLower(last)
	if !strings.Contains(lower, "fail") && !strings.Contains(lower, "error") {
		t.Errorf("TC-002: failure report %q does not mention failure/error", last)
	}
}

// =============================================================================
// TC-003: idle/no-op run → NOT OK, reporter receives failure/not-done report
// =============================================================================

// TestTC120_03_IdleRunReportsNotDone verifies that when the worker runner
// returns an error simulating an idle/no-op run (no ready task), the dispatch:
//   - reports NOT OK (dispatch function returns the idle error)
//   - the reporter receives a failure/not-done message, not a success message
//   - the prior hardcoded Result{OK: true} would have masked this
func TestTC120_03_IdleRunReportsNotDone(t *testing.T) {
	// Simulate the "run idle: no ready task" outcome. In the real runtime, an idle
	// run in the dispatch context (DispatchedTask always set) should not occur; this
	// spy exercises the defensive path that the hardcoded OK:true previously masked.
	idleErr := errors.New("run idle: no ready task")
	restore, _ := tc120SpyRunWorker(t, idleErr)
	defer restore()

	sigKey, workCache, resultCache := tc119KeyMaterial(t)
	rep := &recordingReporter{}

	dispatch, err := newTransportDispatch(sigKey, workCache, resultCache, audit.NewFakeSink(), discardLogger(), rep)
	if err != nil {
		t.Fatalf("TC-003: newTransportDispatch: %v", err)
	}

	sub := tc120Sub()
	base := tc120Base(t)

	dispatchErr := dispatch(context.Background(), sub, base)

	// TC-003: dispatch must return non-nil error — an idle worker is NOT a success.
	// The prior hardcoded Result{OK:true} masked this: it reported OK even when
	// the worker did nothing. Now the dispatch propagates the real outcome.
	if dispatchErr == nil {
		t.Fatal("TC-003: dispatch returned nil (OK); an idle/no-op worker must NOT be reported as success (hardcoded OK:true masked this)")
	}

	// TC-003: the error carries the idle/no-op description.
	if !strings.Contains(dispatchErr.Error(), "idle") && !strings.Contains(dispatchErr.Error(), "no ready") {
		t.Errorf("TC-003: dispatch error %q does not describe the idle/no-op condition", dispatchErr)
	}

	// TC-003: reporter receives a failure/not-done message (NOT a success message).
	reports := rep.all()
	if len(reports) == 0 {
		t.Fatal("TC-003: reporter received no reports; want a not-done/failure report for the idle sub-goal")
	}
	last := reports[len(reports)-1]
	lower := strings.ToLower(last)
	// Must NOT claim success.
	if strings.Contains(lower, "completed") && !strings.Contains(lower, "fail") {
		t.Errorf("TC-003: idle report %q claims success; want a not-done/failure message", last)
	}
	// Must mention the sub-goal.
	if !strings.Contains(last, sub.Task.ID) {
		t.Errorf("TC-003: idle report %q does not mention sub-goal ID %q", last, sub.Task.ID)
	}
}
