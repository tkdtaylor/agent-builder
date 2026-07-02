package loop_test

import (
	"context"
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

func TestRetryPolicyHonorsConfiguredAttemptLimit(t *testing.T) {
	for _, maxAttempts := range []int{1, 3} {
		t.Run("N="+itoa(maxAttempts), func(t *testing.T) {
			task := retryTask("013")
			source := &retrySource{tasks: []supervisor.Task{task}}
			executor := &sequenceExecutor{results: []supervisor.Result{{Branch: "task/013", OK: true}}}
			verifier := &retryGate{verdicts: []gate.Verdict{{OK: false}}}
			writer := newRecordingStatusWriter()
			policy := mustRetryPolicy(t, maxAttempts, agentloop.BootstrapEscalationHook)

			runner := mustRetryingLoop(t, source, executor, verifier, writer, policy)
			outcome, err := runner.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("TC-001 RunOnce() error = %v", err)
			}

			if outcome.Kind != agentloop.RetryOutcomeEscalated {
				t.Fatalf("TC-001 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeEscalated)
			}
			if executor.calls != maxAttempts {
				t.Fatalf("TC-001 executor calls = %d, want %d", executor.calls, maxAttempts)
			}
			if verifier.calls != maxAttempts {
				t.Fatalf("TC-001 gate calls = %d, want %d", verifier.calls, maxAttempts)
			}
			assertSingleNeedsHumanWrite(t, "TC-001", writer, task.ID)
		})
	}
}

// TC-155-05: RetryingLoop.RunOnce(ctx) threads the SAME ctx into every bounded
// retry attempt's Executor.Run — not a fresh context.Background() per attempt.
func TestTC155_05_RetryingLoopForwardsSameContextEveryAttempt(t *testing.T) {
	type ctxKey struct{}
	const marker = "marker-155-05"

	task := retryTask("155")
	source := &retrySource{tasks: []supervisor.Task{task}}
	// OK:true so the gate is reached; gate fails every attempt => retry to the cap.
	executor := &sequenceExecutor{results: []supervisor.Result{{Branch: "task/155", OK: true}}}
	verifier := &retryGate{verdicts: []gate.Verdict{{OK: false}}}
	writer := newRecordingStatusWriter()
	policy := mustRetryPolicy(t, 3, agentloop.BootstrapEscalationHook)

	runner := mustRetryingLoop(t, source, executor, verifier, writer, policy)
	ctx := context.WithValue(context.Background(), ctxKey{}, marker)
	outcome, err := runner.RunOnce(ctx)
	if err != nil {
		t.Fatalf("TC-155-05 RunOnce() error = %v", err)
	}
	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-155-05 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}

	if len(executor.ctxs) != 3 {
		t.Fatalf("TC-155-05 executor ctx captures = %d, want 3 (one per bounded attempt)", len(executor.ctxs))
	}
	for i, c := range executor.ctxs {
		got, ok := c.Value(ctxKey{}).(string)
		if !ok || got != marker {
			t.Fatalf("TC-155-05 attempt %d ctx.Value = %v (ok=%v), want %q — retry rebuilt a context instead of threading the caller's", i+1, got, ok, marker)
		}
	}
}

func TestRetryPolicyZeroAndNegativeCountsAreExplicit(t *testing.T) {
	if _, err := agentloop.NewRetryPolicy(-1, agentloop.BootstrapEscalationHook); !errors.Is(err, agentloop.ErrNegativeMaxAttempts) {
		t.Fatalf("TC-001A NewRetryPolicy(-1) error = %v, want %v", err, agentloop.ErrNegativeMaxAttempts)
	}

	task := retryTask("013")
	source := &retrySource{tasks: []supervisor.Task{task}}
	executor := &sequenceExecutor{results: []supervisor.Result{{Branch: "task/013", OK: true}}}
	verifier := &retryGate{verdicts: []gate.Verdict{{OK: true}}}
	writer := newRecordingStatusWriter()
	hook := &recordingHook{}
	policy := mustRetryPolicy(t, 0, hook.Hook)

	runner := mustRetryingLoop(t, source, executor, verifier, writer, policy)
	outcome, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("TC-001A RunOnce() error = %v", err)
	}

	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-001A Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}
	if outcome.Attempts != 0 {
		t.Fatalf("TC-001A Attempts = %d, want 0", outcome.Attempts)
	}
	if executor.calls != 0 {
		t.Fatalf("TC-001A executor calls = %d, want 0", executor.calls)
	}
	if verifier.calls != 0 {
		t.Fatalf("TC-001A gate calls = %d, want 0", verifier.calls)
	}
	if len(hook.requests) != 0 {
		t.Fatalf("TC-001A hook calls = %d, want 0", len(hook.requests))
	}
	assertSingleNeedsHumanWrite(t, "TC-001A", writer, task.ID)
}

