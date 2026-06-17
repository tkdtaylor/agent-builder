package audit

// VerifyChain invokes "audit-trail verify --logfile <path>" via os/exec, parses
// the block's JSON verdict, and returns a typed Result. It is the agent-builder
// side of the block-severity integrity gate: the block owns the tamper detection;
// this helper owns only the subprocess invocation and JSON-to-typed-result mapping.
//
// Governing ADR: docs/architecture/decisions/026-audit-trail-consume-shipped-block.md
//
// CLI surface (frozen v1 contract, docs/CONTRACT.md in the audit-trail block):
//
//	audit-trail verify -logfile <path>
//	Stdout: {"valid": bool, "tamper_detected_at": <int|null>, "message": string}
//	Exit 0 when valid, exit 1 when tampered.
//
// Error semantics:
//   - A parseable block response with "valid": false → Result.Valid==false, nil error.
//     The caller maps this to a block-severity gate failure via Result.IsTampered().
//   - A missing/non-executable binary, unreadable logfile, or block output that is
//     not parseable JSON → non-nil error wrapping ErrVerifierUnavailable. The caller
//     must never interpret this as "valid".
//
// Leaf-package guarantee: this file imports only stdlib packages (os/exec,
// encoding/json, errors, fmt). No audit-trail Go import, no executor/LLM/web import.
// The F-005 fitness check (task 042) enforces this.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// ErrVerifierUnavailable is the sentinel error returned when the verifier cannot
// be invoked or cannot produce a parseable response — i.e., an infrastructure
// failure distinct from a clean "verified and tampered" verdict. Callers must
// use errors.Is to distinguish "cannot verify" from "verified and tampered".
var ErrVerifierUnavailable = errors.New("audit: verifier unavailable")

// VerifyResult is the typed result of a VerifyChain call. It maps the block's
// JSON verdict onto Go-native fields.
type VerifyResult struct {
	// Valid is true when the block reported "valid": true (chain intact).
	Valid bool
	// TamperedAt is the seq of the first tampered entry reported by the block,
	// or nil when the chain is intact (Valid==true) or unknown.
	TamperedAt *int
	// Message is the human-readable message from the block's verify response.
	Message string
}

// IsTampered returns true when the block detected a tamper (Valid==false). This
// is the block-severity gate predicate: a true return must produce a non-zero /
// gate-fail outcome and name the TamperedAt seq when the block provides it.
func (r VerifyResult) IsTampered() bool {
	return !r.Valid
}

// verifyResponse is the JSON shape returned by "audit-trail verify".
// Fields mirror the block's frozen contract (docs/CONTRACT.md in audit-trail).
type verifyResponse struct {
	Valid      bool   `json:"valid"`
	TamperedAt *int   `json:"tamper_detected_at"`
	Message    string `json:"message"`
}

// VerifyChain invokes "audit-trail verify -logfile <logfile>" using the real
// os/exec path. For tests, use VerifyChainWithRunner to inject a stub runner.
func VerifyChain(binPath, logfile string) (VerifyResult, error) {
	return VerifyChainWithRunner(binPath, logfile, &verifyExecRunner{binPath: binPath})
}

// VerifyChainWithRunner invokes the block's verify verb through the supplied
// ExecRunner seam. The ExecRunner is the same interface used by BlockSink,
// keeping the subprocess abstraction uniform across the package.
//
// The runner is called with args ["verify", "-logfile", logfile]. It must:
//   - Return (stdout, nil) when the block exits 0 (valid chain).
//   - Return (stdout, non-nil) when the block exits non-zero (tampered or error).
func VerifyChainWithRunner(binPath, logfile string, runner ExecRunner) (VerifyResult, error) {
	args := []string{"verify", "-logfile", logfile}
	out, runErr := runner.Run(args)

	// Attempt to parse the block's JSON response regardless of exit code.
	// The block exits 1 on a tamper but still writes parseable JSON to stdout.
	var resp verifyResponse
	if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
		// JSON parse failed — this is an infrastructure failure, not a verdict.
		// Either the binary doesn't exist, the logfile is unreadable, or the block
		// wrote something unexpected. Always surface as ErrVerifierUnavailable.
		//
		// If runErr already wraps ErrVerifierUnavailable (e.g. binary not found),
		// return it directly to avoid double-wrapping the sentinel.
		if runErr != nil {
			if errors.Is(runErr, ErrVerifierUnavailable) {
				return VerifyResult{Valid: false}, runErr
			}
			return VerifyResult{Valid: false}, fmt.Errorf("%w: %s: %w", ErrVerifierUnavailable, binPath, runErr)
		}
		return VerifyResult{Valid: false}, fmt.Errorf("%w: %s: unparseable response %q: %w", ErrVerifierUnavailable, binPath, out, jsonErr)
	}

	// JSON was parseable. Map to the typed result via direct struct conversion —
	// verifyResponse and VerifyResult have identical field names and types.
	result := VerifyResult(resp)

	// When Valid==true, ignore the exec error (block exit 0 is expected; any
	// mismatch is odd but the parseable JSON takes precedence for the verdict).
	//
	// When Valid==false AND we have a parseable JSON response, it is a clean
	// block-detected tamper: return nil error so callers distinguish "tamper"
	// from "cannot verify".
	return result, nil
}

// verifyExecRunner is the real ExecRunner for VerifyChain that shells out to
// the audit-trail binary with the given arguments.
type verifyExecRunner struct {
	binPath string
}

func (v *verifyExecRunner) Run(args []string) ([]byte, error) {
	cmd := exec.Command(v.binPath, args...) //nolint:gosec // path is caller-supplied
	// Use Output() which captures stdout; exit code 1 becomes an *exec.ExitError.
	// CombinedOutput would mix stderr into the JSON; use Output() to keep stdout clean.
	out, err := cmd.Output()
	if err != nil {
		// For ExitError, we still want the stdout the block wrote before exiting.
		// cmd.Output() captures stdout in out even when there's an ExitError, so
		// we return both — the caller decides whether the stdout is parseable JSON.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// out already contains stdout; return it alongside the error so the
			// caller can attempt JSON parsing (the block writes JSON then exits 1
			// on a tamper).
			return out, err
		}
		// Non-ExitError (e.g. binary not found, permission denied): return the
		// error directly with empty output.
		return nil, fmt.Errorf("%w: %s: %w", ErrVerifierUnavailable, v.binPath, err)
	}
	return out, nil
}
