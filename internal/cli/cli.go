// Package cli implements the agent-builder command-line surface.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

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
			return supervisor.New().Run()
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
  agent-builder version
  agent-builder verify <repo>

Subcommands:
  run             dispatch one supervisor loop
  version         print the agent-builder version
  verify <repo>   run the verification gate against a repo

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