func TestRetryPolicyEscalatesAndAdvancesAfterNFailures(t *testing.T) {
	task := retryTask("013")
	source := &retrySource{tasks: []supervisor.Task{task}}
	executor := &sequenceExecutor{results: []supervisor.Result{{Branch: "task/013", OK: false}}}
	verifier := &retryGate{verdicts: []gate.Verdict{{OK: false}}}
	writer := newRecordingStatusWriter()
	policy := mustRetryPolicy(t, 3, agentloop.BootstrapEscalationHook)

	runner := mustRetryingLoop(t, source, executor, verifier, writer, policy)
	outcome, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("TC-002 RunOnce() error = %v", err)
	}

	if executor.calls != 3 {
		t.Fatalf("TC-002 executor calls = %d, want 3", executor.calls)
	}
	if verifier.calls != 0 {
		t.Fatalf("TC-002 gate calls = %d, want 0 for incomplete executor attempts", verifier.calls)
	}
	if outcome.Kind != agentloop.RetryOutcomeEscalated {
		t.Fatalf("TC-002 Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeEscalated)
	}
	if !outcome.Advanced || !outcome.Terminal {
		t.Fatalf("TC-002 Advanced/Terminal = %v/%v, want true/true", outcome.Advanced, outcome.Terminal)
	}
	assertSingleNeedsHumanWrite(t, "TC-002", writer, task.ID)
}

func TestRetryPolicySuccessBeforeLimitDoesNotEscalate(t *testing.T) {
	task := retryTask("013")
	source := &retrySource{tasks: []supervisor.Task{task}}
	first := &sequenceExecutor{results: []supervisor.Result{{Branch: "first", OK: false}}}
	second := &sequenceExecutor{results: []supervisor.Result{{Branch: "second", OK: true}}}
	verifier := &retryGate{verdicts: []gate.Verdict{{OK: true}}}
	writer := newRecordingStatusWriter()
	hook := &recordingHook{next: []supervisor.Executor{second}}
	policy := mustRetryPolicy(t, 3, hook.Hook)

	runner := mustRetryingLoop(t, source, first, verifier, writer, policy)
	outcome, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("TC-002A RunOnce() error = %v", err)
	}

	if outcome.Kind != agentloop.RetryOutcomeDone {
		t.Fatalf("TC-002A Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeDone)
	}
	if outcome.Attempts != 2 {
		t.Fatalf("TC-002A Attempts = %d, want 2", outcome.Attempts)
	}
	if first.calls != 1 || second.calls != 1 {
		t.Fatalf("TC-002A executor calls = first:%d second:%d, want 1/1", first.calls, second.calls)
	}
	if verifier.calls != 1 {
		t.Fatalf("TC-002A gate calls = %d, want 1", verifier.calls)
	}
	if outcome.Branch != "second" {
		t.Fatalf("TC-002A Branch = %q, want second", outcome.Branch)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("TC-002A status writes = %d, want 0", len(writer.writes))
	}
}

