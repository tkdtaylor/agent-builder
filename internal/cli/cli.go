// Package cli implements the agent-builder command-line surface.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe/docsfix"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

var _ = docsfix.DocsFixGate{} // ensure init() is triggered by importing docsfix

const (
	ExitOK      = 0
	ExitGeneric = 1
	ExitUsage   = 2
)

type Runner func() error

type Verifier interface {
	Verify(repoPath string) gate.Verdict
}

type Config struct {
	Args    []string
	Stdout  io.Writer
	Stderr  io.Writer
	Stdin   io.Reader
	Version string
	Run     Runner
	Gate    Verifier
}

func Main(config Config) int {
	if config.Stdout == nil {
		config.Stdout = io.Discard
	}
	if config.Stderr == nil {
		config.Stderr = io.Discard
	}
	if config.Version == "" {
		config.Version = supervisor.Version
	}
	if config.Run == nil {
		config.Run = func() error {
			return runtimewiring.RunFromEnv(config.Stdout)
		}
	}
	if config.Gate == nil {
		config.Gate = newProductionGate()
	}

	if len(config.Args) == 0 {
		printUsage(config.Stderr)
		return ExitUsage
	}

	switch config.Args[0] {
	case "-h", "--help", "help":
		printUsage(config.Stdout)
		return ExitOK
	case "version":
		return runVersion(config, config.Args[1:])
	case "run":
		return runLoop(config, config.Args[1:])
	case "verify":
		return runVerify(config, config.Args[1:])
	case "verify-checkpoint":
		return runVerifyCheckpoint(config, config.Args[1:])
	case "orchestrate":
		return runOrchestrate(config, config.Args[1:])
	default:
		writef(config.Stderr, "usage error: unknown subcommand %q\n\n", config.Args[0])
		printUsage(config.Stderr)
		return ExitUsage
	}
}

func runVersion(config Config, args []string) int {
	flags := newFlagSet("version", config.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			versionUsage(config.Stdout)
			return ExitOK
		}
		return usage(config.Stderr, err)
	}
	if flags.NArg() != 0 {
		return usage(config.Stderr, fmt.Errorf("version accepts no arguments"))
	}
	writef(config.Stdout, "agent-builder %s\n", config.Version)
	return ExitOK
}

func runLoop(config Config, args []string) int {
	flags := newFlagSet("run", config.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			runUsage(config.Stdout)
			return ExitOK
		}
		return usage(config.Stderr, err)
	}
	if flags.NArg() != 0 {
		return usage(config.Stderr, fmt.Errorf("run accepts no arguments"))
	}
	if err := config.Run(); err != nil {
		writef(config.Stderr, "error: %v\n", err)
		return ExitGeneric
	}
	return ExitOK
}

func runVerify(config Config, args []string) int {
	flags := newFlagSet("verify", config.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			verifyUsage(config.Stdout)
			return ExitOK
		}
		return usage(config.Stderr, err)
	}
	if flags.NArg() != 1 {
		return usage(config.Stderr, fmt.Errorf("verify requires exactly one repo argument"))
	}

	repoPath := filepath.Clean(flags.Arg(0))
	verdict := config.Gate.Verify(repoPath)
	for _, result := range verdict.Results {
		status := "PASS"
		if !result.OK {
			status = "FAIL"
		}
		writef(config.Stdout, "%s %s\n", status, result.Name)
		output := strings.TrimSpace(result.Output)
		if output != "" {
			writeln(config.Stdout, output)
		}
	}
	if verdict.OK {
		writef(config.Stdout, "verification passed: %s\n", repoPath)
		return ExitOK
	}
	writef(config.Stderr, "verification failed: %s\n", repoPath)
	return ExitGeneric
}

