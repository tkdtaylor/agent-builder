package runtime

// Task 155 — context threading through the production in-box loop.
//
// TC-155-06 (L2): retryingInBoxLoop.RunInside forwards its RECEIVED ctx (not a
//   fresh context.Background()) into the agentloop it drives, so the ctx reaches
//   the executor.
// TC-155-07 (L5): the FULL production chain — supervisor.Supervisor.Run →
//   retryingInBoxLoop.RunInside → agentloop.RetryingLoop.RunOnce →
//   agentloop.Loop.RunOnce → Executor.Run — propagates a caller cancellation to
//   an in-flight executor within a bounded time. This is the load-bearing proof
//   that the plumbing is live end-to-end, not merely type-threaded.
//
// The test lives in package runtime (not internal/supervisor) because the real
// InBoxLoop implementation (retryingInBoxLoop) is unexported here; constructing
// the genuine chain requires access to it. It still wires a real
// supervisor.Supervisor, so no shortcut fake InBoxLoop stands in for production.

import (
	"context"
	"errors"
	"testing"
	"time"

	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

type ctxKey155 struct{}

// ctxRecordingExecutor records the ctx of each Run call (pointer receiver so the
// captures persist to the caller).
type ctxRecordingExecutor struct {
	result supervisor.Result
	ctxs   []context.Context
}

func (e *ctxRecordingExecutor) Run(ctx context.Context, _ supervisor.Task) (supervisor.Result, error) {
	e.ctxs = append(e.ctxs, ctx)
	return e.result, nil
}

// TC-155-06: retryingInBoxLoop.RunInside forwards its ctx to the executor.
func TestTC155_06_RetryingInBoxLoopForwardsContext(t *testing.T) {
	const marker = "marker-155-06"
	exec := &ctxRecordingExecutor{result: supervisor.Result{Branch: "task/155", OK: true}}
	loop := newInBoxLoop(t, exec, passingVerdict(), fakePublisher{})
	streams := supervisor.RunStreams{Stdout: discardWriter{}, Stderr: discardWriter{}, Command: discardWriter{}}

	ctx := context.WithValue(context.Background(), ctxKey155{}, marker)
	if err := loop.RunInside(ctx, supervisor.BoxHandle{ID: "box-155"}, supervisor.Task{ID: "155"}, streams); err != nil {
		t.Fatalf("TC-155-06: RunInside error = %v, want nil", err)
	}

	if len(exec.ctxs) != 1 {
		t.Fatalf("TC-155-06: executor call count = %d, want 1", len(exec.ctxs))
	}
	got, ok := exec.ctxs[0].Value(ctxKey155{}).(string)
	if !ok || got != marker {
		t.Fatalf("TC-155-06: executor received ctx.Value = %v (ok=%v), want %q — retryingInBoxLoop rebuilt context.Background()", got, ok, marker)
	}
}

// slowContextAwareExecutor blocks in Run until ctx is cancelled, records that it
// observed the cancellation, then (after a short deterministic delay so the
// supervisor's ctx.Done arm wins its select over the completing loop goroutine)
// returns ctx.Err(). A long fallback timeout is a test safety net: on pre-155
// code the executor would build its own context.Background(), never observe the
// caller's cancellation, and hit this fallback — making the test fail loudly
// instead of hanging.
type slowContextAwareExecutor struct {
	entered  chan struct{}
	observed chan struct{}
}

func (e *slowContextAwareExecutor) Run(ctx context.Context, _ supervisor.Task) (supervisor.Result, error) {
	close(e.entered)
	select {
	case <-ctx.Done():
		close(e.observed)
		// Small delay so the supervisor's <-ctx.Done() select arm reliably wins
		// over the loop goroutine completing, yielding a deterministic
		// ErrRunCancelled (task 116 kill path).
		time.Sleep(200 * time.Millisecond)
		return supervisor.Result{}, ctx.Err()
	case <-time.After(10 * time.Second):
		return supervisor.Result{}, errors.New("slowContextAwareExecutor: cancellation never observed (fallback timeout) — ctx did not reach the executor")
	}
}

// ctx155Box is a minimal supervisor.ContainmentBox for the end-to-end harness.
type ctx155Box struct{}

func (ctx155Box) Create(t supervisor.Task) (supervisor.BoxHandle, error) {
	return supervisor.BoxHandle{ID: "box-" + t.ID, Worktree: "/work"}, nil
}
func (ctx155Box) Kill(supervisor.BoxHandle) error     { return nil }
func (ctx155Box) Teardown(supervisor.BoxHandle) error { return nil }

// TC-155-07: end-to-end cancellation through the real production chain.
func TestTC155_07_EndToEndCancellationReachesExecutor(t *testing.T) {
	slow := &slowContextAwareExecutor{
		entered:  make(chan struct{}),
		observed: make(chan struct{}),
	}
	task := supervisor.Task{ID: "155", Repo: "agent-builder", Spec: "docs/tasks/backlog/155-executor-context-threading.md"}

	// The REAL production in-box loop (retryingInBoxLoop), wired with the real
	// agentloop.RetryingLoop via its policy, driving the slow executor.
	policy, err := agentloop.NewRetryPolicy(1, agentloop.BootstrapEscalationHook)
	if err != nil {
		t.Fatalf("TC-155-07: retry policy: %v", err)
	}
	inBox := retryingInBoxLoop{
		executor:      slow,
		gate:          fakeGate{verdict: passingVerdict()},
		worktree:      "/work/agent-builder",
		launcher:      "containment/execution-box/run.sh",
		statusWriter:  fakeStatusWriter{},
		policy:        policy,
		publisher:     fakePublisher{},
		publishRemote: "origin",
	}

	sup := supervisor.New(
		supervisor.WithTask(task),
		supervisor.WithContainmentBox(ctx155Box{}),
		supervisor.WithInBoxLoop(inBox),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErr := make(chan error, 1)
	go func() { runErr <- sup.Run(ctx) }()

	// Wait until the executor is actually mid-Run before cancelling, proving the
	// cancellation lands on an IN-FLIGHT executor call.
	select {
	case <-slow.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("TC-155-07: executor Run was never entered — chain did not reach the executor")
	}

	cancel()

	// The executor must OBSERVE the cancellation (the load-bearing REQ-155-05 proof).
	select {
	case <-slow.observed:
	case <-time.After(5 * time.Second):
		t.Fatal("TC-155-07: executor never observed ctx.Done() — cancellation did not propagate through the production chain")
	}

	// And Supervisor.Run must return promptly with ErrRunCancelled.
	select {
	case err := <-runErr:
		if !errors.Is(err, supervisor.ErrRunCancelled) {
			t.Fatalf("TC-155-07: Run() error = %v, want errors.Is(err, supervisor.ErrRunCancelled)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("TC-155-07: Supervisor.Run did not return within 5s of cancellation")
	}
}

// discardWriter is an io.Writer that discards everything (avoids importing io in
// several call sites; RunStreams needs non-nil writers or defaults to discard).
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
