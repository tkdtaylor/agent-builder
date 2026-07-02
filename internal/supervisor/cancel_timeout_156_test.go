package supervisor

// Task 156 — the wall-clock timeout arm (not only the cancel arm) terminates the
// in-flight executor, by threading a supervisor-derived cancellable CHILD context
// into the in-box loop.
//
// TC-156-01 (L2): Supervisor.Run derives a child context and passes THAT (not the
//   raw caller ctx) into InBoxLoop.RunInside; cancelling the parent cascades to the
//   child. This proves the context.WithCancel(ctx) parent/child wiring is genuine,
//   not a coincidental pass-through of the same object.
//
// The load-bearing L5 proof that a TIMEOUT (not a caller cancel) actually
// terminates a context-aware in-flight executor within a bounded time lives in
// internal/runtime/cancel_timeout_156_test.go (TC-156-02/03) — it needs the real
// unexported retryingInBoxLoop, which supervisor cannot import (fitness F-003).

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TC-156-01 — Run threads a derived child context (not the raw caller ctx) into
// the loop, and a caller-side cancel of the parent cascades to that child.
func TestTC156_01_RunThreadsDerivedChildContext(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{})
	box := &fakeBox{
		handle: BoxHandle{ID: "box-156-01", Worktree: "/work"},
		// Unblock the fake loop so killAndJoin completes once the cancel arm fires.
		onKill: func() { close(release) },
	}
	loop := &fakeInBoxLoop{
		blockUntil: release,
		duringRun:  func(RunStreams) error { close(entered); return nil },
	}

	callerCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- New(
			WithTask(Task{ID: "156"}),
			WithContainmentBox(box),
			WithInBoxLoop(loop),
		).Run(callerCtx)
	}()

	// Wait until the loop is genuinely in-flight so loop.ctxs[0] is populated.
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("TC-156-01: loop never entered — the chain did not reach RunInside")
	}

	loop.mu.Lock()
	if len(loop.ctxs) != 1 {
		loop.mu.Unlock()
		t.Fatalf("TC-156-01: loop received %d contexts, want 1", len(loop.ctxs))
	}
	recordedCtx := loop.ctxs[0]
	loop.mu.Unlock()

	// The loop must receive a DISTINCT context object (the derived child), not the
	// exact caller ctx — proving Supervisor.Run wrapped it in context.WithCancel.
	if recordedCtx == callerCtx {
		t.Fatal("TC-156-01: loop received the RAW caller ctx — Run did not derive a child context (context.WithCancel semantics not wired)")
	}
	// Before the caller cancels, the child is still live.
	if err := recordedCtx.Err(); err != nil {
		t.Fatalf("TC-156-01: derived child ctx.Err() = %v before cancel, want nil", err)
	}

	// A caller-side cancel of the PARENT must cascade to the derived child (this is
	// the "cancel arm gets the fix for free" property the timeout arm builds on).
	cancel()

	select {
	case <-recordedCtx.Done():
		// cascade observed
	case <-time.After(3 * time.Second):
		t.Fatal("TC-156-01: cancelling the parent did not cascade to the derived child ctx.Done() — child is not a descendant of the caller ctx")
	}
	if !errors.Is(recordedCtx.Err(), context.Canceled) {
		t.Fatalf("TC-156-01: derived child ctx.Err() = %v after parent cancel, want context.Canceled", recordedCtx.Err())
	}

	var err error
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("TC-156-01: Run did not return after cancel")
	}
	if !errors.Is(err, ErrRunCancelled) {
		t.Fatalf("TC-156-01: Run() error = %v, want ErrRunCancelled", err)
	}
}
