package audit_test

// Tests for BlockSink: the production audit.Sink implementation that maps
// AuditEvent values onto audit-trail emit CLI subprocess calls.
//
// TC-039-01: BlockSink satisfies audit.Sink at compile time
// TC-039-02: each AuditAction maps to the correct emit argv
// TC-039-03: non-zero exit or malformed response surfaces as an error
// TC-039-04: Append after Seal fails; validation-failing event spawns no subprocess; Seal idempotent
// TC-039-05: block unavailability is a hard, named error
// TC-039-07: real binary path — chain the seven events and verify with audit-trail verify

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
)

// TC-039-01: BlockSink satisfies audit.Sink at compile time.
var _ audit.Sink = (*audit.BlockSink)(nil)

// recordingRunner captures argv and returns canned responses. It implements
// the audit.ExecRunner seam injected into BlockSink for unit tests.
type recordingRunner struct {
	calls     [][]string // each call's argv slice
	stdout    string     // canned stdout to return (default: valid JSON)
	exitErr   error      // non-nil to simulate a failing exec
	malformed bool       // return non-JSON on success
}

func (r *recordingRunner) Run(args []string) ([]byte, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if r.exitErr != nil {
		return nil, r.exitErr
	}
	if r.malformed {
		return []byte("not-json"), nil
	}
	// Default: return a valid {hash,seq} response.
	seq := len(r.calls) - 1
	out, _ := json.Marshal(map[string]any{"seq": seq, "hash": fmt.Sprintf("hash%d", seq)})
	if r.stdout != "" {
		return []byte(r.stdout), nil
	}
	return out, nil
}

func newRecordingRunner() *recordingRunner {
	return &recordingRunner{}
}

// makeEventFor creates a minimal valid AuditEvent for the given action.
func makeEventFor(action audit.AuditAction) audit.AuditEvent {
	ev := audit.AuditEvent{
		Action: action,
		RunID:  "run-039",
		TaskID: "039",
	}
	switch action {
	case audit.ActionVerify:
		ev.Verdict = audit.VerdictPass
	case audit.ActionFinish:
		ev.Outcome = audit.OutcomeCompleted
	case audit.ActionContainment:
		ev.Detail = audit.EventDetail{Launcher: "podman"}
	case audit.ActionPublish:
		ev.Detail = audit.EventDetail{Branch: "task/039-audit-chain-writer", Remote: "origin"}
	case audit.ActionAttempt, audit.ActionEscalate:
		ev.Detail = audit.EventDetail{Attempt: 1}
	}
	return ev
}

