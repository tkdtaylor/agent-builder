package supervisor

import (
	"bytes"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

func TestVersionSet(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must be set")
	}
}

func TestRunDispatchesOneTaskAndLogsLifecycle(t *testing.T) {
	var logs bytes.Buffer
	task := Task{ID: "017", Repo: "agent-builder", Spec: "docs/tasks/backlog/017-supervisor-dispatch.md"}
	callLog := []string{}
	box := &fakeBox{
		handle:  BoxHandle{ID: "box-017", Worktree: "/work/agent-builder"},
		callLog: &callLog,
	}
	loop := &fakeInBoxLoop{callLog: &callLog}

	err := New(
		WithTask(task),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
		WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
	).Run()
	if err != nil {
		t.Fatalf("TC-001: Run() error = %v, want nil", err)
	}

	wantLog := []string{"box.create", "loop.run", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-001: lifecycle call log = %v, want %v", callLog, wantLog)
	}
	if box.createCalls != 1 {
		t.Fatalf("TC-001: create calls = %d, want 1", box.createCalls)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-001: teardown calls = %d, want 1", box.teardownCalls)
	}
	if loop.calls != 1 {
		t.Fatalf("TC-001: loop calls = %d, want 1", loop.calls)
	}
	if gotTask := loop.tasks[0]; gotTask != task {
		t.Fatalf("TC-001: loop task = %+v, want %+v", gotTask, task)
	}

	logOutput := logs.String()
	t.Logf("TC-001 lifecycle logs:\n%s", logOutput)
	for _, want := range []string{"event=box.created", "event=loop.started", "event=box.torn_down", "task_id=017", "box_id=box-017"} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("TC-001: lifecycle logs missing %q in:\n%s", want, logOutput)
		}
	}
}

func TestRunPassesCreatedBoxToLoopBeforeTeardown(t *testing.T) {
	task := Task{ID: "017"}
	callLog := []string{}
	handle := BoxHandle{ID: "box-017", Worktree: "/work"}
	box := &fakeBox{
		handle:  handle,
		callLog: &callLog,
	}
	loop := &fakeInBoxLoop{callLog: &callLog}

	err := New(
		WithTask(task),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
	).Run()
	if err != nil {
		t.Fatalf("TC-002: Run() error = %v, want nil", err)
	}

	if !reflect.DeepEqual(loop.handles, []BoxHandle{handle}) {
		t.Fatalf("TC-002: loop handles = %+v, want [%+v]", loop.handles, handle)
	}
	wantLog := []string{"box.create", "loop.run", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-002: lifecycle call log = %v, want %v", callLog, wantLog)
	}
}

func TestRunTearsDownOnceOnLoopError(t *testing.T) {
	loopErr := errors.New("loop failed")
	callLog := []string{}
	box := &fakeBox{
		handle:  BoxHandle{ID: "box-017"},
		callLog: &callLog,
	}
	loop := &fakeInBoxLoop{
		err:     loopErr,
		callLog: &callLog,
	}

	err := New(
		WithTask(Task{ID: "017"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
	).Run()
	if !errors.Is(err, loopErr) {
		t.Fatalf("TC-003: Run() error = %v, want loop error %v", err, loopErr)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-003: teardown calls = %d, want 1", box.teardownCalls)
	}
	wantLog := []string{"box.create", "loop.run", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-003: lifecycle call log = %v, want %v", callLog, wantLog)
	}
}

func TestRunTearsDownOnceOnLoopPanic(t *testing.T) {
	callLog := []string{}
	box := &fakeBox{
		handle:  BoxHandle{ID: "box-017"},
		callLog: &callLog,
	}
	loop := &fakeInBoxLoop{
		panicValue: "loop panic",
		callLog:    &callLog,
	}

	err := New(
		WithTask(Task{ID: "017"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
	).Run()
	if err == nil {
		t.Fatal("TC-004: Run() error = nil, want panic recovery error")
	}
	if !strings.Contains(err.Error(), "loop panic") {
		t.Fatalf("TC-004: Run() error = %v, want panic value", err)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-004: teardown calls = %d, want 1", box.teardownCalls)
	}
	wantLog := []string{"box.create", "loop.run", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-004: lifecycle call log = %v, want %v", callLog, wantLog)
	}
}

func TestRunRejectsMissingDispatchDependencies(t *testing.T) {
	task := Task{ID: "017"}
	box := &fakeBox{handle: BoxHandle{ID: "box-017"}}
	loop := &fakeInBoxLoop{}

	tests := map[string]struct {
		options []Option
		wantErr error
	}{
		"nil containment box": {
			options: []Option{WithTask(task), WithInBoxLoop(loop)},
			wantErr: ErrNilContainmentBox,
		},
		"nil in-box loop": {
			options: []Option{WithTask(task), WithContainmentBox(box)},
			wantErr: ErrNilInBoxLoop,
		},
		"missing task": {
			options: []Option{WithContainmentBox(box), WithInBoxLoop(loop)},
			wantErr: ErrMissingTask,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if err := New(tc.options...).Run(); !errors.Is(err, tc.wantErr) {
				t.Fatalf("Run() error = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

type fakeBox struct {
	handle        BoxHandle
	err           error
	teardownErr   error
	createCalls   int
	teardownCalls int
	tasks         []Task
	handles       []BoxHandle
	callLog       *[]string
}

func (b *fakeBox) Create(task Task) (BoxHandle, error) {
	b.createCalls++
	b.tasks = append(b.tasks, task)
	b.record("box.create")
	return b.handle, b.err
}

func (b *fakeBox) Teardown(handle BoxHandle) error {
	b.teardownCalls++
	b.handles = append(b.handles, handle)
	b.record("box.teardown")
	return b.teardownErr
}

func (b *fakeBox) record(event string) {
	if b.callLog != nil {
		*b.callLog = append(*b.callLog, event)
	}
}

type fakeInBoxLoop struct {
	err        error
	panicValue any
	calls      int
	tasks      []Task
	handles    []BoxHandle
	callLog    *[]string
}

func (l *fakeInBoxLoop) RunInside(handle BoxHandle, task Task) error {
	l.calls++
	l.handles = append(l.handles, handle)
	l.tasks = append(l.tasks, task)
	if l.callLog != nil {
		*l.callLog = append(*l.callLog, "loop.run")
	}
	if l.panicValue != nil {
		panic(l.panicValue)
	}
	return l.err
}
