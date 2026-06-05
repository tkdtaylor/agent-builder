// Package supervisor is the trusted, outside-the-box control loop. It dispatches
// one task at a time to an executor, enforces the wall-clock/escalation kill, and
// tears the containment box down. It deliberately depends on no executor/LLM/web
// code (invariant F-003 in docs/spec/SPEC.md) so a hijacked agent inside the box
// can never reach back through it.
package supervisor

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

// Version is the current build version.
const Version = "0.0.0-scaffold"

// ErrNotImplemented marks seams that are stubbed during the Phase 0 bootstrap.
var ErrNotImplemented = errors.New("agent-builder: not implemented")

var (
	// ErrNilContainmentBox means Run was called without the outside-the-box lifecycle seam.
	ErrNilContainmentBox = errors.New("supervisor: nil containment box")

	// ErrNilInBoxLoop means Run was called without the inside-the-box loop seam.
	ErrNilInBoxLoop = errors.New("supervisor: nil in-box loop")

	// ErrMissingTask means Run was called without the one task it must dispatch.
	ErrMissingTask = errors.New("supervisor: missing task")

	// ErrRunTimedOut means the in-box loop exceeded the configured wall-clock deadline.
	ErrRunTimedOut = errors.New("supervisor: run timed out")
)

// Task is one unit of work: build or modify exactly one target repo on its own
// branch. One task = one repo = one branch (no cross-repo sprawl).
type Task struct {
	ID   string // e.g. "001"
	Repo string // target block repo, e.g. "exec-sandbox"
	Spec string // path to the task spec the executor must satisfy
}

// Result is what an executor returns after attempting a Task.
type Result struct {
	Branch string // branch it produced
	OK     bool   // whether the executor believes it completed the task
}

// Executor is the pluggable brain seam: (harness, model) -> branch. Cloud CLIs
// (Claude Code, Gemini) bundle harness+model; local LLMs supply a harness. The
// router that picks an executor by quota/sensitivity/cost is a deferred v1 feature
// designed against this seam.
type Executor interface {
	Run(t Task) (Result, error)
}

// Gate is the machine-checkable definition of done (tests + build + lint +
// dep-scan/code-scanner). A Task is never "done" unattended unless Verify passes.
type Gate interface {
	Verify(repoPath string) gate.Verdict
}

// BoxHandle identifies a created containment box for one dispatched task.
type BoxHandle struct {
	ID       string
	Worktree string
}

// ContainmentBox is the fakeable outside-the-box lifecycle seam.
type ContainmentBox interface {
	Create(Task) (BoxHandle, error)
	Kill(BoxHandle) error
	Teardown(BoxHandle) error
}

// InBoxLoop is the fakeable seam for one agent-loop run inside a created box.
type InBoxLoop interface {
	RunInside(BoxHandle, Task, RunStreams) error
}

// Supervisor is the outside-the-box dispatcher.
//
// The default-deny egress allowlist — the load-bearing control for the accepted
// token-in-box risk (see docs/spec/configuration.md) — will be added here in the
// containment task (Phase 0.3), when something actually enforces it.
type Supervisor struct {
	sandboxRunner sandbox.Runner
	box           ContainmentBox
	loop          InBoxLoop
	task          Task
	logger        *slog.Logger
	runRecordPath string
	runTimeout    time.Duration
}

// Option configures a Supervisor.
type Option func(*Supervisor)

// WithSandboxRunner configures the exec-sandbox run adapter used for contained
// command execution.
func WithSandboxRunner(runner sandbox.Runner) Option {
	return func(s *Supervisor) {
		s.sandboxRunner = runner
	}
}

// WithContainmentBox configures the lifecycle seam that creates and tears down
// the ephemeral execution box for one dispatched task.
func WithContainmentBox(box ContainmentBox) Option {
	return func(s *Supervisor) {
		s.box = box
	}
}

// WithInBoxLoop configures the agent loop seam that runs inside the created box.
func WithInBoxLoop(loop InBoxLoop) Option {
	return func(s *Supervisor) {
		s.loop = loop
	}
}

// WithTask configures the single task dispatched by one Run call.
func WithTask(task Task) Option {
	return func(s *Supervisor) {
		s.task = task
	}
}

// WithLogger configures structured lifecycle logging for Run.
func WithLogger(logger *slog.Logger) Option {
	return func(s *Supervisor) {
		s.logger = logger
	}
}

// WithRunRecordPath configures a durable NDJSON run-record file for streamed
// in-box stdout, stderr, command events, and the terminal run outcome.
func WithRunRecordPath(path string) Option {
	return func(s *Supervisor) {
		s.runRecordPath = path
	}
}

// WithRunTimeout configures the wall-clock deadline for one in-box loop run.
// Non-positive durations leave the timeout disabled.
func WithRunTimeout(timeout time.Duration) Option {
	return func(s *Supervisor) {
		s.runTimeout = timeout
	}
}

