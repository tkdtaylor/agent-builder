package audit_test

// Tests for VerifyChain: the block-severity integrity gate helper that invokes
// "audit-trail verify --logfile <path>" and maps the block's verdict to a
// typed result.
//
// TC-040-01: an intact chain verifies valid (Valid==true, TamperedAt==nil)
// TC-040-02: a tampered chain maps to Valid==false with TamperedAt set
// TC-040-03: Valid==false is a block-severity gate failure (non-zero / distinct from nil error)
// TC-040-04: missing binary or unreadable logfile is a hard error, not "valid"
// TC-040-05: VerifyChain uses os/exec only — dependency check is a separate fitness assertion
// TC-040-06 (L5): real-block round trip — intact valid, tamper invalid

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// verifyRunner is a test double for the ExecRunner seam in VerifyChain tests.
// It returns canned output and exit error to simulate the block's responses
// without spawning real subprocesses.
type verifyRunner struct {
	stdout  string
	exitErr error
}

func (v *verifyRunner) Run(args []string) ([]byte, error) {
	return []byte(v.stdout), v.exitErr
}

// TC-040-01: an intact chain (real or stubbed) verifies Valid==true.
func TestVerifyChainIntactChain(t *testing.T) {
	// Simulate the block returning the documented intact response.
	// {"valid": true, "tamper_detected_at": null, "message": "chain intact"}
	r := &verifyRunner{
		stdout:  `{"valid":true,"tamper_detected_at":null,"message":"chain intact"}`,
		exitErr: nil,
	}
	result, err := audit.VerifyChainWithRunner("/fake/audit-trail", "/fake/log", r)
	if err != nil {
		t.Fatalf("VerifyChain returned error on intact chain: %v", err)
	}
	if !result.Valid {
		t.Errorf("VerifyChain: Valid = false, want true; message: %q", result.Message)
	}
	if result.TamperedAt != nil {
		t.Errorf("VerifyChain: TamperedAt = %v, want nil for intact chain", *result.TamperedAt)
	}
	if result.Message == "" {
		t.Error("VerifyChain: Message is empty; want the block's message string")
	}
	t.Logf("TC-040-01 PASS: Valid=%v TamperedAt=%v Message=%q", result.Valid, result.TamperedAt, result.Message)
}

// TC-040-01 edge case: empty chain the block treats as valid.
func TestVerifyChainEmptyChainIsValid(t *testing.T) {
	// Simulate the block returning valid for an empty (zero-event) chain.
	r := &verifyRunner{
		stdout:  `{"valid":true,"tamper_detected_at":null,"message":"chain intact"}`,
		exitErr: nil,
	}
	result, err := audit.VerifyChainWithRunner("/fake/audit-trail", "/fake/empty.log", r)
	if err != nil {
		t.Fatalf("VerifyChain (empty chain) returned error: %v", err)
	}
	if !result.Valid {
		t.Errorf("VerifyChain (empty chain): Valid = false, want true")
	}
}

// TC-040-02: a tampered chain maps to Valid==false with TamperedAt set.
func TestVerifyChainTamperedChain(t *testing.T) {
	// Simulate the block returning tampered JSON with exit code 1.
	// The block reports {"valid":false, "tamper_detected_at":3, "message":"..."}
	r := &verifyRunner{
		stdout:  `{"valid":false,"tamper_detected_at":3,"message":"chain tampered at seq 3"}`,
		exitErr: fmt.Errorf("exit status 1"),
	}
	result, err := audit.VerifyChainWithRunner("/fake/audit-trail", "/fake/log", r)
	// A tampered chain: err is nil (it's not an infrastructure error), result.Valid is false.
	if err != nil {
		t.Fatalf("VerifyChain returned error for tampered chain; want nil err with Valid==false: %v", err)
	}
	if result.Valid {
		t.Errorf("VerifyChain: Valid = true, want false for tampered chain")
	}
	if result.TamperedAt == nil {
		t.Fatal("VerifyChain: TamperedAt is nil, want a non-nil seq for tampered chain")
	}
	if *result.TamperedAt != 3 {
		t.Errorf("VerifyChain: TamperedAt = %d, want 3", *result.TamperedAt)
	}
	t.Logf("TC-040-02 PASS: Valid=%v TamperedAt=%v Message=%q", result.Valid, result.TamperedAt, result.Message)
}

