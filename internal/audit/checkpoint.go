package audit

// CheckpointSigner wraps the "audit-trail checkpoint create" CLI subprocess
// using the same ExecRunner seam as BlockSink. It is called after VerifyChain
// passes at supervisor seal time to produce a cryptographic attestation of the
// sealed audit chain.
//
// Governing ADR: docs/architecture/decisions/037-checkpoint-signer-seam.md
//
// CLI surface (frozen v1 contract):
//
//	audit-trail checkpoint create \
//	  --logfile <path> \
//	  --log-id  <id> \
//	  --signing-key <key.pem> \
//	  [--out <path.json>]
//
// Exit 0 = checkpoint created; exit non-zero = error (message on stderr).
// When --out is omitted the checkpoint JSON is written to stdout.
//
// Leaf-package guarantee: this file imports only stdlib packages (os/exec, fmt).
// No audit-trail Go import, no executor/LLM/web import. The F-005 fitness check
// (task 042) enforces this via "go list -deps ./internal/audit/...".

import (
	"fmt"
	"os/exec"
)

// CheckpointSigner calls "audit-trail checkpoint create" to produce a signed
// checkpoint attestation of the sealed audit chain. Construct with
// NewCheckpointSigner or NewCheckpointSignerWithRunner.
//
// The verifyRunner field holds a separate ExecRunner for the pre-checkpoint
// VerifyChain call. In the production path (NewCheckpointSigner) it shares the
// same binary via a second checkpointRunner; in tests it can be a separate fake.
type CheckpointSigner struct {
	logfile        string
	logID          string
	signingKeyPath string
	outPath        string // empty = stdout
	runner         ExecRunner
	verifyRunner   ExecRunner // for VerifyChain before CreateCheckpoint
}

// checkpointRunner is the real ExecRunner for CheckpointSigner that shells out
// to the audit-trail binary with the given arguments.
type checkpointRunner struct {
	binPath string
}

func (r *checkpointRunner) Run(args []string) ([]byte, error) {
	cmd := exec.Command(r.binPath, args...) //nolint:gosec // path is caller-supplied and validated at construction
	return cmd.Output()
}

// NewCheckpointSigner constructs a CheckpointSigner that shells out to the
// given audit-trail binary path for VerifyChain and CreateCheckpoint. The
// binary path is resolved and validated before dispatch by
// resolveCheckpointConfig in internal/runtime/run.go.
func NewCheckpointSigner(binPath, logfile, logID, signingKeyPath, outPath string) *CheckpointSigner {
	r := &checkpointRunner{binPath: binPath}
	return &CheckpointSigner{
		logfile:        logfile,
		logID:          logID,
		signingKeyPath: signingKeyPath,
		outPath:        outPath,
		runner:         r,
		verifyRunner:   r, // same binary; shared runner is safe (stateless)
	}
}

// NewCheckpointSignerWithRunner constructs a CheckpointSigner with an
// injectable ExecRunner for both VerifyChain and CreateCheckpoint. Used by
// tests to record argv without spawning real subprocesses.
func NewCheckpointSignerWithRunner(logfile, logID, signingKeyPath, outPath string, runner ExecRunner) *CheckpointSigner {
	return &CheckpointSigner{
		logfile:        logfile,
		logID:          logID,
		signingKeyPath: signingKeyPath,
		outPath:        outPath,
		runner:         runner,
		verifyRunner:   runner,
	}
}

// NewCheckpointSignerWithRunners constructs a CheckpointSigner with separate
// injectable ExecRunners for VerifyChain and CreateCheckpoint. Used by tests
// that need to assert the two calls independently.
func NewCheckpointSignerWithRunners(logfile, logID, signingKeyPath, outPath string, verifyRunner, checkpointRunner ExecRunner) *CheckpointSigner {
	return &CheckpointSigner{
		logfile:        logfile,
		logID:          logID,
		signingKeyPath: signingKeyPath,
		outPath:        outPath,
		runner:         checkpointRunner,
		verifyRunner:   verifyRunner,
	}
}

// VerifyChain calls the audit-trail verify verb on the logfile and returns the
// result. The supervisor calls this after Seal() to gate CreateCheckpoint: a
// tampered chain must not receive a signed checkpoint attestation.
func (c *CheckpointSigner) VerifyChain() (VerifyResult, error) {
	return VerifyChainWithRunner("", c.logfile, c.verifyRunner)
}

// CreateCheckpoint builds the "checkpoint create" argv and delegates to the
// ExecRunner. A non-zero runner exit returns a non-nil error wrapping the
// runner error.
//
// Argv structure:
//
//	["checkpoint", "create", "--logfile", <logfile>, "--log-id", <logID>,
//	 "--signing-key", <signingKeyPath>] + (["--out", <outPath>] if outPath != "")
func (c *CheckpointSigner) CreateCheckpoint() error {
	args := []string{
		"checkpoint", "create",
		"--logfile", c.logfile,
		"--log-id", c.logID,
		"--signing-key", c.signingKeyPath,
	}
	if c.outPath != "" {
		args = append(args, "--out", c.outPath)
	}
	_, err := c.runner.Run(args)
	if err != nil {
		return fmt.Errorf("audit: checkpoint create failed: %w", err)
	}
	return nil
}