// New returns a Supervisor with default (empty) configuration.
func New(options ...Option) *Supervisor {
	s := &Supervisor{
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, option := range options {
		option(s)
	}
	return s
}

// Run dispatches exactly one configured task through create -> run-inside ->
// teardown. Retry and escalation policy live in later tasks; this method
// guarantees deterministic lifecycle ordering, enforces the optional
// wall-clock kill, and, when configured, streams run output to a durable
// host-side run-record before teardown.
func (s *Supervisor) Run() (err error) {
	if s.box == nil {
		return ErrNilContainmentBox
	}
	if s.loop == nil {
		return ErrNilInBoxLoop
	}
	if strings.TrimSpace(s.task.ID) == "" {
		return ErrMissingTask
	}

	handle, err := s.box.Create(s.task)
	if err != nil {
		return fmt.Errorf("supervisor: create box: %w", err)
	}
	s.logLifecycle("box.created", handle)

	var record *RunRecordWriter
	outcome := RunOutcomeFailed

	defer func() {
		recordErr := s.closeRunRecord(record, outcome, err)

		teardownErr := s.box.Teardown(handle)
		s.logLifecycle("box.torn_down", handle)
		if teardownErr != nil {
			teardownErr = fmt.Errorf("supervisor: teardown box: %w", teardownErr)
		}

		err = errors.Join(err, recordErr, teardownErr)
	}()

	record, err = s.openRunRecord(handle)
	if err != nil {
		return err
	}
	streams := RunStreams{
		Stdout:  io.Discard,
		Stderr:  io.Discard,
		Command: io.Discard,
	}
	if record != nil {
		streams = record.Streams()
	}

	s.logLifecycle("loop.started", handle)
	if record != nil {
		if commandErr := record.Command("RunInside task " + s.task.ID); commandErr != nil {
			return fmt.Errorf("supervisor: write run command: %w", commandErr)
		}
	}
	loopResult := s.runLoop(handle, streams)
	if s.runTimeout > 0 {
		timer := time.NewTimer(s.runTimeout)
		defer timer.Stop()

		select {
		case result := <-loopResult:
			err = result.err
		case <-timer.C:
			outcome = RunOutcomeTimedOut
			timeoutErr := fmt.Errorf("%w after %s", ErrRunTimedOut, s.runTimeout)
			s.logTimeout(handle, timeoutErr)
			killErr := s.box.Kill(handle)
			if killErr != nil {
				killErr = fmt.Errorf("supervisor: kill box: %w", killErr)
				err = errors.Join(timeoutErr, killErr)
				return err
			}
			err = timeoutErr
			result := <-loopResult
			if result.err != nil {
				err = errors.Join(err, result.err)
			}
			return err
		}
	} else {
		result := <-loopResult
		err = result.err
	}

	if err == nil {
		outcome = RunOutcomeCompleted
	}
	return err
}

type loopRunResult struct {
	err error
}

func (s *Supervisor) runLoop(handle BoxHandle, streams RunStreams) <-chan loopRunResult {
	done := make(chan loopRunResult, 1)
	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("supervisor: run inside box panic: %v", recovered)
			}
			done <- loopRunResult{err: err}
		}()
		if loopErr := s.loop.RunInside(handle, s.task, streams); loopErr != nil {
			err = fmt.Errorf("supervisor: run inside box: %w", loopErr)
		}
	}()
	return done
}

func (s *Supervisor) logLifecycle(event string, handle BoxHandle) {
	if s.logger == nil {
		return
	}
	s.logger.Info("supervisor lifecycle",
		"event", event,
		"task_id", s.task.ID,
		"box_id", handle.ID,
		"worktree", handle.Worktree,
	)
}

func (s *Supervisor) logTimeout(handle BoxHandle, err error) {
	if s.logger == nil {
		return
	}
	s.logger.Error("supervisor timeout kill",
		"event", "box.kill.timeout",
		"task_id", s.task.ID,
		"box_id", handle.ID,
		"worktree", handle.Worktree,
		"error", err,
	)
}

func (s *Supervisor) openRunRecord(handle BoxHandle) (*RunRecordWriter, error) {
	if strings.TrimSpace(s.runRecordPath) == "" {
		return nil, nil
	}

	file, err := os.Create(s.runRecordPath)
	if err != nil {
		return nil, fmt.Errorf("supervisor: create run record: %w", err)
	}

	record := NewRunRecordWriter(file, RunRecordMetadata{
		RunID:    runID(s.task, handle),
		TaskID:   s.task.ID,
		Repo:     s.task.Repo,
		Spec:     s.task.Spec,
		BoxID:    handle.ID,
		Worktree: handle.Worktree,
	})
	if err := record.Start(); err != nil {
		closeErr := file.Close()
		return nil, errors.Join(fmt.Errorf("supervisor: start run record: %w", err), closeErr)
	}
	return record, nil
}

func (s *Supervisor) closeRunRecord(record *RunRecordWriter, outcome RunOutcome, runErr error) error {
	if record == nil {
		return nil
	}

	finishErr := record.Finish(outcome, runErr)
	if finishErr != nil {
		finishErr = fmt.Errorf("supervisor: finish run record: %w", finishErr)
	}
	closeErr := record.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("supervisor: close run record: %w", closeErr)
	}
	return errors.Join(finishErr, closeErr)
}

func runID(task Task, handle BoxHandle) string {
	parts := []string{}
	if strings.TrimSpace(task.ID) != "" {
		parts = append(parts, task.ID)
	}
	if strings.TrimSpace(handle.ID) != "" {
		parts = append(parts, handle.ID)
	}
	if len(parts) == 0 {
		return "run"
	}
	return strings.Join(parts, "/")
}
