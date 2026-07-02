// Package supervisor is the trusted, outside-the-box control loop. It dispatches
// one task at a time to an executor, enforces the wall-clock/escalation kill, and
// tears the containment box down. It deliberately depends on no executor/LLM/web
// code (invariant F-003 in docs/spec/SPEC.md) so a hijacked agent inside the box
// can never reach back through it.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
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

	// ErrRunCancelled means the run's context was cancelled mid-flight (a `cancel
	// <goalID>` reached this worker via the per-goal cancel context — ADR 054 §5).
	// It triggers the SAME box.Kill/Teardown path as ErrRunTimedOut; the wall-clock
	// timeout remains the backstop. The teardown error (if any) is errors.Join'd
	// onto this so the cancel handler can surface a partial-teardown leak.
	ErrRunCancelled = errors.New("supervisor: run cancelled")
)

// Task is one unit of work: build or modify exactly one target repo on its own
// branch. One task = one repo = one branch (no cross-repo sprawl).
type Task struct {
	ID           string // e.g. "001"
	Repo         string // target block repo, e.g. "exec-sandbox"
	Spec         string // path to the task spec the executor must satisfy
	PriorFailure string // non-empty only on retry attempt N≥2; formatted gate-failure detail from previous attempt
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
	Run(ctx context.Context, t Task) (Result, error)
}

// Gate is the machine-checkable definition of done (tests + build + lint +
// dep-scan/code-scanner). A Task is never "done" unattended unless Verify passes.
type Gate interface {
	Verify(repoPath string) gate.Verdict
}

// GoalSource is the seam interface for reading the task/goal that an agent
// must work on. It returns the next available task, a boolean indicating whether
// a task was found, and an optional error.
type GoalSource interface {
	Next() (task Task, ok bool, err error)
}

// PublishRequest is the payload passed to a ResultSink.Publish call.
// It mirrors the concrete internal/publisher.Request structure at the seam boundary.
type PublishRequest struct {
	Task     Task   // the task that was completed
	Worktree string // path to the ephemeral worktree
	Branch   string // the branch the executor created
	Remote   string // the git remote to publish to
}

// PublishResult is the response from a ResultSink.Publish call.
// It mirrors the concrete internal/publisher.Result structure at the seam boundary.
type PublishResult struct {
	Branch string // the branch that was published
	PRURL  string // URL to the pull request (if created)
	PRID   string // pull request ID (if created)
}

// ResultSink is the seam interface for writing the result of an agent's work
// back to persistent storage or a callback.
type ResultSink interface {
	Publish(ctx context.Context, req PublishRequest) (PublishResult, error)
}

// BoxHandle identifies a created containment box for one dispatched task.
type BoxHandle struct {
	ID       string
	Worktree string
	Backend  string // "podman" or "exec-sandbox"
}

// ContainmentBox is the fakeable outside-the-box lifecycle seam.
type ContainmentBox interface {
	Create(Task) (BoxHandle, error)
	Kill(BoxHandle) error
	Teardown(BoxHandle) error
}

// InBoxLoop is the fakeable seam for one agent-loop run inside a created box.
//
// ctx is the per-goal cancel context threaded from Supervisor.Run (ADR 054 §5,
// task 116/155). The implementation forwards it down to the executor so a
// caller cancellation reaches the in-flight executor subprocess. The wall-clock
// timeout arm remains independent (task 156).
type InBoxLoop interface {
	RunInside(ctx context.Context, handle BoxHandle, t Task, streams RunStreams) error
}