func TestRetryPolicyTerminatesAcrossFailingTasks(t *testing.T) {
	tasks := []supervisor.Task{retryTask("013"), retryTask("014")}
	writer := newRecordingStatusWriter()
	source := &retrySource{tasks: tasks, writer: writer}
	executor := &sequenceExecutor{results: []supervisor.Result{{Branch: "failed", OK: false}}}
	verifier := &retryGate{verdicts: []gate.Verdict{{OK: false}}}
	policy := mustRetryPolicy(t, 2, agentloop.BootstrapEscalationHook)

	runner := mustRetryingLoop(t, source, executor, verifier, writer, policy)
	maxAllowedAttempts := len(tasks) * policy.MaxAttempts
	for i := 0; i < len(tasks); i++ {
		outcome, err := runner.RunOnce(context.Background())
		if err != nil {
			t.Fatalf("TC-003 RunOnce() #%d error = %v", i+1, err)
		}
		if outcome.Kind != agentloop.RetryOutcomeEscalated {
			t.Fatalf("TC-003 RunOnce() #%d Kind = %q, want %q", i+1, outcome.Kind, agentloop.RetryOutcomeEscalated)
		}
		if executor.calls > maxAllowedAttempts {
			t.Fatalf("TC-003 executor calls = %d exceeded bound %d", executor.calls, maxAllowedAttempts)
		}
	}

	idle, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("TC-003 idle RunOnce() error = %v", err)
	}
	if idle.Kind != agentloop.RetryOutcomeIdle {
		t.Fatalf("TC-003 idle Kind = %q, want %q", idle.Kind, agentloop.RetryOutcomeIdle)
	}
	if executor.calls != maxAllowedAttempts {
		t.Fatalf("TC-003 executor calls = %d, want bounded total %d", executor.calls, maxAllowedAttempts)
	}
	for _, task := range tasks {
		if !writer.needsHuman[task.ID] {
			t.Fatalf("TC-003 task %s was not marked needs-human", task.ID)
		}
	}
}

func TestEscalationHookIsInvokedBetweenAttemptsOnly(t *testing.T) {
	task := retryTask("013")
	source := &retrySource{tasks: []supervisor.Task{task}}
	executor := &sequenceExecutor{results: []supervisor.Result{{Branch: "failed", OK: false}}}
	verifier := &retryGate{verdicts: []gate.Verdict{{OK: false}}}
	writer := newRecordingStatusWriter()
	hook := &recordingHook{}
	policy := mustRetryPolicy(t, 3, hook.Hook)

	runner := mustRetryingLoop(t, source, executor, verifier, writer, policy)
	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("TC-004 RunOnce() error = %v", err)
	}

	if len(hook.requests) != 2 {
		t.Fatalf("TC-004 hook calls = %d, want 2", len(hook.requests))
	}
	for i, request := range hook.requests {
		wantAttempt := i + 1
		if request.Attempt != wantAttempt {
			t.Fatalf("TC-004 hook request %d Attempt = %d, want %d", i, request.Attempt, wantAttempt)
		}
		if request.Task.ID != task.ID {
			t.Fatalf("TC-004 hook request %d Task.ID = %q, want %q", i, request.Task.ID, task.ID)
		}
		if request.Outcome.Kind != agentloop.OutcomeFail {
			t.Fatalf("TC-004 hook request %d Outcome.Kind = %q, want %q", i, request.Outcome.Kind, agentloop.OutcomeFail)
		}
		if request.CurrentExecutor != executor {
			t.Fatalf("TC-004 hook request %d CurrentExecutor did not receive bootstrap executor", i)
		}
	}
}

func TestEscalationHookReturnValueControlsNextExecutor(t *testing.T) {
	task := retryTask("013")
	source := &retrySource{tasks: []supervisor.Task{task}}
	first := &sequenceExecutor{results: []supervisor.Result{{Branch: "first", OK: false}}}
	second := &sequenceExecutor{results: []supervisor.Result{{Branch: "second", OK: true}}}
	verifier := &retryGate{verdicts: []gate.Verdict{{OK: true}}}
	writer := newRecordingStatusWriter()
	hook := &recordingHook{next: []supervisor.Executor{second}}
	policy := mustRetryPolicy(t, 2, hook.Hook)

	runner := mustRetryingLoop(t, source, first, verifier, writer, policy)
	outcome, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("TC-004A RunOnce() error = %v", err)
	}

	if first.calls != 1 {
		t.Fatalf("TC-004A first executor calls = %d, want 1", first.calls)
	}
	if second.calls != 1 {
		t.Fatalf("TC-004A replacement executor calls = %d, want 1", second.calls)
	}
	if outcome.Kind != agentloop.RetryOutcomeDone {
		t.Fatalf("TC-004A Kind = %q, want %q", outcome.Kind, agentloop.RetryOutcomeDone)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("TC-004A status writes = %d, want 0", len(writer.writes))
	}
}