func runVerifyCheckpoint(config Config, args []string) int {
	flags := newFlagSet("verify-checkpoint", config.Stderr)
	checkpointPath := flags.String("checkpoint", "", "path to the SignedCheckpoint JSON file (required)")
	publicKeyPath := flags.String("public-key", "", "path to the PEM Ed25519 public key file (required)")
	logfile := flags.String("logfile", "", "optional chain logfile for live cross-check")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			verifyCheckpointUsage(config.Stdout)
			return ExitOK
		}
		return usage(config.Stderr, err)
	}
	if flags.NArg() != 0 {
		return usage(config.Stderr, fmt.Errorf("verify-checkpoint accepts no positional arguments"))
	}
	if *checkpointPath == "" {
		return usage(config.Stderr, fmt.Errorf("--checkpoint is required"))
	}
	if *publicKeyPath == "" {
		return usage(config.Stderr, fmt.Errorf("--public-key is required"))
	}

	binPath, err := resolveAuditBin()
	if err != nil {
		return usage(config.Stderr, fmt.Errorf("audit binary resolution failed: %w", err))
	}

	verifier := audit.NewCheckpointVerifier(binPath, *checkpointPath, *publicKeyPath, *logfile)
	result, err := verifier.VerifyCheckpoint()
	if err != nil {
		writef(config.Stderr, "error: %v\n", err)
		return ExitUsage
	}
	if result.Valid {
		writef(config.Stdout, `{"valid":true,"message":%q}`+"\n", result.Message)
		return ExitOK
	}
	writef(config.Stdout, `{"valid":false,"message":%q}`+"\n", result.Message)
	return ExitGeneric
}

// resolveAuditBin returns the path to the audit-trail binary. It checks
// AGENT_BUILDER_AUDIT_BIN first, then falls back to "audit-trail" on PATH.
// This mirrors the same resolution used by the existing "verify" subcommand
// wiring in internal/runtime.
func resolveAuditBin() (string, error) {
	if bin := os.Getenv("AGENT_BUILDER_AUDIT_BIN"); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("AGENT_BUILDER_AUDIT_BIN=%q: %w", bin, err)
		}
		return bin, nil
	}
	bin, err := exec.LookPath("audit-trail")
	if err != nil {
		return "", fmt.Errorf("audit-trail not found on PATH and AGENT_BUILDER_AUDIT_BIN not set: %w", err)
	}
	return bin, nil
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	return flags
}

func usage(stderr io.Writer, err error) int {
	writef(stderr, "usage error: %v\n", err)
	return ExitUsage
}

func printUsage(w io.Writer) {
	write(w, `Usage:
  agent-builder run
  agent-builder orchestrate
  agent-builder version
  agent-builder verify <repo>
  agent-builder verify-checkpoint --checkpoint <path> --public-key <path> [--logfile <path>]

Subcommands:
  run                   dispatch one supervisor loop
  orchestrate           drive the Tier-1 orchestrator: goal-intake -> plan -> N workers
  version               print the agent-builder version
  verify <repo>         run the verification gate against a repo
  verify-checkpoint     verify a signed checkpoint against an Ed25519 public key

Exit codes:
  0  success
  1  generic error
  2  usage error
`)
}

func versionUsage(w io.Writer) {
	write(w, "Usage: agent-builder version\n")
}

func runUsage(w io.Writer) {
	write(w, "Usage: agent-builder run\n")
}

func verifyUsage(w io.Writer) {
	write(w, "Usage: agent-builder verify <repo>\n")
}

func verifyCheckpointUsage(w io.Writer) {
	write(w, "Usage: agent-builder verify-checkpoint --checkpoint <path> --public-key <path> [--logfile <path>]\n")
}

func write(w io.Writer, text string) {
	_, _ = fmt.Fprint(w, text)
}

func writef(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func newProductionGate() Verifier {
	g, err := gate.New(
		gate.GoBuildStep{},
		gate.GoVetStep{},
		gate.GoTestStep{},
		gate.GoFmtStep{},
		gate.GolangciLintStep{},
		gate.DepScanStep{},
		gate.CodeScannerStep{},
	)
	if err != nil {
		panic(fmt.Sprintf("construct production gate: %v", err))
	}
	return g
}
