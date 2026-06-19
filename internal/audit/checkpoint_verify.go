package audit

// CheckpointVerifier wraps the "audit-trail checkpoint verify" CLI subprocess
// using the same ExecRunner seam as CheckpointSigner (task 068) and BlockSink.
// It verifies an Ed25519-signed checkpoint attestation produced by CheckpointSigner
// without requiring access to the signing key — only the public key is needed.
//
// Governing ADR: docs/architecture/decisions/037-checkpoint-signer-seam.md
//
// CLI surface (frozen v1 contract):
//
//	audit-trail checkpoint verify \
//	  --checkpoint <path.json> \
//	  --public-key <pub.pem> \
//	  [--logfile <path>]
//
// Exit 0 = valid; exit 1 = invalid; exit 2 = usage error.
// Stdout: JSON {"valid": bool, "message": string}
//
// The --logfile flag is optional: when present it enables cross-checking the
// checkpoint against the live chain log. When absent, only the Ed25519 signature
// over the checkpoint payload is verified (pure offline verification).
//
// Error semantics mirror VerifyChain (verify.go):
//   - Parseable {"valid": false, ...} response → CheckpointVerifyResult{Valid: false}, nil error.
//     This is a clean cryptographic verdict, not an infrastructure failure.
//   - Unparseable output or binary-not-found → non-nil error wrapping
//     ErrCheckpointVerifierUnavailable; Valid: false. Callers must never treat an
//     error return as "valid".
//
// Leaf-package guarantee: this file imports only stdlib packages (encoding/json,
// errors, fmt). No audit-trail Go import, no executor/LLM/web import.
// The F-005 fitness check (task 042) enforces this via "go list -deps ./internal/audit/...".

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
)

// ErrCheckpointVerifierUnavailable is the sentinel error returned when the
// checkpoint verifier cannot be invoked or produces an unparseable response —
// an infrastructure failure distinct from a clean "invalid signature" verdict.
// Callers use errors.Is to distinguish "cannot verify" from "verified and invalid".
var ErrCheckpointVerifierUnavailable = errors.New("audit: checkpoint verifier unavailable")

// CheckpointVerifyResult is the typed result of a VerifyCheckpoint call.
// It maps the block's JSON verdict onto Go-native fields.
type CheckpointVerifyResult struct {
	// Valid is true when the block reported "valid": true (signature verified).
	Valid bool
	// Message is the human-readable message from the block's verify response.
	Message string
}

// checkpointVerifyResponse is the JSON shape returned by "audit-trail checkpoint verify".
// Fields mirror the frozen v1 contract.
type checkpointVerifyResponse struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}

// CheckpointVerifier calls "audit-trail checkpoint verify" to confirm that a
// signed checkpoint attestation was produced by the holder of the expected
// Ed25519 signing key. Construct with NewCheckpointVerifier or
// NewCheckpointVerifierWithRunner.
type CheckpointVerifier struct {
	checkpointPath string
	publicKeyPath  string
	logfile        string // optional; empty = offline-only verification
	runner         ExecRunner
}

// checkpointVerifyRunner is the real ExecRunner for CheckpointVerifier that
// shells out to the audit-trail binary with the given arguments.
type checkpointVerifyRunner struct {
	binPath string
}

func (r *checkpointVerifyRunner) Run(args []string) ([]byte, error) {
	cmd := exec.Command(r.binPath, args...) //nolint:gosec // path is caller-supplied and validated at construction
	out, err := cmd.Output()
	if err != nil {
		// Return stdout alongside the error so the caller can attempt JSON
		// parsing — the block writes JSON then exits 1 on an invalid signature.
		return out, err
	}
	return out, nil
}

// NewCheckpointVerifier constructs a CheckpointVerifier that shells out to the
// given audit-trail binary path. The binary path is resolved by the CLI layer
// (internal/cli) from AGENT_BUILDER_AUDIT_BIN or PATH before construction.
func NewCheckpointVerifier(binPath, checkpointPath, publicKeyPath, logfile string) *CheckpointVerifier {
	return &CheckpointVerifier{
		checkpointPath: checkpointPath,
		publicKeyPath:  publicKeyPath,
		logfile:        logfile,
		runner:         &checkpointVerifyRunner{binPath: binPath},
	}
}

// NewCheckpointVerifierWithRunner constructs a CheckpointVerifier with an
// injectable ExecRunner. Used by tests to record argv without spawning real
// subprocesses.
func NewCheckpointVerifierWithRunner(checkpointPath, publicKeyPath, logfile string, runner ExecRunner) *CheckpointVerifier {
	return &CheckpointVerifier{
		checkpointPath: checkpointPath,
		publicKeyPath:  publicKeyPath,
		logfile:        logfile,
		runner:         runner,
	}
}

// VerifyCheckpoint builds the "checkpoint verify" argv, invokes the runner,
// and maps the JSON stdout response to a CheckpointVerifyResult.
//
// Argv structure:
//
//	["checkpoint", "verify", "--checkpoint", <checkpointPath>,
//	 "--public-key", <publicKeyPath>]
//	+ (["--logfile", <logfile>] only when logfile is non-empty)
//
// A parseable "valid: false" response is a clean verdict (nil error).
// Unparseable output or binary-not-found returns a non-nil error wrapping
// ErrCheckpointVerifierUnavailable.
func (v *CheckpointVerifier) VerifyCheckpoint() (CheckpointVerifyResult, error) {
	args := []string{
		"checkpoint", "verify",
		"--checkpoint", v.checkpointPath,
		"--public-key", v.publicKeyPath,
	}
	if v.logfile != "" {
		args = append(args, "--logfile", v.logfile)
	}

	out, runErr := v.runner.Run(args)

	// Attempt JSON parse regardless of exit code — the block writes parseable
	// JSON to stdout even on exit 1 (invalid signature). This mirrors
	// VerifyChainWithRunner semantics: parseable + valid:false is a clean verdict.
	var resp checkpointVerifyResponse
	if jsonErr := json.Unmarshal(out, &resp); jsonErr != nil {
		// JSON parse failed — infrastructure failure (binary absent, output
		// malformed, etc.). Always surfaces as ErrCheckpointVerifierUnavailable.
		if runErr != nil {
			if errors.Is(runErr, ErrCheckpointVerifierUnavailable) {
				return CheckpointVerifyResult{Valid: false}, runErr
			}
			return CheckpointVerifyResult{Valid: false}, fmt.Errorf("%w: %w", ErrCheckpointVerifierUnavailable, runErr)
		}
		return CheckpointVerifyResult{Valid: false}, fmt.Errorf("%w: unparseable response %q: %w", ErrCheckpointVerifierUnavailable, out, jsonErr)
	}

	// JSON was parseable: map to typed result via direct struct conversion —
	// checkpointVerifyResponse and CheckpointVerifyResult have identical field
	// names and types (mirrors VerifyChainWithRunner in verify.go).
	// Ignore runErr — parseable JSON with valid:false is a clean block verdict,
	// not an infrastructure error.
	return CheckpointVerifyResult(resp), nil
}
