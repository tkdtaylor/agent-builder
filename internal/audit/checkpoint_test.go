package audit_test

// Tests for CheckpointSigner: the audit-trail checkpoint create seam.
//
// TC-068-01: CheckpointSigner shape compiles; constructors exist.
// TC-068-02: CreateCheckpoint builds correct argv; handles outPath=""; returns
//            non-nil error on runner failure.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// TC-068-01: CheckpointSigner satisfies the expected public shape at compile time.
// This is a compile-time assertion — if CheckpointSigner is missing the methods or
// constructors, this file will not compile.
var _ interface {
	CreateCheckpoint() error
} = (*audit.CheckpointSigner)(nil)

// checkpointRecordingRunner captures argv calls for CheckpointSigner tests.
// It reuses the same recording pattern as recordingRunner in blocksink_test.go.
type checkpointRecordingRunner struct {
	calls   [][]string
	stdout  []byte
	exitErr error
}

func (r *checkpointRecordingRunner) Run(args []string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.exitErr != nil {
		return r.stdout, r.exitErr
	}
	return r.stdout, nil
}

func newCheckpointRecordingRunner() *checkpointRecordingRunner {
	return &checkpointRecordingRunner{}
}

// TC-068-01: both constructors exist and produce a non-nil *CheckpointSigner.
func TestCheckpointSignerConstructors(t *testing.T) {
	// NewCheckpointSigner uses a real binary path (validated at dispatch time, not construction).
	cs := audit.NewCheckpointSigner("/path/to/audit-trail", "/tmp/audit.log", "prod-001", "/tmp/key.pem", "/tmp/checkpoint.json")
	if cs == nil {
		t.Fatal("TC-068-01: NewCheckpointSigner returned nil")
	}

	// NewCheckpointSignerWithRunner injects a fake runner.
	r := newCheckpointRecordingRunner()
	cs2 := audit.NewCheckpointSignerWithRunner("/tmp/audit.log", "prod-001", "/tmp/key.pem", "/tmp/checkpoint.json", r)
	if cs2 == nil {
		t.Fatal("TC-068-01: NewCheckpointSignerWithRunner returned nil")
	}
}

