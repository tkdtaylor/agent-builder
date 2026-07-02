package loop_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestRunOnceDoneOutcomeCarriesBranchAndTrace(t *testing.T) {
	task := supervisor.Task{ID: "012", Repo: "agent-builder", Spec: "docs/tasks/backlog/012-agent-loop.md"}
	source := &fakeSource{task: task, ok: true}
	executor := &fakeExecutor{result: supervisor.Result{Branch: "task/012-agent-loop", OK: true}}
	verifier := &fakeGate{verdict: gate.Verdict{OK: true}}

	cycle, err := agentloop.New(source, executor, verifier, "/tmp/target-worktree")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outcome, err := cycle.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	// TC-001: state transitions pick -> attempt -> verify -> advance are observable.
	wantTrace := []agentloop.State{
		agentloop.StatePick,
		agentloop.StateAttempt,
		agentloop.StateVerify,
		agentloop.StateAdvance,
	}
	if !reflect.DeepEqual(outcome.Trace, wantTrace) {
		t.Fatalf("Trace = %v, want %v", outcome.Trace, wantTrace)
	}
	if len(executor.tasks) != 1 || executor.tasks[0] != task {
		t.Fatalf("executor tasks = %+v, want [%+v]", executor.tasks, task)
	}
	if len(verifier.repoPaths) != 1 || verifier.repoPaths[0] != "/tmp/target-worktree" {
		t.Fatalf("gate repo paths = %v, want [/tmp/target-worktree]", verifier.repoPaths)
	}
	if outcome.Task.ID != task.ID {
		t.Fatalf("outcome Task.ID = %q, want %q", outcome.Task.ID, task.ID)
	}

	// TC-002: Gate pass produces a done outcome carrying the executor branch.
	if outcome.Kind != agentloop.OutcomeDone {
		t.Fatalf("Kind = %q, want %q", outcome.Kind, agentloop.OutcomeDone)
	}
	if outcome.Branch != "task/012-agent-loop" {
		t.Fatalf("Branch = %q, want task/012-agent-loop", outcome.Branch)
	}
	if !outcome.Verdict.OK {
		t.Fatal("Verdict.OK = false, want true")
	}
	assertNoRetryDecisionFields(t, outcome)
}

// TC-155-04: Loop.RunOnce(ctx) forwards the SAME ctx to Executor.Run.
func TestTC155_04_RunOnceForwardsContextToExecutor(t *testing.T) {
	type ctxKey struct{}
	const marker = "marker-155-04"

	task := supervisor.Task{ID: "155", Repo: "agent-builder", Spec: "docs/tasks/backlog/155-executor-context-threading.md"}
	source := &fakeSource{task: task, ok: true}
	executor := &fakeExecutor{result: supervisor.Result{Branch: "task/155", OK: true}}
	verifier := &fakeGate{verdict: gate.Verdict{OK: true}}

	cycle, err := agentloop.New(source, executor, verifier, "/tmp/target-worktree")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.WithValue(context.Background(), ctxKey{}, marker)
	if _, err := cycle.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	if len(executor.ctxs) != 1 {
		t.Fatalf("TC-155-04: executor call count = %d, want 1", len(executor.ctxs))
	}
	got, ok := executor.ctxs[0].Value(ctxKey{}).(string)
	if !ok || got != marker {
		t.Fatalf("TC-155-04: executor received ctx.Value = %v (ok=%v), want %q", got, ok, marker)
	}
}

