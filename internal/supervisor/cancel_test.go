package supervisor

// Tests for the run-loop cancellation arm (ADR 054 §5, task 116). A per-goal
// cancel context fires the run-loop's case <-ctx.Done(): arm, which reuses the SAME
// box.Kill/Teardown path the wall-clock timeout drives — no new teardown mechanism
// is invented. The wall-clock timeout remains the independent backstop.
//
//   - TC-116-02: a far-future timer + a ctx cancel → the run-loop selects ctx.Done(),
//     the stub box records Kill then Teardown exactly once each (the same path the
//     timeout uses), and Run returns ErrRunCancelled after teardown.
//   - TC-116-05: a cancel whose box.Kill (a partial-teardown failure: the box cannot
//     be confirmed torn down) errors → the kill error is errors.Join'd onto
//     ErrRunCancelled and surfaced (not swallowed); Teardown still runs (the
//     close-before-teardown defer is intact, so the timer-backstop ordering holds).

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// TC-116-02 — the ctx.Done() arm tears down the box via box.Kill/Teardown, the same
// path the wall-clock timeout uses. The timer is set far in the future so it cannot
// fire; only the cancel arm can trigger the teardown.
func TestTC116_02_CancelArmTearsDownBoxViaKillTeardown(t *testing.T) {
	var logs bytes.Buffer
	release := make(chan struct{})
	entered := make(chan struct{})
	callLog := []string{}
	var logMu sync.Mutex
	box := &fakeBox{
		handle:  BoxHandle{ID: "box-116", Worktree: "/work"},
		callLog: &callLog,
		logMu:   &logMu,
		// Unblock the fake loop so killAndJoin's <-loopResult read completes. In
		// THIS test that unblock is driven by the fake's onKill callback. In
		// PRODUCTION, termination is driven by the run-scoped context being
		// cancelled (task 155/156) — the in-flight executor observes ctx.Done() and
		// returns — NOT by sandboxBox.Kill, which is an intentional no-op (see
		// internal/runtime/run.go). The fake stands in for that cancellation here.
		onKill: func() { close(release) },
	}
	loop := &fakeInBoxLoop{
		callLog:    &callLog,
		logMu:      &logMu,
		blockUntil: release,
		// Signal that the loop is running (so the test cancels MID-RUN, deterministically
		// after the loop has recorded loop.run and before the kill arm fires).
		duringRun: func(RunStreams) error { close(entered); return nil },
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- New(
			WithTask(Task{ID: "116"}),
			WithContainmentBox(box),
			WithInBoxLoop(loop),
			WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
			// Timer far in the future so the timer arm cannot fire — only ctx.Done()
			// can trigger the kill/teardown.
			WithRunTimeout(time.Hour),
		).Run(ctx)
	}()

	// Wait until the loop is in-flight, THEN cancel → the run-loop's case <-ctx.Done():
	// arm fires while the worker is genuinely running (a running-worker cancellation,
	// observed not inferred).
	<-entered
	cancel()

	var err error
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("TC-116-02: Run did not return after cancel — the ctx.Done() arm did not fire")
	}

	if !errors.Is(err, ErrRunCancelled) {
		t.Fatalf("TC-116-02: Run() error = %v, want ErrRunCancelled", err)
	}
	if box.killCalls != 1 {
		t.Fatalf("TC-116-02: kill calls = %d, want exactly 1 (cancel must trigger the same kill path as timeout)", box.killCalls)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-116-02: teardown calls = %d, want exactly 1", box.teardownCalls)
	}
	// The SAME lifecycle order as the wall-clock timeout: create → loop → kill → teardown.
	wantLog := []string{"box.create", "loop.run", "box.kill", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-116-02: lifecycle call log = %v, want %v (kill before teardown, same path as timeout)", callLog, wantLog)
	}
	if !reflect.DeepEqual(box.killedHandles, []BoxHandle{box.handle}) {
		t.Fatalf("TC-116-02: killed handles = %+v, want [%+v]", box.killedHandles, box.handle)
	}
	// A distinct cancel-kill log event so an operator can tell a cancel teardown apart
	// from a wall-clock timeout.
	if logOutput := logs.String(); !strings.Contains(logOutput, "event=box.kill.cancel") {
		t.Fatalf("TC-116-02: cancel log missing event=box.kill.cancel in:\n%s", logOutput)
	}
}

