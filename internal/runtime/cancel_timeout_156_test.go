package runtime

// Task 156 — the wall-clock TIMEOUT arm actually terminates an in-flight,
// context-aware executor (not only a caller cancel).
//
// TC-156-02 (L5): the FULL production chain — supervisor.Supervisor.Run →
//   retryingInBoxLoop.RunInside → agentloop.RetryingLoop.RunOnce → Executor.Run —
//   with an UN-cancelled caller ctx and a short WithRunTimeout. Only the wall-clock
//   timer can trigger termination. The load-bearing assertion is BOUNDED TIME:
//   Supervisor.Run must return close to the configured 50ms deadline, NOT after the
//   executor's long (10s) fallback safety timeout. Against the pre-156 code — where
//   the timeout arm cancels nothing downstream and killAndJoin blocks on
//   <-loopResult until the executor finishes on its own — this returns only after
//   ~10s and the < 3s assertion FAILS. That is the mutation-distinguishing proof
//   the dead wire is now live.
// TC-156-03 (L5): the executor's Run has observably returned by the time
//   Supervisor.Run returns — no leaked loop goroutine (killAndJoin's join is intact
//   for the timeout path).
//
// This lives in package runtime (not internal/supervisor) because the real
// InBoxLoop implementation (retryingInBoxLoop) is unexported here; supervisor
// cannot import runtime (fitness F-003). It wires a real supervisor.Supervisor and
// the real retryingInBoxLoop, so no shortcut fake stands in for production.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// timeoutAwareExecutor blocks in Run until its ctx is cancelled, records that it
// observed the cancellation and that it returned, then returns ctx.Err(). The long
// fallback is a safety net: on pre-156 code the timeout arm never cancels this ctx,
// so the executor would sit here until the fallback fires — making the bounded-time
// assertion fail loudly (a ~10s return) rather than hang forever.
type timeoutAwareExecutor struct {
	entered  chan struct{}
	observed chan struct{}
	returned atomic.Bool
}

func (e *timeoutAwareExecutor) Run(ctx context.Context, _ supervisor.Task) (supervisor.Result, error) {
	close(e.entered)
	defer e.returned.Store(true)
	select {
	case <-ctx.Done():
		close(e.observed)
		return supervisor.Result{}, ctx.Err()
	case <-time.After(10 * time.Second):
		return supervisor.Result{}, errors.New("timeoutAwareExecutor: ctx never cancelled (fallback) — the wall-clock timeout arm did not reach the executor")
	}
}

func newTimeoutHarness(t *testing.T, exec supervisor.Executor, runTimeout time.Duration) *supervisor.Supervisor {
	t.Helper()
	policy, err := agentloop.NewRetryPolicy(1, agentloop.BootstrapEscalationHook)
	if err != nil {
		t.Fatalf("retry policy: %v", err)
	}
	inBox := retryingInBoxLoop{
		executor:      exec,
		gate:          fakeGate{verdict: passingVerdict()},
		worktree:      "/work/agent-builder",
		launcher:      "containment/execution-box/run.sh",
		statusWriter:  fakeStatusWriter{},
		policy:        policy,
		publisher:     fakePublisher{},
		publishRemote: "origin",
	}
	return supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "156", Repo: "agent-builder", Spec: "docs/tasks/backlog/156-supervisor-cancel-timeout-stops-executor.md"}),
		supervisor.WithContainmentBox(ctx155Box{}),
		supervisor.WithInBoxLoop(inBox),
		supervisor.WithRunTimeout(runTimeout),
	)
}

// TC-156-02 / TC-156-03: a wall-clock timeout terminates the in-flight executor
// within a bounded time (not the long fallback), and the executor's Run has
// returned by the time Supervisor.Run returns.
func TestTC156_02_WallClockTimeoutTerminatesInFlightExecutor(t *testing.T) {
	const (
		runTimeout = 50 * time.Millisecond
		// Well under the executor's 10s fallback but generously above the 50ms
		// deadline + scheduling/teardown overhead. On pre-156 code Run returns only
		// after ~10s, so this bound is what makes the dead wire observable.
		bound = 3 * time.Second
	)
	exec := &timeoutAwareExecutor{
		entered:  make(chan struct{}),
		observed: make(chan struct{}),
	}
	sup := newTimeoutHarness(t, exec, runTimeout)

	// An UN-cancelled caller ctx: only the wall-clock timer can trigger termination.
	runErr := make(chan error, 1)
	start := time.Now()
	go func() { runErr <- sup.Run(context.Background()) }()

	// The executor must be genuinely in-flight before the timer fires.
	select {
	case <-exec.entered:
	case <-time.After(bound):
		t.Fatal("TC-156-02: executor Run was never entered — chain did not reach the executor")
	}

	// The load-bearing bounded-time assertion.
	var err error
	select {
	case err = <-runErr:
	case <-time.After(bound):
		t.Fatalf("TC-156-02: Supervisor.Run did not return within %s of a %s wall-clock timeout — the timeout arm did not cancel the in-flight executor (pre-156 cosmetic-timeout bug: it blocks on <-loopResult until the executor's own fallback fires)", bound, runTimeout)
	}
	elapsed := time.Since(start)
	if elapsed >= bound {
		t.Fatalf("TC-156-02: Supervisor.Run returned after %s (>= bound %s) — timeout did not promptly terminate the executor", elapsed, bound)
	}

	// The executor must have OBSERVED the timeout-driven cancellation (i.e. the
	// timeout arm cancelled the SAME ctx the executor watches), not returned via its
	// fallback.
	select {
	case <-exec.observed:
	case <-time.After(time.Second):
		t.Fatal("TC-156-02: executor never observed ctx.Done() — the wall-clock timeout did not cancel the run-scoped context the executor watches")
	}

	if !errors.Is(err, supervisor.ErrRunTimedOut) {
		t.Fatalf("TC-156-02: Run() error = %v, want errors.Is(err, supervisor.ErrRunTimedOut)", err)
	}
	// Note: the loop error IS joined onto the timeout error via killAndJoin, but the
	// agentloop retry layer reformats the executor's context.Canceled into an
	// "escalated after N attempts" string that does not preserve the errors.Is chain,
	// so we do not assert errors.Is(err, context.Canceled) here. The direct proof
	// that the executor observed the timeout-driven cancellation is the exec.observed
	// channel closing above — stronger and less coupled to error-string formatting.

	// TC-156-03: no leaked loop goroutine — the executor's Run has already returned
	// by the time Supervisor.Run returned (killAndJoin's join is intact for timeout).
	if !exec.returned.Load() {
		t.Fatal("TC-156-03: executor Run had not returned when Supervisor.Run returned — killAndJoin did not join the loop goroutine on the timeout path (leak)")
	}
}