func TestRunOnceGateFailureSuspendsForPolicy(t *testing.T) {
	task := supervisor.Task{ID: "012", Repo: "agent-builder", Spec: "docs/tasks/backlog/012-agent-loop.md"}
	source := &fakeSource{task: task, ok: true}
	executor := &fakeExecutor{result: supervisor.Result{Branch: "task/012-agent-loop", OK: true}}
	verifier := &fakeGate{verdict: gate.Verdict{
		OK: false,
		Results: []gate.StepResult{{
			Name:   "go test",
			OK:     false,
			Output: "failing test",
		}},
	}}

	cycle, err := agentloop.New(source, executor, verifier, "/tmp/target-worktree")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outcome, err := cycle.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	// TC-003: Gate fail emits a fail outcome and does not advance or decide retry policy.
	wantTrace := []agentloop.State{
		agentloop.StatePick,
		agentloop.StateAttempt,
		agentloop.StateVerify,
	}
	if !reflect.DeepEqual(outcome.Trace, wantTrace) {
		t.Fatalf("Trace = %v, want %v", outcome.Trace, wantTrace)
	}
	if outcome.Kind != agentloop.OutcomeFail {
		t.Fatalf("Kind = %q, want %q", outcome.Kind, agentloop.OutcomeFail)
	}
	if outcome.Failure.Reason != agentloop.FailureGate {
		t.Fatalf("Failure.Reason = %q, want %q", outcome.Failure.Reason, agentloop.FailureGate)
	}
	if outcome.Verdict.OK {
		t.Fatal("Verdict.OK = true, want false")
	}
	if outcome.Branch != "task/012-agent-loop" {
		t.Fatalf("Branch = %q, want task/012-agent-loop", outcome.Branch)
	}
	assertNoRetryDecisionFields(t, outcome)
}

func TestRunOnceIdleSkipsExecutorAndGate(t *testing.T) {
	source := &fakeSource{ok: false}
	executor := &fakeExecutor{}
	verifier := &fakeGate{}

	cycle, err := agentloop.New(source, executor, verifier, "/tmp/target-worktree")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outcome, err := cycle.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	// TC-004: no ready task idles after pick and performs no attempt or verify.
	wantTrace := []agentloop.State{agentloop.StatePick}
	if !reflect.DeepEqual(outcome.Trace, wantTrace) {
		t.Fatalf("Trace = %v, want %v", outcome.Trace, wantTrace)
	}
	if outcome.Kind != agentloop.OutcomeIdle {
		t.Fatalf("Kind = %q, want %q", outcome.Kind, agentloop.OutcomeIdle)
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls)
	}
	if verifier.calls != 0 {
		t.Fatalf("gate calls = %d, want 0", verifier.calls)
	}
	if outcome.Branch != "" {
		t.Fatalf("Branch = %q, want empty", outcome.Branch)
	}
}

func TestRunOnceExecutorErrorFailsBeforeVerify(t *testing.T) {
	task := supervisor.Task{ID: "012", Repo: "agent-builder", Spec: "docs/tasks/backlog/012-agent-loop.md"}
	execErr := errors.New("executor crashed")
	source := &fakeSource{task: task, ok: true}
	executor := &fakeExecutor{err: execErr}
	verifier := &fakeGate{verdict: gate.Verdict{OK: true}}

	cycle, err := agentloop.New(source, executor, verifier, "/tmp/target-worktree")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outcome, err := cycle.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}

	// TC-005: Executor error emits a fail outcome before verify and makes no retry decision.
	wantTrace := []agentloop.State{
		agentloop.StatePick,
		agentloop.StateAttempt,
	}
	if !reflect.DeepEqual(outcome.Trace, wantTrace) {
		t.Fatalf("Trace = %v, want %v", outcome.Trace, wantTrace)
	}
	if outcome.Kind != agentloop.OutcomeFail {
		t.Fatalf("Kind = %q, want %q", outcome.Kind, agentloop.OutcomeFail)
	}
	if outcome.Failure.Reason != agentloop.FailureExecutorError {
		t.Fatalf("Failure.Reason = %q, want %q", outcome.Failure.Reason, agentloop.FailureExecutorError)
	}
	if !errors.Is(outcome.Failure.Err, execErr) {
		t.Fatalf("Failure.Err = %v, want %v", outcome.Failure.Err, execErr)
	}
	if verifier.calls != 0 {
		t.Fatalf("gate calls = %d, want 0", verifier.calls)
	}
	if outcome.Branch != "" {
		t.Fatalf("Branch = %q, want empty", outcome.Branch)
	}
	assertNoRetryDecisionFields(t, outcome)
}

