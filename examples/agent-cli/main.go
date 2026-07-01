// Package main implements the agent-cli operator tool — a laptop-side CLI that
// generates keys for Telegram operator auth, seals+signs commands for the orchestrator,
// and opens sealed replies.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"strconv"
	"time"

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
		writef(config.Stdout, "  send                seal + sign a command and POST it to Telegram\n")
		writef(config.Stdout, "  reply-open          decrypt + verify a sealed outbound reply envelope\n")
		return ExitOK
	case "keygen":
		return runKeygen(config, config.Args[1:])
	case "send":
		return runSend(config, config.Args[1:])
	case "reply-open":
		return runReplyOpen(config, config.Args[1:])
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

func runSend(config Config, args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // silence flag's default usage

	var keyfilePath string
	var token string
	var baseURL string
	var chatID string
	var replyTo string

	fs.StringVar(&keyfilePath, "keyfile", "", "path to the operator keyfile")
	fs.StringVar(&token, "token", "", "Telegram bot token")
	fs.StringVar(&baseURL, "base-url", "https://api.telegram.org", "Telegram API base URL")
	fs.StringVar(&chatID, "chat-id", "", "Telegram chat ID")
	fs.StringVar(&replyTo, "reply-to", "", "message ID to reply to (positive integer)")

	if err := fs.Parse(args); err != nil {
		writef(config.Stderr, "usage error: %v\n", err)
		return ExitUsage
	}

	// Validate required flags
	if keyfilePath == "" {
		writef(config.Stderr, "usage error: --keyfile is required\n")
		return ExitUsage
	}
	if token == "" {
		writef(config.Stderr, "usage error: --token is required\n")
		return ExitUsage
	}
	if chatID == "" {
		writef(config.Stderr, "usage error: --chat-id is required\n")
		return ExitUsage
	}

	// Get positional argument (command text)
	cmdArgs := fs.Args()
	if len(cmdArgs) == 0 {
		writef(config.Stderr, "usage error: command text is required\n")
		return ExitUsage
	}

	cmdText := strings.TrimSpace(strings.Join(cmdArgs, " "))
	if cmdText == "" {
		writef(config.Stderr, "usage error: command text cannot be empty or whitespace-only\n")
		return ExitUsage
	}

	// Validate reply-to if provided
	var replyToID int64
	if replyTo != "" {
		id, err := strconv.ParseInt(replyTo, 10, 64)
		if err != nil || id <= 0 {
			writef(config.Stderr, "usage error: --reply-to must be a positive integer\n")
			return ExitUsage
		}
		replyToID = id
	}

	// Read and parse keyfile
	keyfileData, err := os.ReadFile(keyfilePath)
	if err != nil {
		writef(config.Stderr, "error: failed to read keyfile: %v\n", err)
		return 1
	}

	var kf KeyFile
	if err := unmarshalJSON(keyfileData, &kf); err != nil {
		writef(config.Stderr, "error: failed to parse keyfile JSON: %v\n", err)
		return 1
	}

	// Decode hex fields
	opEdPriv, err := hexDecode(kf.OperatorEdPriv)
	if err != nil {
		writef(config.Stderr, "error: failed to decode OperatorEdPriv hex: %v\n", err)
		return 1
	}
	opXPriv, err := hexDecode(kf.OperatorXPriv)
	if err != nil {
		writef(config.Stderr, "error: failed to decode OperatorXPriv hex: %v\n", err)
		return 1
	}
	if len(opXPriv) != 32 {
		writef(config.Stderr, "error: OperatorXPriv must be 32 bytes, got %d\n", len(opXPriv))
		return 1
	}
	orchXPub, err := hexDecode(kf.OrchXPub)
	if err != nil {
		writef(config.Stderr, "error: failed to decode OrchXPub hex: %v\n", err)
		return 1
	}
	if len(orchXPub) != 32 {
		writef(config.Stderr, "error: OrchXPub must be 32 bytes, got %d\n", len(orchXPub))
		return 1
	}

	// Build the envelope
	var opXPrivArray [32]byte
	copy(opXPrivArray[:], opXPriv)
	var orchXPubArray [32]byte
	copy(orchXPubArray[:], orchXPub)

	env, err := BuildEnvelope(opEdPriv, opXPrivArray, orchXPubArray, []byte(cmdText))
	if err != nil {
		writef(config.Stderr, "error: failed to build envelope: %v\n", err)
		return 1
	}

	// Marshal envelope to JSON
	envJSON, err := marshalJSON(env)
	if err != nil {
		writef(config.Stderr, "error: failed to marshal envelope: %v\n", err)
		return 1
	}

	// Build request body
	requestBody := map[string]interface{}{
		"chat_id": chatID,
		"text":    string(envJSON),
	}
	if replyToID > 0 {
		requestBody["reply_to_message_id"] = replyToID
	}

	bodyJSON, err := marshalJSON(requestBody)
	if err != nil {
		writef(config.Stderr, "error: failed to marshal request body: %v\n", err)
		return 1
	}

	// POST to Telegram
	url := fmt.Sprintf("%s/bot%s/sendMessage", baseURL, token)
	resp, err := http.Post(url, "application/json", bytes.NewReader(bodyJSON))
	if err != nil {
		writef(config.Stderr, "error: failed to POST to Telegram: %v\n", err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		writef(config.Stderr, "error: Telegram API returned %d: %s\n", resp.StatusCode, string(body))
		return 1
	}

	// Parse response
	var apiResp struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		writef(config.Stderr, "error: failed to parse Telegram response: %v\n", err)
		return 1
	}

	if !apiResp.OK {
		writef(config.Stderr, "error: Telegram API returned ok=false\n")
		return 1
	}

	return ExitOK
}

