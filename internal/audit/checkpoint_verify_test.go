package audit_test

// Tests for CheckpointVerifier: the audit-trail checkpoint verify seam.
//
// TC-069-01: CheckpointVerifier shape compiles; constructors exist;
//            CheckpointVerifyResult type present; VerifyCheckpoint method exists.
// TC-069-02: VerifyCheckpoint builds correct argv; maps responses correctly;
//            binary failure returns error wrapping ErrCheckpointVerifierUnavailable.

import (
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// TC-069-01: CheckpointVerifier satisfies the expected public shape at compile time.
// This is a compile-time assertion — if the method or constructors are missing,
// this file will not compile.
var _ interface {
	VerifyCheckpoint() (audit.CheckpointVerifyResult, error)
} = (*audit.CheckpointVerifier)(nil)

// TC-069-05 (leaf invariant): verified by make fitness-audit-isolation and
// go list -deps ./internal/audit/... — CheckpointVerifier imports only stdlib;
// no internal/ packages leak into the audit leaf. No in-process test assertion
// needed: the F-005 fitness check (task 042) is the machine-checkable gate.

// checkpointVerifyRecordingRunner captures argv calls for CheckpointVerifier tests.
type checkpointVerifyRecordingRunner struct {
	calls   [][]string
	stdout  []byte
	exitErr error
}

func (r *checkpointVerifyRecordingRunner) Run(args []string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	return r.stdout, r.exitErr
}

// TC-069-01: both constructors exist and produce a non-nil *CheckpointVerifier.
func TestCheckpointVerifierConstructors(t *testing.T) {
	// NewCheckpointVerifier uses a real binary path (validated at dispatch time, not construction).
	cv := audit.NewCheckpointVerifier("/path/to/audit-trail", "/tmp/cp.json", "/tmp/pub.pem", "/tmp/audit.log")
	if cv == nil {
		t.Fatal("TC-069-01: NewCheckpointVerifier returned nil")
	}

	// NewCheckpointVerifierWithRunner injects a fake runner.
	r := &checkpointVerifyRecordingRunner{}
	cv2 := audit.NewCheckpointVerifierWithRunner("/tmp/cp.json", "/tmp/pub.pem", "/tmp/audit.log", r)
	if cv2 == nil {
		t.Fatal("TC-069-01: NewCheckpointVerifierWithRunner returned nil")
	}
}

// TC-069-01: CheckpointVerifyResult type has at minimum a Valid bool field.
func TestCheckpointVerifyResultShape(t *testing.T) {
	result := audit.CheckpointVerifyResult{Valid: true, Message: "signature ok"}
	if !result.Valid {
		t.Fatal("TC-069-01: CheckpointVerifyResult.Valid field not working")
	}
	if result.Message == "" {
		t.Fatal("TC-069-01: CheckpointVerifyResult.Message field not working")
	}
}

// TC-069-02: VerifyCheckpoint builds correct argv with logfile set.
// Fake runner returns valid JSON ({"valid":true,"message":"signature ok"}), exit 0.
func TestCheckpointVerifierArgvWithLogfile(t *testing.T) {
	r := &checkpointVerifyRecordingRunner{
		stdout: []byte(`{"valid":true,"message":"signature ok"}`),
	}
	cv := audit.NewCheckpointVerifierWithRunner("/tmp/cp.json", "/tmp/pub.pem", "/tmp/audit.log", r)

	result, err := cv.VerifyCheckpoint()
	if err != nil {
		t.Fatalf("TC-069-02 valid+logfile: VerifyCheckpoint returned error: %v", err)
	}
	if !result.Valid {
		t.Fatalf("TC-069-02 valid+logfile: result.Valid = false, want true")
	}
	if result.Message != "signature ok" {
		t.Fatalf("TC-069-02 valid+logfile: result.Message = %q, want %q", result.Message, "signature ok")
	}

	if len(r.calls) != 1 {
		t.Fatalf("TC-069-02 valid+logfile: expected 1 runner call, got %d", len(r.calls))
	}

	want := []string{
		"checkpoint", "verify",
		"--checkpoint", "/tmp/cp.json",
		"--public-key", "/tmp/pub.pem",
		"--logfile", "/tmp/audit.log",
	}
	got := r.calls[0]
	if len(got) != len(want) {
		t.Fatalf("TC-069-02 valid+logfile: argv length = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TC-069-02 valid+logfile: argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TC-069-02: VerifyCheckpoint with logfile="" omits --logfile; parseable
// valid:false response is a clean verdict (nil error).
func TestCheckpointVerifierArgvWithoutLogfile(t *testing.T) {
	r := &checkpointVerifyRecordingRunner{
		stdout:  []byte(`{"valid":false,"message":"signature mismatch"}`),
		exitErr: errors.New("exit status 1"),
	}
	cv := audit.NewCheckpointVerifierWithRunner("/tmp/cp.json", "/tmp/pub.pem", "", r)

	result, err := cv.VerifyCheckpoint()
	if err != nil {
		t.Fatalf("TC-069-02 invalid+no-logfile: VerifyCheckpoint returned error: %v (want nil — parseable valid:false is a clean verdict)", err)
	}
	if result.Valid {
		t.Fatalf("TC-069-02 invalid+no-logfile: result.Valid = true, want false")
	}
	if result.Message != "signature mismatch" {
		t.Fatalf("TC-069-02 invalid+no-logfile: result.Message = %q, want %q", result.Message, "signature mismatch")
	}

	if len(r.calls) != 1 {
		t.Fatalf("TC-069-02 invalid+no-logfile: expected 1 runner call, got %d", len(r.calls))
	}

	// --logfile must NOT appear in the argv when logfile is empty.
	args := r.calls[0]
	for _, arg := range args {
		if arg == "--logfile" {
			t.Errorf("TC-069-02 invalid+no-logfile: --logfile flag present in argv but logfile is empty: %v", args)
		}
	}

	// Exactly 6 args: ["checkpoint", "verify", "--checkpoint", v, "--public-key", v]
	if len(args) != 6 {
		t.Errorf("TC-069-02 invalid+no-logfile: argv length = %d, want 6\nargv: %v", len(args), args)
	}
}

// TC-069-02: binary not found or nil stdout — returns non-nil error wrapping
// ErrCheckpointVerifierUnavailable. Result must be Valid:false.
func TestCheckpointVerifierBinaryNotFound(t *testing.T) {
	r := &checkpointVerifyRecordingRunner{
		stdout:  nil,
		exitErr: errors.New("exec: not found"),
	}
	cv := audit.NewCheckpointVerifierWithRunner("/tmp/cp.json", "/tmp/pub.pem", "", r)

	result, err := cv.VerifyCheckpoint()
	if err == nil {
		t.Fatal("TC-069-02 binary-not-found: VerifyCheckpoint returned nil error, want non-nil")
	}
	if !errors.Is(err, audit.ErrCheckpointVerifierUnavailable) {
		t.Errorf("TC-069-02 binary-not-found: error %v does not wrap ErrCheckpointVerifierUnavailable", err)
	}
	if result.Valid {
		t.Errorf("TC-069-02 binary-not-found: result.Valid = true, must be false when error returned")
	}
}

// TC-069-02: unparseable output — returns non-nil error wrapping
// ErrCheckpointVerifierUnavailable. Result must be Valid:false.
func TestCheckpointVerifierUnparseableOutput(t *testing.T) {
	r := &checkpointVerifyRecordingRunner{
		stdout: []byte("unexpected non-json output from binary"),
	}
	cv := audit.NewCheckpointVerifierWithRunner("/tmp/cp.json", "/tmp/pub.pem", "", r)

	result, err := cv.VerifyCheckpoint()
	if err == nil {
		t.Fatal("TC-069-02 unparseable: VerifyCheckpoint returned nil error, want non-nil")
	}
	if !errors.Is(err, audit.ErrCheckpointVerifierUnavailable) {
		t.Errorf("TC-069-02 unparseable: error %v does not wrap ErrCheckpointVerifierUnavailable", err)
	}
	if result.Valid {
		t.Errorf("TC-069-02 unparseable: result.Valid = true, must be false when error returned")
	}
}