// TC-040-03: Valid==false is a block-severity gate failure — IsTampered() returns true.
// A Valid==true result passes the gate.
func TestVerifyChainBlockSeverity(t *testing.T) {
	// Tampered chain: Valid==false → gate must treat this as block-severity.
	r := &verifyRunner{
		stdout:  `{"valid":false,"tamper_detected_at":1,"message":"tampered at seq 1"}`,
		exitErr: fmt.Errorf("exit status 1"),
	}
	result, err := audit.VerifyChainWithRunner("/fake/audit-trail", "/fake/log", r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The gate helper IsTampered must return true for a tampered result.
	if !result.IsTampered() {
		t.Error("result.IsTampered() = false; want true for tampered chain (block-severity contract)")
	}

	// Intact chain: Valid==true → gate passes.
	r2 := &verifyRunner{
		stdout:  `{"valid":true,"tamper_detected_at":null,"message":"chain intact"}`,
		exitErr: nil,
	}
	result2, err2 := audit.VerifyChainWithRunner("/fake/audit-trail", "/fake/log", r2)
	if err2 != nil {
		t.Fatalf("unexpected error for intact chain: %v", err2)
	}
	if result2.IsTampered() {
		t.Error("result2.IsTampered() = true; want false for intact chain")
	}
	t.Log("TC-040-03 PASS: block-severity semantics correct")
}

// TC-040-04: missing binary is a hard named error, distinct from Valid==false.
func TestVerifyChainMissingBinaryIsError(t *testing.T) {
	result, err := audit.VerifyChain("/nonexistent/audit-trail-does-not-exist-040", "/tmp/test.log")
	if err == nil {
		t.Fatal("VerifyChain with missing binary returned nil error; want a hard named error")
	}
	// Must not report "valid" on error.
	if result.Valid {
		t.Error("result.Valid = true despite error; an unavailable verifier must not report valid")
	}
	// Error message must not be empty.
	if err.Error() == "" {
		t.Error("error message is empty; must name the missing binary / failure mode")
	}
	// Must be an ErrVerifierUnavailable so callers can distinguish infra errors from integrity errors.
	if !errors.Is(err, audit.ErrVerifierUnavailable) {
		t.Errorf("error %v does not wrap ErrVerifierUnavailable; want distinguishable infra error", err)
	}
	t.Logf("TC-040-04 PASS (missing binary): %v", err)
}

// TC-040-04: non-existent logfile path with real binary produces a hard named error.
// REQ-040-03: an unreadable/missing logfile must return ErrVerifierUnavailable (cannot-verify),
// never a clean Valid==false (which would indicate a genuine block-reported tamper verdict).
func TestVerifyChainUnreadableLogfileIsError(t *testing.T) {
	binPath := findAuditTrailBinary()
	if binPath == "" {
		t.Skip("real audit-trail binary not available; skipping logfile-not-found test")
	}

	result, err := audit.VerifyChain(binPath, "/nonexistent/path/audit-040-missing.log")
	// When the logfile doesn't exist the block exits non-zero and writes an error
	// to stderr (not parseable JSON). We must surface a hard named error, not nil.
	if result.Valid {
		t.Error("result.Valid = true for non-existent logfile; must be false or error")
	}
	// REQ-040-03: cannot-verify must surface as a non-nil error, not a clean Valid==false.
	if err == nil {
		t.Fatal("unreadable logfile must return a non-nil error, not a clean Valid==false")
	}
	// The error must be ErrVerifierUnavailable so callers can distinguish infra errors
	// from genuine tamper verdicts.
	if !errors.Is(err, audit.ErrVerifierUnavailable) {
		t.Errorf("expected ErrVerifierUnavailable, got %v", err)
	}
	t.Logf("TC-040-04 PASS (unreadable logfile): err=%v Valid=%v", err, result.Valid)
}

// TC-040-05: VerifyChain uses os/exec only — the internal/audit package must not
// import forbidden packages (executor, LLM/web token paths, or the audit-trail Go
// module itself). This makes the leaf invariant self-enforced now rather than
// deferring entirely to task 042's F-005 fitness check.
func TestVerifyChainUsesExecRunnerSeamOnly(t *testing.T) {
	// Skip gracefully if go is not on PATH (won't happen in this repo, but be safe).
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; skipping import-graph leaf check")
	}

	cmd := exec.Command("go", "list", "-deps", "./internal/audit/...")
	cmd.Dir = findModuleRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps ./internal/audit/... failed: %v", err)
	}

	// Forbidden package segments: executor code, LLM/web token packages (F-003
	// convention), or a direct Go import of the audit-trail block.
	forbidden := []string{
		"/executor",
		"audit-trail",
		"/llm",
		"/web",
		"/token",
	}
	deps := string(out)
	for _, seg := range forbidden {
		if strings.Contains(deps, seg) {
			t.Errorf("TC-040-05 FAIL: internal/audit imports forbidden package segment %q\n"+
				"  Full deps:\n%s", seg, deps)
		}
	}
	t.Log("TC-040-05 PASS: internal/audit leaf deps contain no forbidden package segments")
}

