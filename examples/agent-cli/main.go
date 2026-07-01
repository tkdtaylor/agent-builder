// Package main implements the agent-cli operator tool — a laptop-side CLI that
// generates keys for Telegram operator auth, seals+signs commands for the orchestrator,
// and opens sealed replies.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/envelope"
)

const (
	ExitOK    = 0
	ExitUsage = 2
)

type Config struct {
	Args   []string
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
}

func Main(config Config) int {
	if config.Stdout == nil {
		config.Stdout = io.Discard
	}
	if config.Stderr == nil {
		config.Stderr = io.Discard
	}

	if len(config.Args) == 0 {
		writef(config.Stderr, "usage: agent-cli <subcommand> [options]\n\n")
		return ExitUsage
	}

	switch config.Args[0] {
	case "-h", "--help", "help":
		writef(config.Stdout, "usage: agent-cli <subcommand> [options]\n\n")
		writef(config.Stdout, "subcommands:\n")
		writef(config.Stdout, "  keygen              generate operator + orchestrator keypairs\n")
		return ExitOK
	case "keygen":
		return runKeygen(config, config.Args[1:])
	default:
		writef(config.Stderr, "usage error: unknown subcommand %q\n\n", config.Args[0])
		return ExitUsage
	}
}

func runKeygen(config Config, args []string) int {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // silence flag's default usage

	var keyfilePath string
	var force bool

	fs.StringVar(&keyfilePath, "keyfile", "", "path to write the operator keyfile")
	fs.BoolVar(&force, "force", false, "overwrite existing keyfile")

	if err := fs.Parse(args); err != nil {
		writef(config.Stderr, "usage error: %v\n", err)
		return ExitUsage
	}

	// Keyfile is mandatory
	if keyfilePath == "" {
		writef(config.Stderr, "usage error: --keyfile is required\n")
		return ExitUsage
	}

	// Check if file exists and --force not given
	if _, err := os.Stat(keyfilePath); err == nil && !force {
		writef(config.Stderr, "error: keyfile %q already exists; use --force to overwrite\n", keyfilePath)
		return ExitUsage
	}

	// Run the keygen command core (generates keys, writes files, writes output)
	// Return value is discarded here; tests use runKeygenCore directly for inspection
	_, exitCode := runKeygenCore(config.Stdout, config.Stderr, keyfilePath)
	return exitCode
}

// runKeygenCore executes the keygen command: generates keys, writes the keyfile,
// prints the env block to stdout and the banner+confirmation to stderr.
// Returns the generated KeyMaterial (for test inspection) and an exit code.
// This is the load-bearing seam for tests that need to inspect the exact keys
// that were printed to the output buffers.
func runKeygenCore(stdout, stderr io.Writer, keyfilePath string) (*KeyMaterial, int) {
	// Generate keys
	keys, err := GenerateKeys()
	if err != nil {
		writef(stderr, "error: failed to generate keys: %v\n", err)
		return nil, 1
	}

	// Write keyfile
	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		writef(stderr, "error: failed to write keyfile: %v\n", err)
		return nil, 1
	}

	// Render env block
	envBlock := RenderEnvBlock(keys)

	// Emit env block to stdout
	_, _ = fmt.Fprint(stdout, envBlock)

	// Emit confirmation to stderr
	writef(stderr, "\n--- paste into orchestrator environment ---\n\nkeyfile written to %s (mode 0600)\n", keyfilePath)

	return keys, ExitOK
}

func writef(w io.Writer, format string, args ...interface{}) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func main() {
	config := Config{
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Stdin:  os.Stdin,
	}
	os.Exit(Main(config))
}

// KeyMaterial holds the four keypairs generated for operator + orchestrator auth.
type KeyMaterial struct {
	OperatorEdPub   []byte // Ed25519 public key (32 bytes)
	OperatorEdPriv  []byte // Ed25519 private key (64 bytes)
	OperatorXPub    [32]byte
	OperatorXPriv   [32]byte
	OrchEdPub       []byte // Ed25519 public key (32 bytes)
	OrchEdPriv      []byte // Ed25519 private key (64 bytes)
	OrchXPub        [32]byte
	OrchXPriv       [32]byte
}

