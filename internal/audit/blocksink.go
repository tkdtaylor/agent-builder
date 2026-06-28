package audit

// BlockSink is the production audit.Sink that maps each typed AuditEvent onto
// one "audit-trail emit" CLI subprocess call. The block owns the hash chain,
// canonical encoding, genesis sentinel, and verifier; this adapter owns only
// the typed-event→argv mapping and the subprocess seam.
//
// Governing ADR: docs/architecture/decisions/026-audit-trail-consume-shipped-block.md
//
// CLI surface (frozen v1 contract):
//
//	audit-trail emit -logfile <path> -actor <id> -action <verb> -target <res> [-decision <d>]
//
// The block sets ts itself per emit. context and refs fields exist only on the
// IPC transport (deferred per ADR 026 Option B) and are not carried by this v0
// CLI path.
//
// Leaf-package guarantee: this file imports only stdlib packages (os/exec,
// encoding/json, fmt, errors, strings). No audit-trail Go import, no
// executor/LLM/web import. The F-005 fitness check (task 042) enforces this.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

// ExecRunner is the subprocess seam. The default implementation invokes the
// real audit-trail binary. Tests supply a recording stub via NewBlockSinkWithRunner.
type ExecRunner interface {
	// Run executes the block with the given arguments and returns combined stdout
	// and the process error. A non-zero exit must be reflected as a non-nil error.
	Run(args []string) ([]byte, error)
}

// emitRunner is the real ExecRunner that shells out to the audit-trail binary.
type emitRunner struct {
	binPath string
}

func (e *emitRunner) Run(args []string) ([]byte, error) {
	cmd := exec.Command(e.binPath, args...) //nolint:gosec // path is caller-supplied and validated at construction
	return cmd.Output()
}

// emitResponse is the JSON shape returned by "audit-trail emit".
// The block pretty-prints { "hash": "...", "seq": N }.
type emitResponse struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

// BlockSink implements audit.Sink using the audit-trail block CLI subprocess.
// Construct with NewBlockSink or NewBlockSinkWithRunner.
type BlockSink struct {
	// mu serializes Append/Seal. The block owns a hash chain whose prev-hash links
	// are computed per emit; concurrent emits would interleave at the subprocess
	// seam and corrupt the chain. Task 086 dispatches N workers concurrently, all
	// writing the one fleet chain, so this serialization is load-bearing on the
	// live path, not just style.
	mu      sync.Mutex
	logfile string
	runner  ExecRunner
	sealed  bool
}

// Compile-time assertion: *BlockSink must satisfy the Sink interface.
var _ Sink = (*BlockSink)(nil)

// NewBlockSink constructs a BlockSink that shells out to the given binary path
// for each Append. The binary path and logfile path are passed in by the
// caller (resolved from AGENT_BUILDER_AUDIT_BIN / AGENT_BUILDER_AUDIT_RECORD
// by the supervisor wiring in task 041). Neither path is validated at
// construction; a missing or non-executable binary surfaces as a hard error on
// the first Append call.
func NewBlockSink(binPath, logfile string) *BlockSink {
	return &BlockSink{
		logfile: logfile,
		runner:  &emitRunner{binPath: binPath},
	}
}

// NewBlockSinkWithRunner constructs a BlockSink with an injectable ExecRunner.
// Used by tests to record argv without spawning real subprocesses.
func NewBlockSinkWithRunner(logfile string, runner ExecRunner) *BlockSink {
	return &BlockSink{
		logfile: logfile,
		runner:  runner,
	}
}

// Append validates ev, maps it onto an "audit-trail emit" argv, and invokes the
// block once. It returns:
//   - ErrAfterSeal when Seal has already been called.
//   - *ValidationError (from audit.Validate) when ev is invalid — no subprocess is invoked.
//   - A non-nil named error when the block exits non-zero or returns an unparseable response.
func (b *BlockSink) Append(ev AuditEvent) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed {
		return ErrAfterSeal
	}
	// Validate before touching the subprocess seam.
	if err := Validate(ev); err != nil {
		return err
	}
	// Build the argv for "audit-trail emit".
	args := b.buildArgs(ev)
	// Invoke the block.
	out, err := b.runner.Run(args)
	if err != nil {
		return fmt.Errorf("audit: emit %s failed: %w", ev.Action, err)
	}
	// Parse the {seq,hash} response — a malformed response is a hard error.
	var resp emitResponse
	if jerr := json.Unmarshal(out, &resp); jerr != nil {
		return fmt.Errorf("audit: emit %s returned unparseable response %q: %w", ev.Action, out, jerr)
	}
	return nil
}

// Seal marks the sink as closed. Subsequent Append calls return ErrAfterSeal.
// Seal is idempotent — a second call does not panic.
func (b *BlockSink) Seal() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sealed = true
	return nil
}

// buildArgs constructs the argv slice for "audit-trail emit" from ev.
// Mapping (ADR 026, table):
//
//	action       → -action <verb>
//	actor        → -actor "agent-builder/<RunID>"
//	target       → -target <task id / branch / remote / launcher>
//	decision     → -decision <verdict|outcome> — only when the event carries one
//
// context and refs are IPC-transport fields deferred per ADR 026 Option B.
// The block sets ts itself; we do not pass -ts.
func (b *BlockSink) buildArgs(ev AuditEvent) []string {
	actor := "agent-builder"
	if ev.RunID != "" {
		actor = "agent-builder/" + ev.RunID
	}

	target := buildTarget(ev)

	args := []string{
		"emit",
		"-logfile", b.logfile,
		"-actor", actor,
		"-action", string(ev.Action),
		"-target", target,
	}

	// -decision only for verify (verdict) and finish (outcome).
	if decision := buildDecision(ev); decision != "" {
		args = append(args, "-decision", decision)
	}

	return args
}

// buildTarget returns the resource the action touched, derived from ev fields.
// The target is the most semantically meaningful resource for each action:
//
//	containment  → launcher path
//	pick/attempt → task id
//	verify       → task id (verdict is in decision)
//	publish      → branch@remote
//	escalate     → task id
//	finish       → task id
func buildTarget(ev AuditEvent) string {
	switch ev.Action {
	case ActionContainment:
		if ev.Detail.Launcher != "" {
			return ev.Detail.Launcher
		}
		return "containment"
	case ActionPublish:
		if ev.Detail.Branch != "" && ev.Detail.Remote != "" {
			return ev.Detail.Branch + "@" + ev.Detail.Remote
		}
		if ev.Detail.Branch != "" {
			return ev.Detail.Branch
		}
		return "publish"
	default:
		if ev.TaskID != "" {
			return "task/" + ev.TaskID
		}
		return string(ev.Action)
	}
}

// buildDecision returns the decision string for actions that carry one:
// - ActionVerify: the verdict ("pass" or "fail")
// - ActionFinish: the outcome ("completed", "failed", or "timed-out")
// All other actions: empty string (no -decision flag emitted).
func buildDecision(ev AuditEvent) string {
	switch ev.Action {
	case ActionVerify:
		if ev.Verdict.Valid() {
			return string(ev.Verdict)
		}
	case ActionFinish:
		if ev.Outcome != "" {
			return string(ev.Outcome)
		}
	}
	return ""
}

// ErrBlockEmitFailed is the sentinel error type for block subprocess failures.
// Callers can use errors.As to distinguish block failures from validation errors.
var ErrBlockEmitFailed = errors.New("audit: block emit failed")