func TestRunOnceTaskSourceErrorReturnsBeforeAttempt(t *testing.T) {
	sourceErr := errors.New("read roadmap")
	source := &fakeSource{err: sourceErr}
	executor := &fakeExecutor{}
	verifier := &fakeGate{}

	cycle, err := agentloop.New(source, executor, verifier, "/tmp/target-worktree")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	outcome, err := cycle.RunOnce(context.Background())
	if err == nil {
		t.Fatal("RunOnce() error = nil")
	}
	if !errors.Is(err, sourceErr) {
		t.Fatalf("RunOnce() error = %v, want %v", err, sourceErr)
	}
	wantTrace := []agentloop.State{agentloop.StatePick}
	if !reflect.DeepEqual(outcome.Trace, wantTrace) {
		t.Fatalf("Trace = %v, want %v", outcome.Trace, wantTrace)
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls)
	}
	if verifier.calls != 0 {
		t.Fatalf("gate calls = %d, want 0", verifier.calls)
	}
}

func TestNewRejectsMissingDependencies(t *testing.T) {
	source := &fakeSource{}
	executor := &fakeExecutor{}
	verifier := &fakeGate{}

	tests := map[string]struct {
		source   agentloop.TaskSource
		exec     supervisor.Executor
		gate     supervisor.Gate
		worktree string
		wantErr  error
	}{
		"nil source": {
			source:   nil,
			exec:     executor,
			gate:     verifier,
			worktree: "/tmp/target-worktree",
			wantErr:  agentloop.ErrNilTaskSource,
		},
		"nil executor": {
			source:   source,
			exec:     nil,
			gate:     verifier,
			worktree: "/tmp/target-worktree",
			wantErr:  agentloop.ErrNilExecutor,
		},
		"nil gate": {
			source:   source,
			exec:     executor,
			gate:     nil,
			worktree: "/tmp/target-worktree",
			wantErr:  agentloop.ErrNilGate,
		},
		"blank worktree": {
			source:   source,
			exec:     executor,
			gate:     verifier,
			worktree: " ",
			wantErr:  agentloop.ErrBlankWorktreePath,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := agentloop.New(tc.source, tc.exec, tc.gate, tc.worktree)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func assertNoRetryDecisionFields(t *testing.T, outcome agentloop.Outcome) {
	t.Helper()

	outcomeType := reflect.TypeOf(outcome)
	for _, field := range []string{"RetryCount", "RetryDecision", "RetryLimit", "EscalationTarget"} {
		if _, ok := outcomeType.FieldByName(field); ok {
			t.Fatalf("Outcome unexpectedly exposes policy field %s", field)
		}
	}
}

type fakeSource struct {
	task  supervisor.Task
	ok    bool
	err   error
	calls int
}

func (s *fakeSource) Next() (supervisor.Task, bool, error) {
	s.calls++
	return s.task, s.ok, s.err
}

type fakeExecutor struct {
	result supervisor.Result
	err    error
	calls  int
	tasks  []supervisor.Task
	ctxs   []context.Context
}

func (e *fakeExecutor) Run(ctx context.Context, task supervisor.Task) (supervisor.Result, error) {
	e.calls++
	e.tasks = append(e.tasks, task)
	e.ctxs = append(e.ctxs, ctx)
	return e.result, e.err
}

type fakeGate struct {
	verdict   gate.Verdict
	calls     int
	repoPaths []string
}

func (g *fakeGate) Verify(repoPath string) gate.Verdict {
	g.calls++
	g.repoPaths = append(g.repoPaths, repoPath)
	return g.verdict
}
