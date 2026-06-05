// Package supervisor is the trusted, outside-the-box control loop. It dispatches
// one task at a time to an executor, enforces the wall-clock/escalation kill, and
// tears the containment box down. It deliberately depends on no executor/LLM/web
// code (invariant F-003 in docs/spec/SPEC.md) so a hijacked agent inside the box
// can never reach back through it.
package supervisor

import (
	"errors"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

// Version is the current build version.
const Version = "0.0.0-scaffold"

// ErrNotImplemented marks seams that are stubbed during the Phase 0 bootstrap.
var ErrNotImplemented = errors.New("agent-builder: not implemented")

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

// Supervisor is the outside-the-box dispatcher.
//
// The default-deny egress allowlist — the load-bearing control for the accepted
// token-in-box risk (see docs/spec/configuration.md) — will be added here in the
// containment task (Phase 0.3), when something actually enforces it.
type Supervisor struct {
	sandboxRunner sandbox.Runner
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

// New returns a Supervisor with default (empty) configuration.
func New(options ...Option) *Supervisor {
	s := &Supervisor{}
	for _, option := range options {
		option(s)
	}
	return s
}

// Run is the outer loop: pick task -> create box -> run agent loop inside ->
// verify -> branch/PR or escalate -> teardown. Stubbed during Phase 0.
func (s *Supervisor) Run() error {
	return ErrNotImplemented
}
