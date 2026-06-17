package runtime

// Tests for the typed audit-event projection added in task 041. The runtime
// in-box loop (retryingInBoxLoop.RunInside) projects each action-class lifecycle
// event through the optional audit.Sink in RunStreams ALONGSIDE the existing
// raw command/stdout/stderr stream. These tests drive RunInside directly with a
// FakeSink and fakes for the executor/gate/publisher/statusWriter seams so the
// projection is asserted without subprocesses.
//
// TC-041-01: every action-class lifecycle event is projected through the Sink in order.
// TC-041-02: raw stdout/stderr stay in the RunRecord; only typed action events reach the Sink.

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/gate"
	agentloop "github.com/tkdtaylor/agent-builder/internal/loop"
	branchpub "github.com/tkdtaylor/agent-builder/internal/publisher"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// --- fakes for the in-box loop seams ---

type fakeExecutor struct {
	result supervisor.Result
	err    error
}

func (f fakeExecutor) Run(supervisor.Task) (supervisor.Result, error) {
	return f.result, f.err
}

type fakeGate struct {
	verdict gate.Verdict
}

func (f fakeGate) Verify(string) gate.Verdict {
	return f.verdict
}

type fakeStatusWriter struct{}

func (fakeStatusWriter) WriteStatus(string, tasksource.WritableStatus) (tasksource.StatusWriteResult, error) {
	return tasksource.StatusWriteResult{}, nil
}

type fakePublisher struct {
	result branchpub.Result
	err    error
}

func (f fakePublisher) Publish(context.Context, branchpub.Request) (branchpub.Result, error) {
	return f.result, f.err
}

// newInBoxLoop builds a retryingInBoxLoop whose seams are all in-process fakes.
func newInBoxLoop(t *testing.T, exec supervisor.Executor, verdict gate.Verdict, pub branchpub.Publisher) retryingInBoxLoop {
	t.Helper()
	policy, err := agentloop.NewRetryPolicy(1, agentloop.BootstrapEscalationHook)
	if err != nil {
		t.Fatalf("setup retry policy: %v", err)
	}
	return retryingInBoxLoop{
		executor:      exec,
		gate:          fakeGate{verdict: verdict},
		worktree:      "/work/agent-builder",
		launcher:      "containment/execution-box/run.sh",
		statusWriter:  fakeStatusWriter{},
		policy:        policy,
		publisher:     pub,
		publishRemote: "origin",
	}
}

func passingVerdict() gate.Verdict {
	return gate.Verdict{OK: true, Results: []gate.StepResult{{Name: "go build ./...", OK: true}}}
}

// auditActions returns the ordered action list recorded by the FakeSink.
func auditActions(events []audit.AuditEvent) []audit.AuditAction {
	out := make([]audit.AuditAction, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Action)
	}
	return out
}