// findArg returns the value following a flag name in an argv slice, or "" if absent.
func findArg(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// hasArg returns true if a flag name appears in the argv slice.
func hasArg(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

// TC-039-02: each AuditAction maps to the correct emit argv.
func TestBlockSinkEmitArgs(t *testing.T) {
	logfile := "/tmp/test-039.log"

	type wantArgs struct {
		action   string
		decision string // empty means no --decision flag expected
	}

	cases := []struct {
		ev   audit.AuditEvent
		want wantArgs
	}{
		{
			ev:   makeEventFor(audit.ActionContainment),
			want: wantArgs{action: "containment"},
		},
		{
			ev:   makeEventFor(audit.ActionPick),
			want: wantArgs{action: "pick"},
		},
		{
			ev:   makeEventFor(audit.ActionAttempt),
			want: wantArgs{action: "attempt"},
		},
		{
			ev:   makeEventFor(audit.ActionVerify),
			want: wantArgs{action: "verify", decision: "pass"},
		},
		{
			ev:   makeEventFor(audit.ActionPublish),
			want: wantArgs{action: "publish"},
		},
		{
			ev:   makeEventFor(audit.ActionEscalate),
			want: wantArgs{action: "escalate"},
		},
		{
			ev:   makeEventFor(audit.ActionFinish),
			want: wantArgs{action: "finish", decision: "completed"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(string(tc.ev.Action), func(t *testing.T) {
			r := newRecordingRunner()
			sink := audit.NewBlockSinkWithRunner(logfile, r)

			if err := sink.Append(tc.ev); err != nil {
				t.Fatalf("Append(%s) returned error: %v", tc.ev.Action, err)
			}
			if len(r.calls) != 1 {
				t.Fatalf("expected 1 exec call, got %d", len(r.calls))
			}
			args := r.calls[0]

			// Must start with "emit"
			if len(args) == 0 || args[0] != "emit" {
				t.Errorf("argv[0] = %q, want %q", safeFirst(args), "emit")
			}
			// --logfile
			if got := findArg(args, "-logfile"); got != logfile {
				t.Errorf("-logfile = %q, want %q", got, logfile)
			}
			// --actor contains "agent-builder"
			if got := findArg(args, "-actor"); !strings.Contains(got, "agent-builder") {
				t.Errorf("-actor = %q, want it to contain 'agent-builder'", got)
			}
			// --action matches the event's action string
			if got := findArg(args, "-action"); got != tc.want.action {
				t.Errorf("-action = %q, want %q", got, tc.want.action)
			}
			// --target must be non-empty
			if got := findArg(args, "-target"); got == "" {
				t.Errorf("-target is empty; must be set for %s", tc.ev.Action)
			}
			// --decision only when expected
			if tc.want.decision != "" {
				if got := findArg(args, "-decision"); got != tc.want.decision {
					t.Errorf("-decision = %q, want %q", got, tc.want.decision)
				}
			} else {
				if hasArg(args, "-decision") {
					t.Errorf("-decision must not be present for %s (no decision field)", tc.ev.Action)
				}
			}
		})
	}
}

// TC-039-02 (continued): verifying that pick and containment never include --decision.
func TestBlockSinkNoDecisionForNonVerifyNonFinish(t *testing.T) {
	r := newRecordingRunner()
	sink := audit.NewBlockSinkWithRunner("/tmp/test.log", r)

	for _, action := range []audit.AuditAction{
		audit.ActionContainment,
		audit.ActionPick,
		audit.ActionAttempt,
		audit.ActionPublish,
		audit.ActionEscalate,
	} {
		ev := makeEventFor(action)
		if err := sink.Append(ev); err != nil {
			t.Fatalf("Append(%s) returned error: %v", action, err)
		}
	}
	for i, call := range r.calls {
		if hasArg(call, "-decision") {
			action := findArg(call, "-action")
			t.Errorf("call[%d] action=%q has -decision flag but should not", i, action)
		}
	}
}

// TC-039-03: non-zero exit surfaces as an error.
func TestBlockSinkNonZeroExitIsError(t *testing.T) {
	r := newRecordingRunner()
	r.exitErr = &exec.ExitError{} // simulate non-zero exit
	sink := audit.NewBlockSinkWithRunner("/tmp/test.log", r)

	err := sink.Append(makeEventFor(audit.ActionPick))
	if err == nil {
		t.Fatal("Append returned nil when exec failed; expected non-nil error")
	}
}

// TC-039-03: malformed response (non-JSON) surfaces as an error.
func TestBlockSinkMalformedResponseIsError(t *testing.T) {
	r := newRecordingRunner()
	r.malformed = true
	sink := audit.NewBlockSinkWithRunner("/tmp/test.log", r)

	err := sink.Append(makeEventFor(audit.ActionPick))
	if err == nil {
		t.Fatal("Append returned nil for malformed (non-JSON) response; expected non-nil error")
	}
}

// TC-039-03: valid {seq,hash} response is accepted and seq increments.
func TestBlockSinkValidResponseAccepted(t *testing.T) {
	r := newRecordingRunner()
	sink := audit.NewBlockSinkWithRunner("/tmp/test.log", r)

	actions := []audit.AuditEvent{
		makeEventFor(audit.ActionPick),
		makeEventFor(audit.ActionAttempt),
	}
	for i, ev := range actions {
		if err := sink.Append(ev); err != nil {
			t.Fatalf("Append[%d] returned error: %v", i, err)
		}
	}
	if len(r.calls) != 2 {
		t.Fatalf("expected 2 exec calls, got %d", len(r.calls))
	}
}

// TC-039-04: Append after Seal fails.
func TestBlockSinkAppendAfterSealFails(t *testing.T) {
	r := newRecordingRunner()
	sink := audit.NewBlockSinkWithRunner("/tmp/test.log", r)

	if err := sink.Append(makeEventFor(audit.ActionPick)); err != nil {
		t.Fatalf("first Append returned error: %v", err)
	}
	if err := sink.Seal(); err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	err := sink.Append(makeEventFor(audit.ActionAttempt))
	if err == nil {
		t.Fatal("Append after Seal returned nil; expected non-nil error")
	}
	if !errors.Is(err, audit.ErrAfterSeal) {
		t.Errorf("Append after Seal returned %v; want ErrAfterSeal", err)
	}
}

// TC-039-04: validation-failing event spawns no subprocess.
func TestBlockSinkValidationFailingEventNoSubprocess(t *testing.T) {
	r := newRecordingRunner()
	sink := audit.NewBlockSinkWithRunner("/tmp/test.log", r)

	// Verify event without a verdict — invalid.
	badEv := audit.AuditEvent{
		Action: audit.ActionVerify,
		RunID:  "r1",
		TaskID: "1",
		// Verdict intentionally absent
	}
	err := sink.Append(badEv)
	if err == nil {
		t.Fatal("Append with invalid event returned nil; expected non-nil error")
	}
	if len(r.calls) != 0 {
		t.Errorf("validation-failing event invoked %d subprocesses; expected 0", len(r.calls))
	}
}

// TC-039-04: second Seal does not panic.
func TestBlockSinkSealIsIdempotent(t *testing.T) {
	r := newRecordingRunner()
	sink := audit.NewBlockSinkWithRunner("/tmp/test.log", r)
	_ = sink.Append(makeEventFor(audit.ActionPick))
	if err := sink.Seal(); err != nil {
		t.Fatalf("first Seal returned error: %v", err)
	}
	// Second seal must not panic and must return at most one error.
	defer func() {
		if rec := recover(); rec != nil {
			t.Errorf("second Seal panicked: %v", rec)
		}
	}()
	_ = sink.Seal()
}

// TC-039-05, TC-039-06: a missing binary path is a hard, named error at Append time.
func TestBlockSinkMissingBinaryIsError(t *testing.T) {
	// Use the real exec path (no recording runner) by constructing with a
	// binary that does not exist.
	sink := audit.NewBlockSink("/nonexistent/audit-trail-does-not-exist", "/tmp/test.log")
	err := sink.Append(makeEventFor(audit.ActionPick))
	if err == nil {
		t.Fatal("Append with missing binary returned nil; expected non-nil error")
	}
	// Error must name the issue — check it's not a silent skip.
	if err.Error() == "" {
		t.Error("error message is empty; must identify the failing emit")
	}
}

// TC-039-05, TC-039-06: a non-executable binary path is a hard, named error.
func TestBlockSinkNonExecutableBinaryIsError(t *testing.T) {
	// Create a non-executable file in a temp dir.
	dir := t.TempDir()
	binPath := filepath.Join(dir, "audit-trail")
	if err := os.WriteFile(binPath, []byte("not-a-binary"), 0o444); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}
	// Ensure it is definitely not executable (no +x).
	if err := os.Chmod(binPath, 0o444); err != nil {
		t.Fatalf("setup: Chmod: %v", err)
	}

	sink := audit.NewBlockSink(binPath, "/tmp/test.log")
	err := sink.Append(makeEventFor(audit.ActionPick))
	if err == nil {
		t.Fatal("Append with non-executable binary returned nil; expected non-nil error")
	}
}

// TC-039-07 (L5): real binary path — emit seven events and verify the chain.
// Gated behind AGENT_BUILDER_AUDIT_BIN env var. Falls back to argv recording
// assertion when the env var is not set.
func TestBlockSinkChainVerifies(t *testing.T) {
	binPath := os.Getenv("AGENT_BUILDER_AUDIT_BIN")
	if binPath == "" {
		// Fall back: use the well-known prebuilt path.
		candidate := "$HOME/Code/Public/audit-trail/audit-trail"
		if _, err := os.Stat(candidate); err == nil {
			binPath = candidate
		}
	}

	if binPath == "" {
		// No real binary available; assert argv correctness via recording runner instead.
		t.Log("AGENT_BUILDER_AUDIT_BIN not set and prebuilt path not found; running recorded-exec fallback")
		testBlockSinkChainVerifiesFallback(t)
		return
	}

	t.Logf("L5 real-binary path: using %s", binPath)

	logfile := filepath.Join(t.TempDir(), "audit-039.log")
	sink := audit.NewBlockSink(binPath, logfile)

	// Emit the seven lifecycle events in run order.
	events := []audit.AuditEvent{
		makeEventFor(audit.ActionContainment),
		makeEventFor(audit.ActionPick),
		makeEventFor(audit.ActionAttempt),
		{Action: audit.ActionVerify, RunID: "run-039", TaskID: "039", Verdict: audit.VerdictPass},
		makeEventFor(audit.ActionPublish),
		makeEventFor(audit.ActionEscalate),
		{Action: audit.ActionFinish, RunID: "run-039", TaskID: "039", Outcome: audit.OutcomeCompleted},
	}

	for i, ev := range events {
		if err := sink.Append(ev); err != nil {
			t.Fatalf("Append[%d] (%s) returned error: %v", i, ev.Action, err)
		}
	}
	if err := sink.Seal(); err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}

	// Run audit-trail verify and assert valid == true.
	out, err := exec.Command(binPath, "verify", "-logfile", logfile).Output()
	if err != nil {
		t.Fatalf("audit-trail verify failed: %v\noutput: %s", err, out)
	}
	var result struct {
		Valid bool `json:"valid"`
	}
	if jerr := json.Unmarshal(out, &result); jerr != nil {
		t.Fatalf("could not parse verify output %q: %v", out, jerr)
	}
	if !result.Valid {
		t.Errorf("audit-trail verify reported valid=false; chain is not intact\noutput: %s", out)
	}
	t.Logf("TC-039-07 L5 PASS: audit-trail verify output: %s", strings.TrimSpace(string(out)))
}

// testBlockSinkChainVerifiesFallback asserts argv correctness for the seven events
// using the recording runner seam when the real binary is unavailable.
func testBlockSinkChainVerifiesFallback(t *testing.T) {
	t.Helper()
	r := newRecordingRunner()
	sink := audit.NewBlockSinkWithRunner("/tmp/fallback-039.log", r)

	events := []audit.AuditEvent{
		makeEventFor(audit.ActionContainment),
		makeEventFor(audit.ActionPick),
		makeEventFor(audit.ActionAttempt),
		{Action: audit.ActionVerify, RunID: "run-039", TaskID: "039", Verdict: audit.VerdictPass},
		makeEventFor(audit.ActionPublish),
		makeEventFor(audit.ActionEscalate),
		{Action: audit.ActionFinish, RunID: "run-039", TaskID: "039", Outcome: audit.OutcomeCompleted},
	}

	for i, ev := range events {
		if err := sink.Append(ev); err != nil {
			t.Fatalf("Append[%d] (%s) returned error: %v", i, ev.Action, err)
		}
	}

	if len(r.calls) != 7 {
		t.Fatalf("expected 7 exec calls (one per event), got %d", len(r.calls))
	}

	expectedActions := []string{
		"containment", "pick", "attempt", "verify", "publish", "escalate", "finish",
	}
	for i, call := range r.calls {
		action := findArg(call, "-action")
		if action != expectedActions[i] {
			t.Errorf("call[%d]: -action = %q, want %q", i, action, expectedActions[i])
		}
		actor := findArg(call, "-actor")
		if !strings.Contains(actor, "agent-builder") {
			t.Errorf("call[%d]: -actor = %q; must contain 'agent-builder'", i, actor)
		}
		t.Logf("call[%d] argv: %v", i, call)
	}
	// verify and finish must carry -decision; others must not.
	for i, call := range r.calls {
		action := findArg(call, "-action")
		switch action {
		case "verify", "finish":
			if !hasArg(call, "-decision") {
				t.Errorf("call[%d] action=%s: -decision flag absent; required", i, action)
			}
		default:
			if hasArg(call, "-decision") {
				t.Errorf("call[%d] action=%s: -decision flag present; must not be set", i, action)
			}
		}
	}
	t.Log("TC-039-07 recorded-exec fallback PASS: all 7 argv sets verified")
}

func safeFirst(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}
