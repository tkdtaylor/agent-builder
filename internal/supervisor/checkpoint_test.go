package supervisor

// Tests for checkpoint signer wiring in the supervisor (task 068).
//
// TC-068-05: Supervisor calls CreateCheckpoint after Seal+VerifyChain; not called
//            when chain is tampered; skipped when signer nil.
// TC-068-06: Checkpoint failure logged, teardown and run outcome unaffected.

import (
	"context"
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// fakeCheckpointRunner records VerifyChain and CreateCheckpoint argv calls
// separately to allow independent assertions. It implements audit.ExecRunner.
type fakeCheckpointRunner struct {
	// verifyCalls records argv slices passed to "verify" calls.
	verifyCalls [][]string
	// checkpointCalls records argv slices passed to "checkpoint create" calls.
	checkpointCalls [][]string
	// verifyOut is the JSON to return for verify calls.
	verifyOut []byte
	// verifyErr is the error to return for verify calls.
	verifyErr error
	// checkpointErr is the error to return for checkpoint create calls.
	checkpointErr error
}

func (r *fakeCheckpointRunner) Run(args []string) ([]byte, error) {
	if len(args) > 0 && args[0] == "verify" {
		r.verifyCalls = append(r.verifyCalls, append([]string(nil), args...))
		return r.verifyOut, r.verifyErr
	}
	// checkpoint create
	r.checkpointCalls = append(r.checkpointCalls, append([]string(nil), args...))
	return nil, r.checkpointErr
}

// validVerifyJSON is the JSON a healthy chain produces.
const validVerifyJSON = `{"valid":true,"tamper_detected_at":null,"message":"chain intact"}`

// tamperedVerifyJSON is the JSON a tampered chain produces.
const tamperedVerifyJSON = `{"valid":false,"tamper_detected_at":1,"message":"tamper detected"}`

// newFakeCheckpointSigner constructs a CheckpointSigner backed by a
// fakeCheckpointRunner for test assertions.
func newFakeCheckpointSigner(runner *fakeCheckpointRunner) *audit.CheckpointSigner {
	return audit.NewCheckpointSignerWithRunner(
		"/tmp/audit.log",
		"test-log-068",
		"/tmp/key.pem",
		"/tmp/checkpoint.json",
		runner,
	)
}

// newSuccessfulSupervisorRun runs a supervisor with a fake box + loop that
// succeeds, and the given checkpoint signer (may be nil). Returns the run error.
func newSuccessfulSupervisorRun(t *testing.T, cs *audit.CheckpointSigner) error {
	t.Helper()
	box := &fakeBox{handle: BoxHandle{ID: "box-068"}}
	loop := &fakeInBoxLoop{}
	opts := []Option{
		WithTask(Task{ID: "068"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
	}
	if cs != nil {
		opts = append(opts, WithCheckpointSigner(cs))
	}
	return New(opts...).Run(context.Background())
}

// newFailingSupervisorRun runs a supervisor with a loop that returns an error.
func newFailingSupervisorRun(t *testing.T, cs *audit.CheckpointSigner) error {
	t.Helper()
	box := &fakeBox{handle: BoxHandle{ID: "box-068-fail"}}
	loop := &fakeInBoxLoop{err: errors.New("loop failed")}
	opts := []Option{
		WithTask(Task{ID: "068"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
	}
	if cs != nil {
		opts = append(opts, WithCheckpointSigner(cs))
	}
	return New(opts...).Run(context.Background())
}

// TC-068-05: Supervisor calls VerifyChain then CreateCheckpoint on success path.
func TestSupervisorCheckpointCalledOnSuccess(t *testing.T) {
	runner := &fakeCheckpointRunner{
		verifyOut: []byte(validVerifyJSON),
	}
	cs := newFakeCheckpointSigner(runner)

	if err := newSuccessfulSupervisorRun(t, cs); err != nil {
		t.Fatalf("TC-068-05: Run() error = %v, want nil", err)
	}

	if len(runner.verifyCalls) != 1 {
		t.Fatalf("TC-068-05: VerifyChain called %d times, want 1", len(runner.verifyCalls))
	}
	if len(runner.checkpointCalls) != 1 {
		t.Fatalf("TC-068-05: CreateCheckpoint called %d times, want 1", len(runner.checkpointCalls))
	}

	// Verify the checkpoint argv contains the expected verbs and flags.
	cpArgs := runner.checkpointCalls[0]
	if len(cpArgs) < 2 || cpArgs[0] != "checkpoint" || cpArgs[1] != "create" {
		t.Errorf("TC-068-05: checkpoint argv[0:2] = %v, want [checkpoint create]", cpArgs[:min(2, len(cpArgs))])
	}
	if !containsFlag(cpArgs, "--logfile") {
		t.Errorf("TC-068-05: --logfile missing from checkpoint argv: %v", cpArgs)
	}
	if !containsFlag(cpArgs, "--log-id") {
		t.Errorf("TC-068-05: --log-id missing from checkpoint argv: %v", cpArgs)
	}
	if !containsFlag(cpArgs, "--signing-key") {
		t.Errorf("TC-068-05: --signing-key missing from checkpoint argv: %v", cpArgs)
	}
}

// TC-068-05: CreateCheckpoint NOT called when VerifyChain reports tampered.
func TestSupervisorCheckpointSkippedWhenTampered(t *testing.T) {
	runner := &fakeCheckpointRunner{
		verifyOut: []byte(tamperedVerifyJSON),
		verifyErr: nil, // parseable JSON even on tamper; exit 1 sets verifyErr in real path
	}
	cs := newFakeCheckpointSigner(runner)

	// Run succeeds from the loop perspective; chain is tampered post-seal.
	if err := newSuccessfulSupervisorRun(t, cs); err != nil {
		t.Fatalf("TC-068-05 tampered: Run() error = %v, want nil (run outcome unchanged)", err)
	}

	if len(runner.verifyCalls) != 1 {
		t.Fatalf("TC-068-05 tampered: VerifyChain called %d times, want 1", len(runner.verifyCalls))
	}
	if len(runner.checkpointCalls) != 0 {
		t.Fatalf("TC-068-05 tampered: CreateCheckpoint called %d times, want 0 (tampered chain)", len(runner.checkpointCalls))
	}
}

// TC-068-05: No checkpoint signer → CreateCheckpoint never called, run succeeds.
func TestSupervisorCheckpointSkippedWhenSignerNil(t *testing.T) {
	if err := newSuccessfulSupervisorRun(t, nil); err != nil {
		t.Fatalf("TC-068-05 nil signer: Run() error = %v, want nil", err)
	}
	// No assertion needed beyond successful run — the nil path is a no-op.
}

// TC-068-05: Checkpoint is NOT called on the failure path (loop error).
func TestSupervisorCheckpointNotCalledOnFailure(t *testing.T) {
	runner := &fakeCheckpointRunner{
		verifyOut: []byte(validVerifyJSON),
	}
	cs := newFakeCheckpointSigner(runner)

	err := newFailingSupervisorRun(t, cs)
	if err == nil {
		t.Fatal("TC-068-05 failure path: Run() error = nil, want loop error")
	}

	if len(runner.checkpointCalls) != 0 {
		t.Fatalf("TC-068-05 failure path: CreateCheckpoint called %d times on failure path, want 0", len(runner.checkpointCalls))
	}
}

// TC-068-06: Checkpoint failure is logged but does NOT change run outcome or block teardown.
func TestSupervisorCheckpointFailureDoesNotAffectOutcome(t *testing.T) {
	runner := &fakeCheckpointRunner{
		verifyOut:     []byte(validVerifyJSON),
		checkpointErr: errors.New("checkpoint create: signing key expired"),
	}
	cs := newFakeCheckpointSigner(runner)

	// Run must still succeed (nil error) even though CreateCheckpoint failed.
	if err := newSuccessfulSupervisorRun(t, cs); err != nil {
		t.Fatalf("TC-068-06: Run() error = %v after checkpoint failure, want nil (outcome unchanged)", err)
	}

	// VerifyChain was called.
	if len(runner.verifyCalls) != 1 {
		t.Fatalf("TC-068-06: VerifyChain called %d times, want 1", len(runner.verifyCalls))
	}
	// CreateCheckpoint was called (and failed).
	if len(runner.checkpointCalls) != 1 {
		t.Fatalf("TC-068-06: CreateCheckpoint called %d times, want 1", len(runner.checkpointCalls))
	}
}

// TC-068-06: Teardown still runs when checkpoint fails.
func TestSupervisorTeardownRunsAfterCheckpointFailure(t *testing.T) {
	runner := &fakeCheckpointRunner{
		verifyOut:     []byte(validVerifyJSON),
		checkpointErr: errors.New("checkpoint create: network error"),
	}
	cs := newFakeCheckpointSigner(runner)

	box := &fakeBox{handle: BoxHandle{ID: "box-068-td"}}
	loop := &fakeInBoxLoop{}
	err := New(
		WithTask(Task{ID: "068"}),
		WithContainmentBox(box),
		WithInBoxLoop(loop),
		WithCheckpointSigner(cs),
	).Run(context.Background())

	if err != nil {
		t.Fatalf("TC-068-06 teardown: Run() error = %v, want nil", err)
	}
	if box.teardownCalls != 1 {
		t.Fatalf("TC-068-06 teardown: teardown calls = %d, want 1", box.teardownCalls)
	}
}

// containsFlag returns true if flag appears in args.
func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