func mustRetryPolicy(t *testing.T, maxAttempts int, hook agentloop.EscalationHook) agentloop.RetryPolicy {
	t.Helper()

	policy, err := agentloop.NewRetryPolicy(maxAttempts, hook)
	if err != nil {
		t.Fatalf("NewRetryPolicy() error = %v", err)
	}
	return policy
}

func mustRetryingLoop(
	t *testing.T,
	source agentloop.TaskSource,
	executor supervisor.Executor,
	verifier supervisor.Gate,
	writer agentloop.StatusWriter,
	policy agentloop.RetryPolicy,
) *agentloop.RetryingLoop {
	t.Helper()

	runner, err := agentloop.NewRetryingLoop(source, executor, verifier, "/tmp/target-worktree", writer, policy)
	if err != nil {
		t.Fatalf("NewRetryingLoop() error = %v", err)
	}
	return runner
}

func retryTask(id string) supervisor.Task {
	return supervisor.Task{
		ID:   id,
		Repo: "agent-builder",
		Spec: "docs/tasks/backlog/" + id + "-retry.md",
	}
}

type retrySource struct {
	tasks  []supervisor.Task
	writer *recordingStatusWriter
}

func (s *retrySource) Next() (supervisor.Task, bool, error) {
	for _, task := range s.tasks {
		if s.writer != nil && s.writer.needsHuman[task.ID] {
			continue
		}
		return task, true, nil
	}
	return supervisor.Task{}, false, nil
}

type sequenceExecutor struct {
	results []supervisor.Result
	errs    []error
	calls   int
	ctxs    []context.Context
}

func (e *sequenceExecutor) Run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	index := e.calls
	e.calls++
	e.ctxs = append(e.ctxs, ctx)
	if index < len(e.errs) && e.errs[index] != nil {
		return supervisor.Result{}, e.errs[index]
	}
	if len(e.results) == 0 {
		return supervisor.Result{Branch: "default", OK: false}, nil
	}
	if index >= len(e.results) {
		index = len(e.results) - 1
	}
	return e.results[index], nil
}

type retryGate struct {
	verdicts []gate.Verdict
	calls    int
}

func (g *retryGate) Verify(repoPath string) gate.Verdict {
	index := g.calls
	g.calls++
	if len(g.verdicts) == 0 {
		return gate.Verdict{OK: false}
	}
	if index >= len(g.verdicts) {
		index = len(g.verdicts) - 1
	}
	return g.verdicts[index]
}

type recordingHook struct {
	next     []supervisor.Executor
	requests []agentloop.EscalationRequest
}

func (h *recordingHook) Hook(request agentloop.EscalationRequest) (supervisor.Executor, error) {
	h.requests = append(h.requests, request)
	index := len(h.requests) - 1
	if index < len(h.next) && h.next[index] != nil {
		return h.next[index], nil
	}
	return request.CurrentExecutor, nil
}

type statusWrite struct {
	taskID string
	status tasksource.WritableStatus
}

type recordingStatusWriter struct {
	writes     []statusWrite
	needsHuman map[string]bool
}

func newRecordingStatusWriter() *recordingStatusWriter {
	return &recordingStatusWriter{
		needsHuman: map[string]bool{},
	}
}

func (w *recordingStatusWriter) WriteStatus(taskID string, status tasksource.WritableStatus) (tasksource.StatusWriteResult, error) {
	w.writes = append(w.writes, statusWrite{taskID: taskID, status: status})
	if status == tasksource.WritableStatusNeedsHuman {
		w.needsHuman[taskID] = true
	}
	return tasksource.StatusWriteResult{Path: "docs/tasks/backlog/" + taskID + "-retry.md", Changed: true}, nil
}

func assertSingleNeedsHumanWrite(t *testing.T, marker string, writer *recordingStatusWriter, taskID string) {
	t.Helper()

	if len(writer.writes) != 1 {
		t.Fatalf("%s status writes = %d, want 1", marker, len(writer.writes))
	}
	if writer.writes[0].taskID != taskID {
		t.Fatalf("%s status task ID = %q, want %q", marker, writer.writes[0].taskID, taskID)
	}
	if writer.writes[0].status != tasksource.WritableStatusNeedsHuman {
		t.Fatalf("%s status = %q, want %q", marker, writer.writes[0].status, tasksource.WritableStatusNeedsHuman)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 4)
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
