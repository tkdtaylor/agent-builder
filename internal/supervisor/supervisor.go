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
	"strings"

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
	Teardown(BoxHandle) error
}

// InBoxLoop is the fakeable seam for one agent-loop run inside a created box.
type InBoxLoop interface {
	RunInside(BoxHandle, Task) error
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
// teardown. Retry, escalation, timeout, and run-record policy live in later
// tasks; this method only guarantees deterministic lifecycle ordering.
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

	defer func() {
		var panicErr error
		if recovered := recover(); recovered != nil {
			panicErr = fmt.Errorf("supervisor: run inside box panic: %v", recovered)
		}

		teardownErr := s.box.Teardown(handle)
		s.logLifecycle("box.torn_down", handle)
		if teardownErr != nil {
			teardownErr = fmt.Errorf("supervisor: teardown box: %w", teardownErr)
		}

		err = errors.Join(err, panicErr, teardownErr)
	}()

	s.logLifecycle("loop.started", handle)
	if loopErr := s.loop.RunInside(handle, s.task); loopErr != nil {
		err = fmt.Errorf("supervisor: run inside box: %w", loopErr)
	}
	return err
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
