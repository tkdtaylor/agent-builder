// Package loop drives the inside-the-box pick -> attempt -> verify cycle.
package loop

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

var (
	// ErrNilTaskSource means New was called without a task source.
	ErrNilTaskSource = errors.New("loop: nil task source")

	// ErrNilExecutor means New was called without an executor.
	ErrNilExecutor = errors.New("loop: nil executor")

	// ErrNilGate means New was called without a verification gate.
	ErrNilGate = errors.New("loop: nil gate")

	// ErrBlankWorktreePath means New was called without the target repo path.
	ErrBlankWorktreePath = errors.New("loop: blank worktree path")
)

// TaskSource is the read side of roadmap/task selection consumed by the loop.
type TaskSource interface {
	Next() (supervisor.Task, bool, error)
}

// State is an observable state in one agent loop cycle.
type State string

const (
	StatePick    State = "pick"
	StateAttempt State = "attempt"
	StateVerify  State = "verify"
	StateAdvance State = "advance"
)

// OutcomeKind identifies the result of one loop cycle.
type OutcomeKind string

const (
	OutcomeIdle OutcomeKind = "idle"
	OutcomeDone OutcomeKind = "done"
	OutcomeFail OutcomeKind = "fail"
)

// FailureReason identifies which part of the cycle produced a fail outcome.
type FailureReason string

const (
	FailureExecutorError      FailureReason = "executor-error"
	FailureExecutorIncomplete FailureReason = "executor-incomplete"
	FailureGate               FailureReason = "gate-fail"
)

// Failure captures policy-free diagnostics for an OutcomeFail.
type Failure struct {
	Reason FailureReason
	Err    error
	// Blocked carries the denied resource/action + reason when Reason ==
	// FailureBlockedAction (ADR 055 seam 4, task 121). It is nil for every other
	// FailureReason. A blocked-action failure is distinct from FailureGate and
	// FailureExecutorError: it routes to bounded reevaluation + independent human
	// escalation, never to a self-grant.
	Blocked *BlockedAction
}

// Outcome is the observable result of one loop cycle.
type Outcome struct {
	Kind    OutcomeKind
	Task    supervisor.Task
	Branch  string
	Verdict gate.Verdict
	Failure Failure
	Trace   []State
}

// Loop runs one task through task selection, executor attempt, and gate verify.
type Loop struct {
	source       TaskSource
	executor     supervisor.Executor
	gate         supervisor.Gate
	worktreePath string
}

// New constructs a Loop from its stable seams.
func New(source TaskSource, executor supervisor.Executor, verifier supervisor.Gate, worktreePath string) (*Loop, error) {
	if source == nil {
		return nil, ErrNilTaskSource
	}
	if executor == nil {
		return nil, ErrNilExecutor
	}
	if verifier == nil {
		return nil, ErrNilGate
	}
	if strings.TrimSpace(worktreePath) == "" {
		return nil, ErrBlankWorktreePath
	}

	return &Loop{
		source:       source,
		executor:     executor,
		gate:         verifier,
		worktreePath: worktreePath,
	}, nil
}

// RunOnce executes one pick -> attempt -> verify cycle.
func (l *Loop) RunOnce() (Outcome, error) {
	trace := []State{StatePick}

	task, ok, err := l.source.Next()
	if err != nil {
		return Outcome{Trace: copyTrace(trace)}, fmt.Errorf("loop: pick next task: %w", err)
	}
	if !ok {
		return Outcome{
			Kind:  OutcomeIdle,
			Trace: copyTrace(trace),
		}, nil
	}

	trace = append(trace, StateAttempt)
	result, err := l.executor.Run(task)
	if err != nil {
		return Outcome{
			Kind:    OutcomeFail,
			Task:    task,
			Failure: Failure{Reason: FailureExecutorError, Err: err},
			Trace:   copyTrace(trace),
		}, nil
	}
	if !result.OK {
		return Outcome{
			Kind:    OutcomeFail,
			Task:    task,
			Branch:  result.Branch,
			Failure: Failure{Reason: FailureExecutorIncomplete},
			Trace:   copyTrace(trace),
		}, nil
	}

	trace = append(trace, StateVerify)
	verdict := l.gate.Verify(l.worktreePath)
	if !verdict.OK {
		return Outcome{
			Kind:    OutcomeFail,
			Task:    task,
			Branch:  result.Branch,
			Verdict: verdict,
			Failure: Failure{Reason: FailureGate},
			Trace:   copyTrace(trace),
		}, nil
	}

	trace = append(trace, StateAdvance)
	return Outcome{
		Kind:    OutcomeDone,
		Task:    task,
		Branch:  result.Branch,
		Verdict: verdict,
		Trace:   copyTrace(trace),
	}, nil
}

func copyTrace(trace []State) []State {
	return append([]State(nil), trace...)
}