// TC-068-02: CreateCheckpoint builds correct argv with all fields set.
func TestCheckpointSignerArgvAllFieldsSet(t *testing.T) {
	r := newCheckpointRecordingRunner()
	cs := audit.NewCheckpointSignerWithRunner(
		"/tmp/audit.log",
		"prod-001",
		"/tmp/key.pem",
		"/tmp/checkpoint.json",
		r,
	)

	if err := cs.CreateCheckpoint(); err != nil {
		t.Fatalf("TC-068-02: CreateCheckpoint returned error: %v", err)
	}

	if len(r.calls) != 1 {
		t.Fatalf("TC-068-02: expected 1 runner call, got %d", len(r.calls))
	}

	want := []string{
		"checkpoint", "create",
		"--logfile", "/tmp/audit.log",
		"--log-id", "prod-001",
		"--signing-key", "/tmp/key.pem",
		"--out", "/tmp/checkpoint.json",
	}
	got := r.calls[0]
	if len(got) != len(want) {
		t.Fatalf("TC-068-02: argv length = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("TC-068-02: argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TC-068-02: CreateCheckpoint with outPath="" omits the --out flag.
func TestCheckpointSignerArgvOutPathEmpty(t *testing.T) {
	r := newCheckpointRecordingRunner()
	cs := audit.NewCheckpointSignerWithRunner(
		"/tmp/audit.log",
		"prod-001",
		"/tmp/key.pem",
		"", // empty outPath — stdout mode
		r,
	)

	if err := cs.CreateCheckpoint(); err != nil {
		t.Fatalf("TC-068-02 stdout mode: CreateCheckpoint returned error: %v", err)
	}

	if len(r.calls) != 1 {
		t.Fatalf("TC-068-02 stdout mode: expected 1 runner call, got %d", len(r.calls))
	}

	args := r.calls[0]

	// --out must NOT appear in the argv.
	for _, arg := range args {
		if arg == "--out" {
			t.Errorf("TC-068-02 stdout mode: --out flag present in argv but outPath is empty: %v", args)
		}
	}

	// The expected fixed flags must still be present.
	wantFixed := []string{"checkpoint", "create", "--logfile", "--log-id", "--signing-key"}
	for _, flag := range wantFixed {
		if !containsCheckpointArg(args, flag) {
			t.Errorf("TC-068-02 stdout mode: flag %q missing from argv: %v", flag, args)
		}
	}

	// Exactly 8 args: ["checkpoint", "create", "--logfile", v, "--log-id", v, "--signing-key", v]
	if len(args) != 8 {
		t.Errorf("TC-068-02 stdout mode: argv length = %d, want 8\nargv: %v", len(args), args)
	}
}

// TC-068-02: CreateCheckpoint returns non-nil error when runner returns an error.
func TestCheckpointSignerRunnerFailureReturnsError(t *testing.T) {
	r := newCheckpointRecordingRunner()
	r.exitErr = errors.New("exit status 1")
	r.stdout = []byte("checkpoint: signing key not found")

	cs := audit.NewCheckpointSignerWithRunner(
		"/tmp/audit.log",
		"prod-001",
		"/tmp/key.pem",
		"/tmp/checkpoint.json",
		r,
	)

	err := cs.CreateCheckpoint()
	if err == nil {
		t.Fatal("TC-068-02 runner failure: CreateCheckpoint returned nil error, want non-nil")
	}

	// Error must wrap or contain the runner error.
	if !errors.Is(err, r.exitErr) && !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("TC-068-02 runner failure: error %v does not reference runner error %v", err, r.exitErr)
	}
}

// TC-068-07 (leaf invariant): This file is in package audit_test so it does not
// affect the leaf invariant. The invariant is checked by:
//   go list -deps ./internal/audit/... | grep 'agent-builder/internal/' | grep -v 'internal/audit$'
// which must produce no output (no non-audit internal imports). See the harness command.

// TestCheckpointSignerRealBinary is an L6 gate-behind-env test that runs the
// real audit-trail binary's "checkpoint create" verb. It requires:
//   - AGENT_BUILDER_LIVE_AUDIT=1
//   - AGENT_BUILDER_AUDIT_BIN pointing at the audit-trail binary
//   - The binary to support "keygen" and "checkpoint create" verbs
//
// This test is NOT required to run for L5 verification. It documents the L6
// live-binary contract. Without AGENT_BUILDER_LIVE_AUDIT=1 it skips immediately.
func TestCheckpointSignerRealBinary(t *testing.T) {
	if os.Getenv("AGENT_BUILDER_LIVE_AUDIT") != "1" {
		t.Skip("AGENT_BUILDER_LIVE_AUDIT=1 not set; skipping real-binary checkpoint test")
	}

	binPath := os.Getenv("AGENT_BUILDER_AUDIT_BIN")
	if binPath == "" {
		binPath = "$HOME/Code/Public/audit-trail/audit-trail"
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Skipf("audit-trail binary not found at %s: %v", binPath, err)
	}

	// Produce a real audit chain to checkpoint.
	logfile := filepath.Join(t.TempDir(), "audit-068.log")
	sink := audit.NewBlockSink(binPath, logfile)

	ev := audit.AuditEvent{
		Action: audit.ActionPick,
		RunID:  "run-068",
		TaskID: "068",
	}
	if err := sink.Append(ev); err != nil {
		t.Fatalf("setup: Append failed: %v", err)
	}
	if err := sink.Seal(); err != nil {
		t.Fatalf("setup: Seal failed: %v", err)
	}

	// For the L6 checkpoint test we need a real signing key. The keygen verb is
	// assumed to be available on the binary. If it fails, skip gracefully.
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "signing.pem")
	keygenRunner := newCheckpointRecordingRunner()
	keygenSigner := audit.NewCheckpointSignerWithRunner("", "", "", "", keygenRunner)
	_ = keygenSigner // key generation uses a direct runner call below

	// Use a real runner to call keygen.
	realRunner := &realExecRunner{binPath: binPath}
	if _, err := realRunner.Run([]string{"keygen", "--out", keyPath}); err != nil {
		t.Skipf("L6: keygen failed (binary may not support this verb): %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Skipf("L6: keygen produced no key at %s: %v", keyPath, err)
	}

	outPath := filepath.Join(t.TempDir(), "checkpoint.json")
	cs := audit.NewCheckpointSignerWithRunner(logfile, "test-log-068", keyPath, outPath, realRunner)
	if err := cs.CreateCheckpoint(); err != nil {
		t.Fatalf("TC-068-L6: CreateCheckpoint failed: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("TC-068-L6: checkpoint JSON not found at %s: %v", outPath, err)
	}
	t.Logf("TC-068-L6 PASS: checkpoint JSON created at %s", outPath)
}

// realExecRunner is a test-only ExecRunner that shells out for L6 real-binary tests.
// It lives in the test file so os/exec stays out of the leaf package itself.
type realExecRunner struct {
	binPath string
}

func (r *realExecRunner) Run(args []string) ([]byte, error) {
	cmd := exec.Command(r.binPath, args...) //nolint:gosec // test-only; path is test-supplied
	return cmd.Output()
}

func containsCheckpointArg(args []string, s string) bool {
	for _, a := range args {
		if a == s {
			return true
		}
	}
	return false
}