func runReplyOpen(config Config, args []string) int {
	fs := flag.NewFlagSet("reply-open", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // silence flag's default usage

	var keyfilePath string
	var envelopeFlag string

	fs.StringVar(&keyfilePath, "keyfile", "", "path to the operator keyfile")
	fs.StringVar(&envelopeFlag, "envelope", "", "envelope JSON (inline)")

	if err := fs.Parse(args); err != nil {
		writef(config.Stderr, "usage error: %v\n", err)
		return ExitUsage
	}

	// Validate required keyfile
	if keyfilePath == "" {
		writef(config.Stderr, "usage error: --keyfile is required\n")
		return ExitUsage
	}

	// Determine envelope source (file, stdin, or flag) — exactly one required
	positionalArgs := fs.Args()
	var envelopeJSON []byte
	var err error

	if len(positionalArgs) > 0 && envelopeFlag != "" {
		// Both file and --envelope given
		writef(config.Stderr, "usage error: cannot specify both positional file and --envelope flag\n")
		return ExitUsage
	}

	if len(positionalArgs) > 0 {
		// Read from file
		envelopeJSON, err = os.ReadFile(positionalArgs[0])
		if err != nil {
			writef(config.Stderr, "error: failed to read envelope file: %v\n", err)
			return 1
		}
	} else if envelopeFlag != "" {
		// Use inline envelope
		envelopeJSON = []byte(envelopeFlag)
	} else {
		// Read from stdin
		envelopeJSON, err = io.ReadAll(config.Stdin)
		if err != nil {
			writef(config.Stderr, "error: failed to read from stdin: %v\n", err)
			return 1
		}
	}

	// Read and parse keyfile
	keyfileData, err := os.ReadFile(keyfilePath)
	if err != nil {
		writef(config.Stderr, "error: failed to read keyfile: %v\n", err)
		return 1
	}

	var kf KeyFile
	if err := unmarshalJSON(keyfileData, &kf); err != nil {
		writef(config.Stderr, "error: failed to parse keyfile JSON: %v\n", err)
		return 1
	}

	// Decode hex fields from keyfile
	opXPriv, err := hexDecode(kf.OperatorXPriv)
	if err != nil {
		writef(config.Stderr, "error: failed to decode OperatorXPriv hex: %v\n", err)
		return 1
	}
	if len(opXPriv) != 32 {
		writef(config.Stderr, "error: OperatorXPriv must be 32 bytes, got %d\n", len(opXPriv))
		return 1
	}

	orchEdPub, err := hexDecode(kf.OrchEdPub)
	if err != nil {
		writef(config.Stderr, "error: failed to decode OrchEdPub hex: %v\n", err)
		return 1
	}

	orchXPub, err := hexDecode(kf.OrchXPub)
	if err != nil {
		writef(config.Stderr, "error: failed to decode OrchXPub hex: %v\n", err)
		return 1
	}
	if len(orchXPub) != 32 {
		writef(config.Stderr, "error: OrchXPub must be 32 bytes, got %d\n", len(orchXPub))
		return 1
	}

	// Parse envelope JSON
	var env envelope.Envelope
	if err := unmarshalJSON(envelopeJSON, &env); err != nil {
		writef(config.Stderr, "error: malformed envelope JSON: %v\n", err)
		return 1
	}

	// Prepare key arrays for VerifyAndOpen
	var opXPrivArray [32]byte
	copy(opXPrivArray[:], opXPriv)
	var orchXPubArray [32]byte
	copy(orchXPubArray[:], orchXPub)

	// VerifyAndOpen: orchestrator signed, so verify with orchestrator's Ed25519 pub
	// and open with operator X25519 priv + orchestrator X25519 pub
	plaintext, err := envelope.VerifyAndOpen(
		env,
		orchEdPub,
		envelope.NewReplayCache(60*time.Second),
		opXPrivArray,
		orchXPubArray,
	)
	if err != nil {
		// Classify the error
		if errors.Is(err, envelope.ErrBadSignature) {
			writef(config.Stderr, "error: signature verification failed\n")
		} else if errors.Is(err, envelope.ErrUnknownKey) {
			writef(config.Stderr, "error: decryption failed\n")
		} else {
			writef(config.Stderr, "error: %v\n", err)
		}
		return 1
	}

	// Print recovered plaintext to stdout
	_, _ = config.Stdout.Write(plaintext)
	// Add optional trailing newline for terminal display
	_, _ = fmt.Fprint(config.Stdout, "\n")

	return ExitOK
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