// TC-116-05 — a cancel whose box.Kill errors (a partial-teardown failure: the box
// cannot be confirmed killed) joins the kill error onto ErrRunCancelled and surfaces
// it (not swallowed), with explicit "leaked box" + box ID language for the operator.
// Teardown still runs (the close-before-teardown defer is intact) so the run is not
// abandoned mid-flight. The wall-clock timeout remains configured and the loud
// cancel log fires, proving the cancel path (not timeout) triggered the kill.
func TestTC116_05_CancelKillErrorIsJoinedAndSurfaced(t *testing.T) {
	killErr := errors.New("kill failed: box not confirmed dead")
	release := make(chan struct{})
	callLog := []string{}
	var logMu sync.Mutex
	var logOutput strings.Builder
	box := &fakeBox{
		handle:  BoxHandle{ID: "box-116-leak", Worktree: "/work/116"},
		killErr: killErr,
		callLog: &callLog,
		logMu:   &logMu,
		// Unblock the fake loop so the join completes even on a kill error; the
		// supervisor still surfaces killErr as a leak. As with TC-116-02, the real
		// production unblock is the run-scoped context being cancelled (task
		// 155/156), not sandboxBox.Kill (a no-op) — the fake's onKill stands in for it.
		onKill: func() { close(release) },
	}
	loop := &fakeInBoxLoop{callLog: &callLog, logMu: &logMu, blockUntil: release}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- New(
			WithTask(Task{ID: "116"}),
			WithContainmentBox(box),
			WithInBoxLoop(loop),
			WithLogger(slog.New(slog.NewTextHandler(&logOutput, nil))),
			WithRunTimeout(time.Hour), // backstop still configured; cancel fires first
		).Run(ctx)
	}()
	cancel()

	var err error
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("TC-116-05: Run did not return after cancel")
	}

	if !errors.Is(err, ErrRunCancelled) {
		t.Fatalf("TC-116-05: Run() error = %v, want ErrRunCancelled", err)
	}
	// The kill error (the partial-teardown leak) is errors.Join'd, not swallowed.
	// Since it's wrapped with leak-message formatting, check that the original kill
	// error message is present in the error chain.
	if !strings.Contains(err.Error(), "kill failed: box not confirmed dead") {
		t.Fatalf("TC-116-05: Run() error chain missing original kill message: %v", err)
	}
	// The error message includes explicit box ID + worktree + "leaked" language so
	// the operator sees this is a security-relevant leak (the box holds an executor
	// token and was not confirmed torn down).
	errMsg := err.Error()
	if !strings.Contains(errMsg, "box-116-leak") {
		t.Fatalf("TC-116-05: kill error missing box ID in message: %s", errMsg)
	}
	if !strings.Contains(errMsg, "leaked") {
		t.Fatalf("TC-116-05: kill error missing 'leaked' language in message: %s", errMsg)
	}
	if !strings.Contains(errMsg, "operator attention required") {
		t.Fatalf("TC-116-05: kill error missing 'operator attention required' in message: %s", errMsg)
	}
	// Teardown still ran (close-before-teardown ordering intact, the timer backstop
	// path is not abandoned).
	if box.teardownCalls != 1 {
		t.Fatalf("TC-116-05: teardown calls = %d, want 1 (teardown must still run on a kill error)", box.teardownCalls)
	}
	// The wall-clock timer is NOT disabled by the cancel arm — both the timer and
	// cancel arm are independently valid triggers into killAndJoin. Verify by the
	// timeout being configured and not fired (cancel fired first).
	if box.killCalls != 1 {
		t.Fatalf("TC-116-05: kill calls = %d, want 1 (cancel must trigger kill)", box.killCalls)
	}
	// The loud cancel log fired (event=box.kill.cancel, not timeout).
	logStr := logOutput.String()
	if !strings.Contains(logStr, "event=box.kill.cancel") {
		t.Fatalf("TC-116-05: loud cancel log missing event=box.kill.cancel in:\n%s", logStr)
	}
}
