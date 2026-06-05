package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestRunTimeoutUsesConfiguredDeadlineAndKillsBox(t *testing.T) {
	var logs bytes.Buffer
	release := make(chan struct{})
	callLog := []string{}
	box := &fakeBox{
		handle:  BoxHandle{ID: "box-018", Worktree: "/work"},
		callLog: &callLog,
		onKill: func() {
			close(release)
		},
	}
	loop := &fakeInBoxLoop{callLog: &callLog, blockUntil: release}
	start := time.Now()

	err := New(
		WithTask(Task{ID: "018"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
		WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
		WithRunTimeout(25*time.Millisecond),
	).Run()
	elapsed := time.Since(start)
	if !errors.Is(err, ErrRunTimedOut) {
		t.Fatalf("TC-001-Configurable-Timeout: Run() error = %v, want ErrRunTimedOut", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("TC-001-Configurable-Timeout: elapsed = %s, want configured timeout to fire promptly", elapsed)
	}
	if box.killCalls != 1 {
		t.Fatalf("TC-001-Configurable-Timeout: kill calls = %d, want 1", box.killCalls)
	}
	if !reflect.DeepEqual(box.killedHandles, []BoxHandle{box.handle}) {
		t.Fatalf("TC-001-Configurable-Timeout: killed handles = %+v, want [%+v]", box.killedHandles, box.handle)
	}
	wantLog := []string{"box.create", "loop.run", "box.kill", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-002-Timeout-Kills-Box-And-Tears-Down: lifecycle call log = %v, want %v", callLog, wantLog)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-002-Timeout-Kills-Box-And-Tears-Down: teardown calls = %d, want 1", box.teardownCalls)
	}
	if !reflect.DeepEqual(box.handles, []BoxHandle{box.handle}) {
		t.Fatalf("TC-002-Timeout-Kills-Box-And-Tears-Down: teardown handles = %+v, want [%+v]", box.handles, box.handle)
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, "event=box.kill.timeout") {
		t.Fatalf("TC-002-Timeout-Kills-Box-And-Tears-Down: timeout log missing box.kill.timeout in:\n%s", logOutput)
	}
	t.Logf("TC-002-Timeout-Kills-Box-And-Tears-Down timeout log:\n%s", logOutput)
}

func TestRunTimeoutRecordsTimedOutOutcome(t *testing.T) {
	release := make(chan struct{})
	recordPath := filepath.Join(t.TempDir(), "run-record.ndjson")
	box := &fakeBox{
		handle: BoxHandle{ID: "box-018", Worktree: "/work"},
		onKill: func() {
			close(release)
		},
		onTeardown: func() error {
			content, err := os.ReadFile(recordPath)
			if err != nil {
				return err
			}
			if !strings.Contains(string(content), `"outcome":"timed-out"`) {
				return errors.New("timed-out record was not flushed before teardown")
			}
			return nil
		},
	}
	loop := &fakeInBoxLoop{
		blockUntil: release,
		duringRun: func(streams RunStreams) error {
			if _, err := streams.Stdout.Write([]byte("before timeout\n")); err != nil {
				return err
			}
			return nil
		},
	}

	err := New(
		WithTask(Task{ID: "018"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
		WithRunRecordPath(recordPath),
		WithRunTimeout(25*time.Millisecond),
	).Run()
	if !errors.Is(err, ErrRunTimedOut) {
		t.Fatalf("TC-003-RunRecord-Timed-Out: Run() error = %v, want ErrRunTimedOut", err)
	}

	events := readInternalRunRecord(t, recordPath)
	last := events[len(events)-1]
	if got := last["type"]; got != "run_finished" {
		t.Fatalf("TC-003-RunRecord-Timed-Out: final event type = %v, want run_finished", got)
	}
	if got := last["outcome"]; got != string(RunOutcomeTimedOut) {
		t.Fatalf("TC-003-RunRecord-Timed-Out: outcome = %v, want %s", got, RunOutcomeTimedOut)
	}
	if !strings.Contains(asInternalString(last["error"]), ErrRunTimedOut.Error()) {
		t.Fatalf("TC-003-RunRecord-Timed-Out: error = %v, want timeout message", last["error"])
	}
	assertInternalRecordContains(t, "TC-003-RunRecord-Timed-Out", events, "stdout", "data", "before timeout\n")
	t.Logf("TC-003-RunRecord-Timed-Out terminal event: %s", lastInternalLine(t, recordPath))
}

func TestRunOutcomesDistinguishSuccessFailureAndTimeout(t *testing.T) {
	successRecord, successErr := runSupervisorRecord(t, "success", &fakeBox{handle: BoxHandle{ID: "box-success"}}, &fakeInBoxLoop{}, 0)
	if successErr != nil {
		t.Fatalf("TC-004-Outcome-Distinct: success Run() error = %v, want nil", successErr)
	}
	if got := finalOutcome(t, successRecord); got != string(RunOutcomeCompleted) {
		t.Fatalf("TC-004-Outcome-Distinct: success outcome = %q, want completed", got)
	}

	loopErr := errors.New("gate failed")
	failureBox := &fakeBox{handle: BoxHandle{ID: "box-failure"}}
	failureRecord, failureErr := runSupervisorRecord(t, "failure", failureBox, &fakeInBoxLoop{err: loopErr}, time.Second)
	if !errors.Is(failureErr, loopErr) {
		t.Fatalf("TC-004-Outcome-Distinct: failure Run() error = %v, want %v", failureErr, loopErr)
	}
	if errors.Is(failureErr, ErrRunTimedOut) {
		t.Fatalf("TC-004-Outcome-Distinct: fast loop error = %v, must not be ErrRunTimedOut", failureErr)
	}
	if failureBox.killCalls != 0 {
		t.Fatalf("TC-004-Outcome-Distinct: fast loop error kill calls = %d, want 0", failureBox.killCalls)
	}
	if got := finalOutcome(t, failureRecord); got != string(RunOutcomeFailed) {
		t.Fatalf("TC-004-Outcome-Distinct: failure outcome = %q, want failed", got)
	}

	release := make(chan struct{})
	timeoutBox := &fakeBox{
		handle: BoxHandle{ID: "box-timeout"},
		onKill: func() {
			close(release)
		},
	}
	timeoutRecord, timeoutErr := runSupervisorRecord(t, "timeout", timeoutBox, &fakeInBoxLoop{blockUntil: release}, 25*time.Millisecond)
	if !errors.Is(timeoutErr, ErrRunTimedOut) {
		t.Fatalf("TC-004-Outcome-Distinct: timeout Run() error = %v, want ErrRunTimedOut", timeoutErr)
	}
	if got := finalOutcome(t, timeoutRecord); got != string(RunOutcomeTimedOut) {
		t.Fatalf("TC-004-Outcome-Distinct: timeout outcome = %q, want timed-out", got)
	}
	if timeoutBox.killCalls != 1 {
		t.Fatalf("TC-004-Outcome-Distinct: timeout kill calls = %d, want 1", timeoutBox.killCalls)
	}
}

func TestRunWithoutTimeoutDoesNotKill(t *testing.T) {
	callLog := []string{}
	box := &fakeBox{handle: BoxHandle{ID: "box-018"}, callLog: &callLog}
	loop := &fakeInBoxLoop{callLog: &callLog}

	err := New(
		WithTask(Task{ID: "018"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
	).Run()
	if err != nil {
		t.Fatalf("TC-005-Unset-Timeout-No-Kill: Run() error = %v, want nil", err)
	}
	if box.killCalls != 0 {
		t.Fatalf("TC-005-Unset-Timeout-No-Kill: kill calls = %d, want 0", box.killCalls)
	}
	wantLog := []string{"box.create", "loop.run", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-005-Unset-Timeout-No-Kill: lifecycle call log = %v, want %v", callLog, wantLog)
	}
}

func TestRunTimeoutSurfacesKillErrorAndStillTearsDown(t *testing.T) {
	killErr := errors.New("kill failed")
	callLog := []string{}
	recordPath := filepath.Join(t.TempDir(), "kill-error.ndjson")
	box := &fakeBox{
		handle:  BoxHandle{ID: "box-018"},
		killErr: killErr,
		callLog: &callLog,
	}
	loop := &fakeInBoxLoop{blockUntil: make(chan struct{}), callLog: &callLog}

	err := New(
		WithTask(Task{ID: "018"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
		WithRunRecordPath(recordPath),
		WithRunTimeout(25*time.Millisecond),
	).Run()
	if !errors.Is(err, ErrRunTimedOut) {
		t.Fatalf("TC-006-Kill-Error-Still-Tears-Down: Run() error = %v, want ErrRunTimedOut", err)
	}
	if !errors.Is(err, killErr) {
		t.Fatalf("TC-006-Kill-Error-Still-Tears-Down: Run() error = %v, want kill error %v", err, killErr)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-006-Kill-Error-Still-Tears-Down: teardown calls = %d, want 1", box.teardownCalls)
	}
	wantLog := []string{"box.create", "loop.run", "box.kill", "box.teardown"}
	if !reflect.DeepEqual(callLog, wantLog) {
		t.Fatalf("TC-006-Kill-Error-Still-Tears-Down: lifecycle call log = %v, want %v", callLog, wantLog)
	}
	if got := finalOutcome(t, recordPath); got != string(RunOutcomeTimedOut) {
		t.Fatalf("TC-006-Kill-Error-Still-Tears-Down: run-record outcome = %q, want timed-out", got)
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
	killErr       error
	teardownErr   error
	onKill        func()
	onTeardown    func() error
	createCalls   int
	killCalls     int
	teardownCalls int
	tasks         []Task
	handles       []BoxHandle
	killedHandles []BoxHandle
	callLog       *[]string
}

func (b *fakeBox) Create(task Task) (BoxHandle, error) {
	b.createCalls++
	b.tasks = append(b.tasks, task)
	b.record("box.create")
	return b.handle, b.err
}

func (b *fakeBox) Kill(handle BoxHandle) error {
	b.killCalls++
	b.killedHandles = append(b.killedHandles, handle)
	b.record("box.kill")
	if b.onKill != nil {
		b.onKill()
	}
	return b.killErr
}

func (b *fakeBox) Teardown(handle BoxHandle) error {
	b.teardownCalls++
	b.handles = append(b.handles, handle)
	b.record("box.teardown")
	if b.onTeardown != nil {
		return errors.Join(b.onTeardown(), b.teardownErr)
	}
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
	blockUntil <-chan struct{}
	duringRun  func(RunStreams) error
	calls      int
	tasks      []Task
	handles    []BoxHandle
	callLog    *[]string
}

func (l *fakeInBoxLoop) RunInside(handle BoxHandle, task Task, streams RunStreams) error {
	l.calls++
	l.handles = append(l.handles, handle)
	l.tasks = append(l.tasks, task)
	if l.callLog != nil {
		*l.callLog = append(*l.callLog, "loop.run")
	}
	if l.duringRun != nil {
		if err := l.duringRun(streams); err != nil {
			return err
		}
	}
	if l.blockUntil != nil {
		<-l.blockUntil
	}
	if l.panicValue != nil {
		panic(l.panicValue)
	}
	return l.err
}

func runSupervisorRecord(t *testing.T, name string, box *fakeBox, loop *fakeInBoxLoop, timeout time.Duration) (string, error) {
	t.Helper()

	recordPath := filepath.Join(t.TempDir(), name+".ndjson")
	err := New(
		WithTask(Task{ID: "018"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
		WithRunRecordPath(recordPath),
		WithRunTimeout(timeout),
	).Run()
	return recordPath, err
}

func finalOutcome(t *testing.T, recordPath string) string {
	t.Helper()

	events := readInternalRunRecord(t, recordPath)
	return asInternalString(events[len(events)-1]["outcome"])
}

func readInternalRunRecord(t *testing.T, path string) []map[string]any {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run record %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("parse run record line %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func assertInternalRecordContains(t *testing.T, marker string, events []map[string]any, eventType, field, value string) {
	t.Helper()

	for _, event := range events {
		if event["type"] == eventType && event[field] == value {
			return
		}
	}
	t.Fatalf("%s: missing event type=%q %s=%q in %#v", marker, eventType, field, value, events)
}

func asInternalString(value any) string {
	text, _ := value.(string)
	return text
}

func lastInternalLine(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run record %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	return lines[len(lines)-1]
}