// TC-041-01: a successful run projects every action-class lifecycle event in order.
func TestSupervisorAuditProjectionSuccessOrder(t *testing.T) {
	sink := audit.NewFakeSink()
	loop := newInBoxLoop(t,
		fakeExecutor{result: supervisor.Result{Branch: "task/041-audit", OK: true}},
		passingVerdict(),
		fakePublisher{result: branchpub.Result{PRURL: "https://example.com/pr/41"}},
	)
	streams := supervisor.RunStreams{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Command: &bytes.Buffer{},
		Audit:   sink,
	}

	if err := loop.RunInside(supervisor.BoxHandle{ID: "box-041"}, supervisor.Task{ID: "041"}, streams); err != nil {
		t.Fatalf("TC-041-01: RunInside error = %v, want nil", err)
	}

	want := []audit.AuditAction{
		audit.ActionContainment,
		audit.ActionPick,
		audit.ActionAttempt,
		audit.ActionVerify,
		audit.ActionPublish,
		audit.ActionFinish,
	}
	got := auditActions(sink.Events())
	if len(got) != len(want) {
		t.Fatalf("TC-041-01: action sequence = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("TC-041-01: action[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}

	// The verify event carries the pass verdict; the finish event carries the completed outcome.
	for _, ev := range sink.Events() {
		switch ev.Action {
		case audit.ActionVerify:
			if ev.Verdict != audit.VerdictPass {
				t.Fatalf("TC-041-01: verify verdict = %q, want pass", ev.Verdict)
			}
		case audit.ActionFinish:
			if ev.Outcome != audit.OutcomeCompleted {
				t.Fatalf("TC-041-01: finish outcome = %q, want completed", ev.Outcome)
			}
		}
	}
	t.Logf("TC-041-01 success projection: %v", got)
}

// TC-041-01 (edge): an escalated run projects attempt(s)+escalate+finish(failed) and no publish.
func TestSupervisorAuditProjectionEscalatedNoPublish(t *testing.T) {
	sink := audit.NewFakeSink()
	// Executor fails (incomplete) so the gate is never reached and the loop escalates.
	loop := newInBoxLoop(t,
		fakeExecutor{result: supervisor.Result{Branch: "task/041-audit", OK: false}},
		passingVerdict(),
		fakePublisher{},
	)
	streams := supervisor.RunStreams{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Command: &bytes.Buffer{},
		Audit:   sink,
	}

	err := loop.RunInside(supervisor.BoxHandle{ID: "box-041"}, supervisor.Task{ID: "041"}, streams)
	if err == nil {
		t.Fatal("TC-041-01 escalate: RunInside error = nil, want escalation error")
	}

	got := auditActions(sink.Events())
	for _, a := range got {
		if a == audit.ActionPublish {
			t.Fatalf("TC-041-01 escalate: publish event present in %v, want none", got)
		}
	}
	if !containsAction(got, audit.ActionEscalate) {
		t.Fatalf("TC-041-01 escalate: missing escalate event in %v", got)
	}
	// The terminal finish must record the failed outcome.
	var sawFailedFinish bool
	for _, ev := range sink.Events() {
		if ev.Action == audit.ActionFinish && ev.Outcome == audit.OutcomeFailed {
			sawFailedFinish = true
		}
	}
	if !sawFailedFinish {
		t.Fatalf("TC-041-01 escalate: no finish event with outcome=failed in %#v", sink.Events())
	}
	t.Logf("TC-041-01 escalate projection: %v", got)
}

// TC-041-02: raw stdout/stderr/command stream lines stay in the RunRecord; the
// Sink receives only typed action events — no raw payload bytes.
func TestSupervisorAuditProjectionRawStaysInRecord(t *testing.T) {
	sink := audit.NewFakeSink()
	loop := newInBoxLoop(t,
		fakeExecutor{result: supervisor.Result{Branch: "task/041-audit", OK: true}},
		passingVerdict(),
		fakePublisher{result: branchpub.Result{PRURL: "https://example.com/pr/41"}},
	)
	var stdout, stderr, command bytes.Buffer
	streams := supervisor.RunStreams{
		Stdout:  &stdout,
		Stderr:  &stderr,
		Command: &command,
		Audit:   sink,
	}

	if err := loop.RunInside(supervisor.BoxHandle{ID: "box-041"}, supervisor.Task{ID: "041"}, streams); err != nil {
		t.Fatalf("TC-041-02: RunInside error = %v, want nil", err)
	}

	// The raw stream still carries the command/stdout lines (task 019/028 behavior).
	if !strings.Contains(command.String(), "pick task 041") {
		t.Fatalf("TC-041-02: command stream missing 'pick task 041': %q", command.String())
	}
	if !strings.Contains(stdout.String(), "task 041 selected") {
		t.Fatalf("TC-041-02: stdout stream missing 'task 041 selected': %q", stdout.String())
	}

	// The Sink holds only typed action events — no event carries raw stream text.
	raw := command.String() + stdout.String() + stderr.String()
	if strings.TrimSpace(raw) == "" {
		t.Fatal("TC-041-02: raw streams unexpectedly empty; cannot prove separation")
	}
	for _, ev := range sink.Events() {
		// A typed event's fields are enums/ids — never the free-form raw stream text.
		if strings.Contains(ev.TaskID, "selected") || strings.Contains(string(ev.Detail.Branch), "\n") {
			t.Fatalf("TC-041-02: audit event leaked raw stream payload: %#v", ev)
		}
	}
	// And every recorded event is a valid typed action (no raw "command" verbs).
	for _, ev := range sink.Events() {
		if !ev.Action.Valid() {
			t.Fatalf("TC-041-02: sink recorded a non-typed action %q", ev.Action)
		}
	}
	t.Logf("TC-041-02: %d typed audit events, raw stream %d bytes — separated", len(sink.Events()), len(raw))
}

// TC-041-02 (edge): when no Sink is configured (Audit == nil), the loop behaves
// exactly as before and the run still completes.
func TestSupervisorAuditProjectionNilSinkNoOp(t *testing.T) {
	loop := newInBoxLoop(t,
		fakeExecutor{result: supervisor.Result{Branch: "task/041-audit", OK: true}},
		passingVerdict(),
		fakePublisher{result: branchpub.Result{PRURL: "https://example.com/pr/41"}},
	)
	var command bytes.Buffer
	streams := supervisor.RunStreams{
		Stdout:  &bytes.Buffer{},
		Stderr:  &bytes.Buffer{},
		Command: &command,
		Audit:   nil, // no sink configured
	}

	if err := loop.RunInside(supervisor.BoxHandle{ID: "box-041"}, supervisor.Task{ID: "041"}, streams); err != nil {
		t.Fatalf("TC-041-02 nil sink: RunInside error = %v, want nil", err)
	}
	if !strings.Contains(command.String(), "finish task 041 outcome=completed") {
		t.Fatalf("TC-041-02 nil sink: command stream missing finish line: %q", command.String())
	}
}

func containsAction(actions []audit.AuditAction, want audit.AuditAction) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// ensure unused import guard for errors in case future edits drop its use.
var _ = errors.New