// findModuleRoot walks up from this test file's package directory to find the
// go.mod root so exec.Command("go", "list", ...) runs in the right module.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	// os.Getwd() inside a test returns the package directory.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod walking up from package dir")
		}
		dir = parent
	}
}

// TC-040-06 (L5): real-block round trip.
// Produces a chain via BlockSink, runs VerifyChain (asserts Valid==true),
// tampers a byte on disk, runs VerifyChain again (asserts Valid==false, TamperedAt set).
func TestVerifyChainRealBlockRoundTrip(t *testing.T) {
	binPath := findAuditTrailBinary()
	if binPath == "" {
		t.Skip("real audit-trail binary not available; running recorded-exec fallback via TC-040-01/02")
	}
	t.Logf("TC-040-06 L5 real-binary path: using %s", binPath)

	logfile := filepath.Join(t.TempDir(), "audit-040-l5.log")

	// Step 1: produce a chain via BlockSink.
	sink := audit.NewBlockSink(binPath, logfile)
	events := []audit.AuditEvent{
		{Action: audit.ActionContainment, RunID: "run-040", TaskID: "040", Detail: audit.EventDetail{Launcher: "podman"}},
		{Action: audit.ActionPick, RunID: "run-040", TaskID: "040"},
		{Action: audit.ActionAttempt, RunID: "run-040", TaskID: "040", Detail: audit.EventDetail{Attempt: 1}},
		{Action: audit.ActionVerify, RunID: "run-040", TaskID: "040", Verdict: audit.VerdictPass},
		{Action: audit.ActionFinish, RunID: "run-040", TaskID: "040", Outcome: audit.OutcomeCompleted},
	}
	for i, ev := range events {
		if err := sink.Append(ev); err != nil {
			t.Fatalf("BlockSink.Append[%d] (%s) failed: %v", i, ev.Action, err)
		}
	}
	if err := sink.Seal(); err != nil {
		t.Fatalf("BlockSink.Seal failed: %v", err)
	}

	// Step 2: verify the intact chain — must be Valid==true.
	result, err := audit.VerifyChain(binPath, logfile)
	if err != nil {
		t.Fatalf("VerifyChain (intact) returned error: %v", err)
	}
	if !result.Valid {
		t.Fatalf("VerifyChain (intact): Valid=false, want true; message=%q", result.Message)
	}
	t.Logf("TC-040-06 intact: Valid=%v TamperedAt=%v Message=%q", result.Valid, result.TamperedAt, result.Message)

	// Step 3: tamper a byte in the on-disk logfile.
	data, err := os.ReadFile(logfile)
	if err != nil {
		t.Fatalf("ReadFile (pre-tamper): %v", err)
	}
	if len(data) < 10 {
		t.Fatalf("logfile too short to tamper: %d bytes", len(data))
	}
	// Flip one byte in the middle of the file (avoid first/last few bytes to stay in content).
	tamperIdx := len(data) / 2
	data[tamperIdx] ^= 0x01
	if err := os.WriteFile(logfile, data, 0o600); err != nil {
		t.Fatalf("WriteFile (post-tamper): %v", err)
	}

	// Step 4: verify the tampered chain — must be Valid==false with TamperedAt set.
	resultTampered, err := audit.VerifyChain(binPath, logfile)
	if err != nil {
		t.Fatalf("VerifyChain (tampered) returned error: %v", err)
	}
	if resultTampered.Valid {
		t.Errorf("VerifyChain (tampered): Valid=true, want false")
	}
	if resultTampered.TamperedAt == nil {
		t.Error("VerifyChain (tampered): TamperedAt is nil, want a non-nil seq from the block")
	} else {
		t.Logf("TC-040-06 tampered: Valid=%v TamperedAt=%v Message=%q",
			resultTampered.Valid, *resultTampered.TamperedAt, resultTampered.Message)
	}
	t.Log("TC-040-06 L5 PASS: intact chain valid; tampered chain invalid with TamperedAt set")
}

// findAuditTrailBinary returns the audit-trail binary path from the
// AGENT_BUILDER_AUDIT_BIN environment variable, or "" if unset.
func findAuditTrailBinary() string {
	return os.Getenv("AGENT_BUILDER_AUDIT_BIN")
}
