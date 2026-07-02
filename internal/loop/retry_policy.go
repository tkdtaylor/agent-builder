package loop

import (
	"context"
	"errors"
	"fmt"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

var (
	// ErrNegativeMaxAttempts means the retry policy was configured below zero.
	ErrNegativeMaxAttempts = errors.New("loop: negative max attempts")

	// ErrNilStatusWriter means the retrying loop has no escalation status sink.
	ErrNilStatusWriter = errors.New("loop: nil status writer")

	// ErrNilEscalationHook means the retrying loop has no between-attempt hook.
	ErrNilEscalationHook = errors.New("loop: nil escalation hook")
)

// StatusWriter is the constrained task status writer seam used on escalation.
type StatusWriter interface {
	WriteStatus(taskID string, status tasksource.WritableStatus) (tasksource.StatusWriteResult, error)
}

// EscalationHook selects the Executor to use after a failed non-terminal attempt.
type EscalationHook func(EscalationRequest) (supervisor.Executor, error)

// EscalationRequest describes a failed attempt supplied to an EscalationHook.
type EscalationRequest struct {
	Task            supervisor.Task
	Attempt         int
	Outcome         Outcome
	CurrentExecutor supervisor.Executor
}

// BootstrapEscalationHook keeps bootstrap on the same Executor.
func BootstrapEscalationHook(request EscalationRequest) (supervisor.Executor, error) {
	return request.CurrentExecutor, nil
}

// RetryPolicy configures bounded retry and escalation behavior.
type RetryPolicy struct {
	MaxAttempts int
	Escalate    EscalationHook
}

// NewRetryPolicy validates and returns a retry policy.
func NewRetryPolicy(maxAttempts int, hook EscalationHook) (RetryPolicy, error) {
	policy := RetryPolicy{
		MaxAttempts: maxAttempts,
		Escalate:    hook,
	}
	if err := validateRetryPolicy(policy); err != nil {
		return RetryPolicy{}, err
	}
	return policy, nil
}

// RetryOutcomeKind identifies the result of a bounded retry cycle.
type RetryOutcomeKind string

const (
	RetryOutcomeIdle      RetryOutcomeKind = "idle"
	RetryOutcomeDone      RetryOutcomeKind = "done"
	RetryOutcomeEscalated RetryOutcomeKind = "escalated"
)

// RetryOutcome is the observable result of retrying one selected task.
type RetryOutcome struct {
	Kind        RetryOutcomeKind
	Task        supervisor.Task
	Branch      string
	Attempts    int
	LastOutcome Outcome
	StatusWrite tasksource.StatusWriteResult
	Advanced    bool
	Terminal    bool
}

// RetryingLoop applies a bounded retry/escalation policy around one task cycle.
type RetryingLoop struct {
	source       TaskSource
	executor     supervisor.Executor
	gate         supervisor.Gate
	worktreePath string
	statusWriter StatusWriter
	policy       RetryPolicy
}

// NewRetryingLoop constructs a bounded retry policy consumer from stable seams.
func NewRetryingLoop(
	source TaskSource,
	executor supervisor.Executor,
	verifier supervisor.Gate,
	worktreePath string,
	statusWriter StatusWriter,
	policy RetryPolicy,
) (*RetryingLoop, error) {
	if err := validateRetryPolicy(policy); err != nil {
		return nil, err
	}
	if statusWriter == nil {
		return nil, ErrNilStatusWriter
	}
	if _, err := New(source, executor, verifier, worktreePath); err != nil {
		return nil, err
	}

	return &RetryingLoop{
		source:       source,
		executor:     executor,
		gate:         verifier,
		worktreePath: worktreePath,
		statusWriter: statusWriter,
		policy:       policy,
	}, nil
}

// RunOnce picks one task and retries it up to MaxAttempts before escalation. ctx
// is the supervisor-threaded per-goal cancel context (task 155): the SAME ctx is
// forwarded to every bounded-retry attempt's Loop.RunOnce, so a caller
// cancellation reaches whichever attempt's executor is in-flight.
func (l *RetryingLoop) RunOnce(ctx context.Context) (RetryOutcome, error) {
	task, ok, err := l.source.Next()
	if err != nil {
		return RetryOutcome{}, fmt.Errorf("loop: pick next retry task: %w", err)
	}
	if !ok {
		return RetryOutcome{
			Kind:     RetryOutcomeIdle,
			Terminal: true,
		}, nil
	}

	if l.policy.MaxAttempts == 0 {
		return l.markNeedsHuman(task, 0, Outcome{})
	}

	currentExecutor := l.executor
	var last Outcome
	for attempt := 1; attempt <= l.policy.MaxAttempts; attempt++ {
		cycle, err := New(&singleTaskSource{task: task}, currentExecutor, l.gate, l.worktreePath)
		if err != nil {
			return RetryOutcome{}, err
		}

		outcome, err := cycle.RunOnce(ctx)
		if err != nil {
			return RetryOutcome{}, err
		}
		last = outcome

		switch outcome.Kind {
		case OutcomeDone:
			return RetryOutcome{
				Kind:        RetryOutcomeDone,
				Task:        task,
				Branch:      outcome.Branch,
				Attempts:    attempt,
				LastOutcome: outcome,
				Advanced:    true,
				Terminal:    true,
			}, nil
		case OutcomeFail:
			if attempt == l.policy.MaxAttempts {
				return l.markNeedsHuman(task, l.policy.MaxAttempts, last)
			}
			// Populate PriorFailure for the next attempt with formatted failure detail
			task.PriorFailure = FormatFailure(outcome)
		default:
			return RetryOutcome{}, fmt.Errorf("loop: retry attempt returned unexpected outcome %q", outcome.Kind)
		}

		nextExecutor, err := l.policy.Escalate(EscalationRequest{
			Task:            task,
			Attempt:         attempt,
			Outcome:         outcome,
			CurrentExecutor: currentExecutor,
		})
		if err != nil {
			return RetryOutcome{}, fmt.Errorf("loop: escalation hook after attempt %d: %w", attempt, err)
		}
		if nextExecutor == nil {
			return RetryOutcome{}, fmt.Errorf("loop: escalation hook after attempt %d: %w", attempt, ErrNilExecutor)
		}
		currentExecutor = nextExecutor
	}

	return l.markNeedsHuman(task, l.policy.MaxAttempts, last)
}

func validateRetryPolicy(policy RetryPolicy) error {
	if policy.MaxAttempts < 0 {
		return ErrNegativeMaxAttempts
	}
	if policy.Escalate == nil {
		return ErrNilEscalationHook
	}
	return nil
}

func (l *RetryingLoop) markNeedsHuman(task supervisor.Task, attempts int, last Outcome) (RetryOutcome, error) {
	result, err := l.statusWriter.WriteStatus(task.ID, tasksource.WritableStatusNeedsHuman)
	if err != nil {
		return RetryOutcome{}, fmt.Errorf("loop: mark task %s needs-human: %w", task.ID, err)
	}
	return RetryOutcome{
		Kind:        RetryOutcomeEscalated,
		Task:        task,
		Attempts:    attempts,
		LastOutcome: last,
		StatusWrite: result,
		Advanced:    true,
		Terminal:    true,
	}, nil
}

type singleTaskSource struct {
	task supervisor.Task
}

func (s *singleTaskSource) Next() (supervisor.Task, bool, error) {
	return s.task, true, nil
}
