package supervisor_test

// TC-041-03: the supervisor Seals the audit sink BEFORE containment teardown on
// both the success and failure paths — the audit-chain analogue of the RunRecord
// close-before-teardown durability rule. A run with no sink behaves exactly as
// before.

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TC-041-06: wiring the audit sink does not widen the F-003 supervisor isolation
// boundary. The supervisor's transitive import graph gains internal/audit (a
// leaf) but no executor/LLM/web package. This is the existing-boundary guard;
// the dedicated fitness-audit-isolation (F-005) check is task 042.
func TestSupervisorAuditWiringDoesNotWidenF003Boundary(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./internal/supervisor/...")
	cmd.Dir = repoRoot(t) // resolve module-relative paths from the repo root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TC-041-06: go list -deps failed: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")

	var sawAudit bool
	for _, dep := range deps {
		if dep == "github.com/tkdtaylor/agent-builder/internal/audit" {
			sawAudit = true
		}
		for _, segment := range strings.Split(dep, "/") {
			switch segment {
			case "executor", "executors", "llm", "llms", "web", "webfetch", "web-fetch":
				t.Fatalf("TC-041-06: supervisor graph gained forbidden package %q via %q", segment, dep)
			}
		}
	}
	if !sawAudit {
		t.Fatal("TC-041-06: supervisor graph does not include internal/audit; the audit wiring is not actually in the supervisor's dependency graph")
	}
	t.Log("TC-041-06: supervisor graph includes internal/audit leaf, no executor/LLM/web package")
}

// auditFakeBox is a ContainmentBox that runs an onTeardown hook so a test can
// observe that the sink was already sealed when teardown fires.
type auditFakeBox struct {
	handle        supervisor.BoxHandle
	onTeardown    func() error
	teardownCalls int
}

func (b *auditFakeBox) Create(supervisor.Task) (supervisor.BoxHandle, error) {
	return b.handle, nil
}

func (b *auditFakeBox) Kill(supervisor.BoxHandle) error { return nil }

func (b *auditFakeBox) Teardown(supervisor.BoxHandle) error {
	b.teardownCalls++
	if b.onTeardown != nil {
		return b.onTeardown()
	}
	return nil
}

// auditEmittingLoop emits one finish event through the sink, mirroring the
// production loop's terminal projection, then returns the configured error.
type auditEmittingLoop struct {
	err error
}

func (l auditEmittingLoop) RunInside(_ supervisor.BoxHandle, task supervisor.Task, streams supervisor.RunStreams) error {
	if streams.Audit != nil {
		outcome := audit.OutcomeCompleted
		if l.err != nil {
			outcome = audit.OutcomeFailed
		}
		_ = streams.Audit.Append(audit.AuditEvent{Action: audit.ActionPick, TaskID: task.ID})
		_ = streams.Audit.Append(audit.AuditEvent{Action: audit.ActionFinish, TaskID: task.ID, Outcome: outcome})
	}
	return l.err
}

func TestSupervisorSealsSinkBeforeTeardownOnSuccess(t *testing.T) {
	sink := audit.NewFakeSink()
	box := &auditFakeBox{
		handle: supervisor.BoxHandle{ID: "box-041"},
		onTeardown: func() error {
			if !sink.Sealed() {
				return errors.New("sink was not sealed before teardown")
			}
			return nil
		},
	}

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "041"}),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(auditEmittingLoop{}),
		supervisor.WithSink(sink),
	).Run()
	if err != nil {
		t.Fatalf("TC-041-03: Run() error = %v, want nil", err)
	}
	if !sink.Sealed() {
		t.Fatal("TC-041-03: sink not sealed after run")
	}
	if sink.SealCount() != 1 {
		t.Fatalf("TC-041-03: seal count = %d, want 1", sink.SealCount())
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-041-03: teardown calls = %d, want 1", box.teardownCalls)
	}
	// The terminal finish must have been recorded before Seal closed the sink.
	events := sink.Events()
	if len(events) == 0 || events[len(events)-1].Action != audit.ActionFinish {
		t.Fatalf("TC-041-03: last event = %#v, want a finish event", events)
	}
	t.Logf("TC-041-03 success: sink sealed before teardown, %d events recorded", len(events))
}

func TestSupervisorSealsSinkBeforeTeardownOnFailure(t *testing.T) {
	loopErr := errors.New("loop failed")
	sink := audit.NewFakeSink()
	box := &auditFakeBox{
		handle: supervisor.BoxHandle{ID: "box-041"},
		onTeardown: func() error {
			if !sink.Sealed() {
				return errors.New("sink was not sealed before teardown on failure path")
			}
			return nil
		},
	}

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "041"}),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(auditEmittingLoop{err: loopErr}),
		supervisor.WithSink(sink),
	).Run()
	if !errors.Is(err, loopErr) {
		t.Fatalf("TC-041-03 failure: Run() error = %v, want loop error", err)
	}
	if !sink.Sealed() {
		t.Fatal("TC-041-03 failure: sink not sealed after failed run")
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-041-03 failure: teardown calls = %d, want 1", box.teardownCalls)
	}
	// On the failure path the finish event records the failed outcome.
	events := sink.Events()
	last := events[len(events)-1]
	if last.Action != audit.ActionFinish || last.Outcome != audit.OutcomeFailed {
		t.Fatalf("TC-041-03 failure: last event = %#v, want finish/failed", last)
	}
	t.Log("TC-041-03 failure: sink sealed before teardown on the failure path")
}

func TestSupervisorWithoutSinkBehavesAsBefore(t *testing.T) {
	box := &auditFakeBox{handle: supervisor.BoxHandle{ID: "box-041"}}

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "041"}),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(auditEmittingLoop{}),
	).Run()
	if err != nil {
		t.Fatalf("TC-041-03 no-sink: Run() error = %v, want nil", err)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-041-03 no-sink: teardown calls = %d, want 1", box.teardownCalls)
	}
}

// TC-041-03 (seal failure surfaces): a sink whose Seal returns an error joins
// that error into the run result so the failure is observable, not swallowed.
func TestSupervisorSurfacesSealError(t *testing.T) {
	sealErr := errors.New("seal failed")
	sink := &failingSealSink{err: sealErr}
	box := &auditFakeBox{handle: supervisor.BoxHandle{ID: "box-041"}}

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "041"}),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(auditEmittingLoop{}),
		supervisor.WithSink(sink),
	).Run()
	if !errors.Is(err, sealErr) {
		t.Fatalf("TC-041-03 seal-error: Run() error = %v, want seal error joined", err)
	}
}

type failingSealSink struct {
	err error
}

func (s *failingSealSink) Append(audit.AuditEvent) error { return nil }
func (s *failingSealSink) Seal() error                   { return s.err }