// Supervisor is the outside-the-box dispatcher.
//
// The default-deny egress allowlist — the load-bearing control for the accepted
// token-in-box risk (see docs/spec/configuration.md) — will be added here in the
// containment task (Phase 0.3), when something actually enforces it.
type Supervisor struct {
	sandboxRunner    sandbox.Runner
	box              ContainmentBox
	loop             InBoxLoop
	task             Task
	logger           *slog.Logger
	runRecordPath    string
	sink             audit.Sink
	checkpointSigner *audit.CheckpointSigner
	runTimeout       time.Duration
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

// WithSink configures the optional typed audit action sink (task 041). The
// supervisor hands it to the in-box loop through RunStreams.Audit and Seals it
// before containment teardown on both the success and failure paths, mirroring
// the RunRecord close-before-teardown durability rule. A nil sink disables audit
// projection and leaves the run behaving exactly as before.
func WithSink(sink audit.Sink) Option {
	return func(s *Supervisor) {
		s.sink = sink
	}
}

// WithCheckpointSigner configures the optional checkpoint signer (ADR 037,
// task 068). When set, the supervisor calls cs.CreateCheckpoint() on the
// success path after Seal() and VerifyChain returns valid (IsTampered()==false).
// Checkpoint creation failure is logged but does NOT abort teardown or change
// the run outcome — the chain is already sealed and verified; the checkpoint is
// forensic metadata, not a gate condition. A nil signer disables checkpoint
// creation silently.
func WithCheckpointSigner(cs *audit.CheckpointSigner) Option {
	return func(s *Supervisor) {
		s.checkpointSigner = cs
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
//
// ctx is the per-goal cancel context (ADR 054 §5, task 116). A cancel
// (`cancel <goalID>`) cancels the goal's derived ctx, which fires the run-loop's
// case <-ctx.Done(): arm — the SAME box.Kill/Teardown path the wall-clock timeout
// already drives, returning ErrRunCancelled. The wall-clock timeout remains the
// independent backstop. A nil-equivalent context.Background() leaves Run behaving
// exactly as the pre-116 no-cancel path (the ctx.Done() arm never fires).
func (s *Supervisor) Run(ctx context.Context) (err error) {
	if ctx == nil {
		ctx = context.Background()
	}
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
		// Seal the audit sink before containment teardown on BOTH success and
		// failure paths — same durability ordering as closeRunRecord. A nil
		// sink seals to a no-op.
		sealErr := s.sealSink()

		// Checkpoint creation (ADR 037, task 068): only on the success path
		// (err == nil after loop + recordErr + sealErr), after Seal, after
		// VerifyChain returns valid. Failure is logged and does NOT change the
		// run outcome or block teardown.
		if err == nil && recordErr == nil && sealErr == nil {
			s.maybeCreateCheckpoint()
		}

		teardownErr := s.box.Teardown(handle)
		s.logLifecycle("box.torn_down", handle)
		if teardownErr != nil {
			teardownErr = fmt.Errorf("supervisor: teardown box: %w", teardownErr)
		}

		err = errors.Join(err, recordErr, sealErr, teardownErr)
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
	// Attach the optional typed audit sink so the in-box loop can project action
	// events through it alongside the raw command/stdout/stderr stream above.
	streams.Audit = s.sink

	s.logLifecycle("loop.started", handle)
	if record != nil {
		if commandErr := record.Command("RunInside task " + s.task.ID); commandErr != nil {
			return fmt.Errorf("supervisor: write run command: %w", commandErr)
		}
	}
	loopResult := s.runLoop(ctx, handle, streams)

	// The run-loop select has THREE trigger arms (ADR 054 §5, task 116):
	//   1. the loop finishing on its own (normal completion / failure),
	//   2. the per-goal cancel context being cancelled (case <-ctx.Done():), and
	//   3. the optional wall-clock timeout (case <-timer.C:).
	// Arms 2 and 3 are INDEPENDENT triggers into the SAME box.Kill/Teardown path
	// (killAndJoin) — cancel invents no new teardown mechanism; it reuses the kill
	// path the timeout already drives, and the timeout remains the backstop even
	// when a cancel races it. When no timeout is configured the timer arm is simply
	// a nil channel (never fires), so the cancel arm still works unconditionally.
	var timerC <-chan time.Time
	if s.runTimeout > 0 {
		timer := time.NewTimer(s.runTimeout)
		defer timer.Stop()
		timerC = timer.C
	}

	select {
	case result := <-loopResult:
		err = result.err
	case <-ctx.Done():
		outcome = RunOutcomeFailed
		cancelErr := fmt.Errorf("%w: %v", ErrRunCancelled, ctx.Err())
		s.logCancel(handle, cancelErr)
		err = s.killAndJoin(handle, cancelErr, loopResult)
		return err
	case <-timerC:
		outcome = RunOutcomeTimedOut
		timeoutErr := fmt.Errorf("%w after %s", ErrRunTimedOut, s.runTimeout)
		s.logTimeout(handle, timeoutErr)
		err = s.killAndJoin(handle, timeoutErr, loopResult)
		return err
	}

	if err == nil {
		outcome = RunOutcomeCompleted
	}
	return err
}

// killAndJoin kills the box (the cancel/timeout trigger's shared teardown path)
// and ALWAYS joins the in-flight loop goroutine before returning. Joining is
// load-bearing for correctness AND for race-freedom: it establishes a
// happens-before edge between the loop goroutine's writes and the caller's reads
// after Run returns, so neither a kill-error early return nor the cancel arm can
// leave the loop goroutine running past Run (the pre-116 kill-error path returned
// without joining, leaking the goroutine and racing its writes — fixed here).
//
// It returns triggerErr joined with any kill error and any loop error, so a
// partial-teardown failure (a non-nil kill error) is surfaced to the cancel
// handler as a leak rather than swallowed. Kill errors are wrapped with explicit
// "leaked box" language so the operator sees unambiguous leak-requiring-attention
// messaging in the rendered outcome (REQ-116-05).
func (s *Supervisor) killAndJoin(handle BoxHandle, triggerErr error, loopResult <-chan loopRunResult) error {
	killErr := s.box.Kill(handle)
	// Join the loop goroutine unconditionally — on EVERY path, including a kill
	// error. This is the fix for the pre-116 leak/race where the kill-error path
	// returned before reading loopResult.
	loop := <-loopResult
	out := triggerErr
	if killErr != nil {
		// Wrap the kill error with explicit "leaked box" language so the operator sees
		// this is a security-relevant leak requiring immediate attention (the box holds
		// an executor token and was not confirmed torn down). Use %w to maintain the
		// error chain so existing tests that check errors.Is(err, originalKillErr) still work.
		leakErr := fmt.Errorf("supervisor: box %q (worktree %s) leaked — operator attention required: %w",
			handle.ID, handle.Worktree, killErr)
		out = errors.Join(out, leakErr)
	}
	if loop.err != nil {
		out = errors.Join(out, loop.err)
	}
	return out
}

type loopRunResult struct {
	err error
}

func (s *Supervisor) runLoop(ctx context.Context, handle BoxHandle, streams RunStreams) <-chan loopRunResult {
	done := make(chan loopRunResult, 1)
	go func() {
		var err error
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("supervisor: run inside box panic: %v", recovered)
			}
			done <- loopRunResult{err: err}
		}()
		if loopErr := s.loop.RunInside(ctx, handle, s.task, streams); loopErr != nil {
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

// logCancel logs the cancel-triggered kill loudly (ADR 054 §5). It mirrors
// logTimeout: the cancel arm fires the same box.Kill/Teardown path, so the kill is
// recorded with a distinct event=box.kill.cancel so an operator can tell a cancel
// teardown apart from a wall-clock timeout in the logs.
func (s *Supervisor) logCancel(handle BoxHandle, err error) {
	if s.logger == nil {
		return
	}
	s.logger.Error("supervisor cancel kill",
		"event", "box.kill.cancel",
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

// maybeCreateCheckpoint calls VerifyChain on the checkpoint signer and, only
// when the chain is valid (IsTampered()==false), calls CreateCheckpoint. This
// guards against signing a tampered chain. Failure is logged but does NOT abort
// teardown or change the run outcome — the chain is already sealed and verified;
// the checkpoint is forensic metadata, not a gate condition.
//
// Called only on the success path (loop err == nil, record closed, sink sealed).
func (s *Supervisor) maybeCreateCheckpoint() {
	if s.checkpointSigner == nil {
		return
	}
	result, err := s.checkpointSigner.VerifyChain()
	if err != nil {
		s.logger.Error("supervisor: checkpoint verify chain error (skipping checkpoint)",
			"error", err)
		return
	}
	if result.IsTampered() {
		s.logger.Error("supervisor: chain tampered — checkpoint creation skipped to avoid false attestation",
			"tampered_at", result.TamperedAt,
			"message", result.Message)
		return
	}
	if cpErr := s.checkpointSigner.CreateCheckpoint(); cpErr != nil {
		s.logger.Error("supervisor: checkpoint creation failed (run outcome unchanged)",
			"error", cpErr)
	}
}

// sealSink flushes and closes the optional audit sink. It is the audit-chain
// analogue of closeRunRecord: called in the teardown defer so the chain is
// durable before the containment box is torn down, on both success and failure
// paths. A nil sink seals to a no-op.
func (s *Supervisor) sealSink() error {
	if s.sink == nil {
		return nil
	}
	if err := s.sink.Seal(); err != nil {
		return fmt.Errorf("supervisor: seal audit sink: %w", err)
	}
	return nil
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