// GenerateKeys produces all four keypairs using envelope.GenerateKeyPair (X25519)
// and crypto/ed25519.GenerateKey exclusively.
func GenerateKeys() (*KeyMaterial, error) {
	// Generate operator keypairs
	opXPub, opXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate operator X25519 keypair: %w", err)
	}

	opEdPub, opEdPriv, err := generateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate operator Ed25519 keypair: %w", err)
	}

	// Generate orchestrator keypairs
	orchXPub, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate orchestrator X25519 keypair: %w", err)
	}

	orchEdPub, orchEdPriv, err := generateEd25519KeyPair()
	if err != nil {
		return nil, fmt.Errorf("failed to generate orchestrator Ed25519 keypair: %w", err)
	}

	return &KeyMaterial{
		OperatorEdPub:  opEdPub,
		OperatorEdPriv: opEdPriv,
		OperatorXPub:   opXPub,
		OperatorXPriv:  opXPriv,
		OrchEdPub:      orchEdPub,
		OrchEdPriv:     orchEdPriv,
		OrchXPub:       orchXPub,
		OrchXPriv:      orchXPriv,
	}, nil
}

// RenderEnvBlock creates the paste-ready orchestrator env block with all seven
// AGENT_BUILDER_TELEGRAM_* variables (public keys + orchestrator privates only).
func RenderEnvBlock(keys *KeyMaterial) string {
	var sb strings.Builder

	// Format: hex-encoded all the way; the seven required vars
	fmt.Fprintf(&sb, "AGENT_BUILDER_TELEGRAM_SIGNING_KEY=%s\n", hexEncode(keys.OperatorEdPub))
	fmt.Fprintf(&sb, "AGENT_BUILDER_TELEGRAM_X25519_PUB=%s\n", hexEncode(keys.OperatorXPub[:]))
	fmt.Fprintf(&sb, "AGENT_BUILDER_TELEGRAM_ORCH_PRIV=%s\n", hexEncode(keys.OrchXPriv[:]))
	fmt.Fprintf(&sb, "AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV=%s\n", hexEncode(keys.OrchEdPriv))
	fmt.Fprintf(&sb, "AGENT_BUILDER_TELEGRAM_OP_X25519_PUB=%s\n", hexEncode(keys.OperatorXPub[:]))
	sb.WriteString("AGENT_BUILDER_TELEGRAM_BOT_TOKEN=<fill in>\n")
	sb.WriteString("AGENT_BUILDER_TELEGRAM_BASE_URL=<fill in or omit>\n")
	sb.WriteString("AGENT_BUILDER_TELEGRAM_CHAT_ID=<fill in>\n")

	return sb.String()
}

// WriteKeyfile writes the operator keyfile (operator privates + orchestrator publics)
// with 0600 permissions.
func WriteKeyfile(path string, keys *KeyMaterial) error {
	// Create parent directory if needed (and fail if parent doesn't exist)
	dir := getParentDir(path)
	if dir != "." && dir != "" {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("parent directory does not exist: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("parent is not a directory")
		}
	}

	// Prepare the keyfile structure
	keyfileContent := KeyFile{
		OperatorEdPriv: hexEncode(keys.OperatorEdPriv),
		OperatorXPriv:  hexEncode(keys.OperatorXPriv[:]),
		OrchEdPub:      hexEncode(keys.OrchEdPub),
		OrchXPub:       hexEncode(keys.OrchXPub[:]),
	}

	data, err := marshalJSON(keyfileContent)
	if err != nil {
		return fmt.Errorf("failed to marshal keyfile: %w", err)
	}

	// Write with 0600 permissions
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("failed to write keyfile: %w", err)
	}

	return nil
}

// KeyFile is the structure persisted to the 0600 operator keyfile on disk.
type KeyFile struct {
	OperatorEdPriv string `json:"OperatorEdPriv"`
	OperatorXPriv  string `json:"OperatorXPriv"`
	OrchEdPub      string `json:"OrchEdPub"`
	OrchXPub       string `json:"OrchXPub"`
}

func getParentDir(path string) string {
	idx := strings.LastIndexByte(path, '/')
	if idx == -1 {
		return "."
	}
	if idx == 0 {
		return "/"
	}
	return path[:idx]
}
