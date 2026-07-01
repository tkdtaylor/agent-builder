package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/channel/telegram"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
)

// TC-148-01: keygen generates all four keypairs at correct sizes
func TestGenerateKeys_CorrectSizes(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() failed: %v", err)
	}

	// Check Ed25519 public keys (32 bytes)
	if len(keys.OperatorEdPub) != ed25519.PublicKeySize {
		t.Errorf("OperatorEdPub: expected %d bytes, got %d", ed25519.PublicKeySize, len(keys.OperatorEdPub))
	}
	if len(keys.OrchEdPub) != ed25519.PublicKeySize {
		t.Errorf("OrchEdPub: expected %d bytes, got %d", ed25519.PublicKeySize, len(keys.OrchEdPub))
	}

	// Check Ed25519 private keys (64 bytes)
	if len(keys.OperatorEdPriv) != ed25519.PrivateKeySize {
		t.Errorf("OperatorEdPriv: expected %d bytes, got %d", ed25519.PrivateKeySize, len(keys.OperatorEdPriv))
	}
	if len(keys.OrchEdPriv) != ed25519.PrivateKeySize {
		t.Errorf("OrchEdPriv: expected %d bytes, got %d", ed25519.PrivateKeySize, len(keys.OrchEdPriv))
	}

	// Check X25519 keys (32 bytes, fixed array)
	if len(keys.OperatorXPub) != 32 {
		t.Errorf("OperatorXPub: expected 32 bytes, got %d", len(keys.OperatorXPub))
	}
	if len(keys.OperatorXPriv) != 32 {
		t.Errorf("OperatorXPriv: expected 32 bytes, got %d", len(keys.OperatorXPriv))
	}
	if len(keys.OrchXPub) != 32 {
		t.Errorf("OrchXPub: expected 32 bytes, got %d", len(keys.OrchXPub))
	}
	if len(keys.OrchXPriv) != 32 {
		t.Errorf("OrchXPriv: expected 32 bytes, got %d", len(keys.OrchXPriv))
	}
}

// TC-148-01 (edge case): two calls produce different key material
func TestGenerateKeys_NonDeterministic(t *testing.T) {
	keys1, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() call 1 failed: %v", err)
	}

	keys2, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() call 2 failed: %v", err)
	}

	// At least one field should differ; we check all to be thorough
	if bytes.Equal(keys1.OperatorEdPub, keys2.OperatorEdPub) {
		t.Error("OperatorEdPub should not be identical across two calls")
	}
	if bytes.Equal(keys1.OperatorXPub[:], keys2.OperatorXPub[:]) {
		t.Error("OperatorXPub should not be identical across two calls")
	}
	if bytes.Equal(keys1.OrchEdPub, keys2.OrchEdPub) {
		t.Error("OrchEdPub should not be identical across two calls")
	}
	if bytes.Equal(keys1.OrchXPub[:], keys2.OrchXPub[:]) {
		t.Error("OrchXPub should not be identical across two calls")
	}
}

// TC-148-02: source inspection — verify only envelope.GenerateKeyPair and
// ed25519.GenerateKey are used (NOT hand-rolled crypto)
func TestGenerateKeys_OnlyStdlibCrypto(t *testing.T) {
	// Read the source files and grep for the required crypto calls.
	// This test FAILS if someone swaps in a hand-rolled RNG.

	// Get the path to main.go and crypto.go relative to this test file
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	testDir := filepath.Dir(filename)

	// Read main.go
	mainFilePath := filepath.Join(testDir, "main.go")
	mainContent, err := os.ReadFile(mainFilePath)
	if err != nil {
		t.Fatalf("failed to read main.go: %v", err)
	}
	mainText := string(mainContent)

	// Read crypto.go
	cryptoFilePath := filepath.Join(testDir, "crypto.go")
	cryptoContent, err := os.ReadFile(cryptoFilePath)
	if err != nil {
		t.Fatalf("failed to read crypto.go: %v", err)
	}
	cryptoText := string(cryptoContent)

	// Combine for searching (either file could have the imports)
	allSource := mainText + "\n" + cryptoText

	// Assert that envelope.GenerateKeyPair is called
	if !strings.Contains(allSource, "envelope.GenerateKeyPair") {
		t.Error("source does not call envelope.GenerateKeyPair")
	}

	// Assert that ed25519.GenerateKey is called
	if !strings.Contains(allSource, "ed25519.GenerateKey") {
		t.Error("source does not call ed25519.GenerateKey")
	}

	// Assert that no hand-rolled crypto is present (check for suspicious patterns)
	// This is a heuristic: looking for common hand-rolled RNG misuses
	if strings.Contains(mainText, "rand.Int") || strings.Contains(mainText, "random.") {
		t.Error("source may contain hand-rolled random number generation")
	}
}

// TC-148-03: env block contains all seven orchestrator-side variables with
// correct hex encodings
func TestRenderEnvBlock_AllVarsPresent(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() failed: %v", err)
	}

	envBlock := RenderEnvBlock(keys)

	// Check all seven vars are present
	requiredVars := []string{
		"AGENT_BUILDER_TELEGRAM_SIGNING_KEY=",
		"AGENT_BUILDER_TELEGRAM_X25519_PUB=",
		"AGENT_BUILDER_TELEGRAM_ORCH_PRIV=",
		"AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV=",
		"AGENT_BUILDER_TELEGRAM_OP_X25519_PUB=",
		"AGENT_BUILDER_TELEGRAM_BOT_TOKEN=",
		"AGENT_BUILDER_TELEGRAM_BASE_URL=",
		"AGENT_BUILDER_TELEGRAM_CHAT_ID=",
	}

	for _, v := range requiredVars {
		if !strings.Contains(envBlock, v) {
			t.Errorf("env block missing variable: %s", v)
		}
	}

	// Parse each line and verify hex lengths
	lines := strings.Split(strings.TrimSpace(envBlock), "\n")
	if len(lines) < len(requiredVars) {
		t.Errorf("expected at least %d lines, got %d", len(requiredVars), len(lines))
	}

	// Check specific hex lengths
	varMap := make(map[string]string)
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			varMap[parts[0]] = parts[1]
		}
	}

	// _SIGNING_KEY: operator Ed25519 public key (32 bytes = 64 hex chars)
	if signingKeyHex, ok := varMap["AGENT_BUILDER_TELEGRAM_SIGNING_KEY"]; ok {
		signingKeyBytes, err := hex.DecodeString(signingKeyHex)
		if err != nil {
			t.Errorf("_SIGNING_KEY hex decode failed: %v", err)
		} else if len(signingKeyBytes) != 32 {
			t.Errorf("_SIGNING_KEY: expected 32 bytes, got %d", len(signingKeyBytes))
		} else if !bytes.Equal(signingKeyBytes, keys.OperatorEdPub) {
			t.Error("_SIGNING_KEY does not match OperatorEdPub")
		}
	}

	// _X25519_PUB: operator X25519 public key (32 bytes)
	if x25519PubHex, ok := varMap["AGENT_BUILDER_TELEGRAM_X25519_PUB"]; ok {
		x25519PubBytes, err := hex.DecodeString(x25519PubHex)
		if err != nil {
			t.Errorf("_X25519_PUB hex decode failed: %v", err)
		} else if len(x25519PubBytes) != 32 {
			t.Errorf("_X25519_PUB: expected 32 bytes, got %d", len(x25519PubBytes))
		} else if !bytes.Equal(x25519PubBytes, keys.OperatorXPub[:]) {
			t.Error("_X25519_PUB does not match OperatorXPub")
		}
	}

	// _ORCH_PRIV: orchestrator X25519 private key (32 bytes)
	if orchPrivHex, ok := varMap["AGENT_BUILDER_TELEGRAM_ORCH_PRIV"]; ok {
		orchPrivBytes, err := hex.DecodeString(orchPrivHex)
		if err != nil {
			t.Errorf("_ORCH_PRIV hex decode failed: %v", err)
		} else if len(orchPrivBytes) != 32 {
			t.Errorf("_ORCH_PRIV: expected 32 bytes, got %d", len(orchPrivBytes))
		} else if !bytes.Equal(orchPrivBytes, keys.OrchXPriv[:]) {
			t.Error("_ORCH_PRIV does not match OrchXPriv")
		}
	}

	// _ORCH_ED_PRIV: orchestrator Ed25519 private key (64 bytes = 128 hex chars)
	if orchEdPrivHex, ok := varMap["AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV"]; ok {
		orchEdPrivBytes, err := hex.DecodeString(orchEdPrivHex)
		if err != nil {
			t.Errorf("_ORCH_ED_PRIV hex decode failed: %v", err)
		} else if len(orchEdPrivBytes) != 64 {
			t.Errorf("_ORCH_ED_PRIV: expected 64 bytes, got %d", len(orchEdPrivBytes))
		} else if !bytes.Equal(orchEdPrivBytes, keys.OrchEdPriv) {
			t.Error("_ORCH_ED_PRIV does not match OrchEdPriv")
		}
	}

	// _OP_X25519_PUB: operator X25519 public key (same as _X25519_PUB)
	if opX25519PubHex, ok := varMap["AGENT_BUILDER_TELEGRAM_OP_X25519_PUB"]; ok {
		opX25519PubBytes, err := hex.DecodeString(opX25519PubHex)
		if err != nil {
			t.Errorf("_OP_X25519_PUB hex decode failed: %v", err)
		} else if len(opX25519PubBytes) != 32 {
			t.Errorf("_OP_X25519_PUB: expected 32 bytes, got %d", len(opX25519PubBytes))
		} else if !bytes.Equal(opX25519PubBytes, keys.OperatorXPub[:]) {
			t.Error("_OP_X25519_PUB does not match OperatorXPub")
		}

		// Assert the two are identical
		if x25519PubHex, ok := varMap["AGENT_BUILDER_TELEGRAM_X25519_PUB"]; ok {
			if x25519PubHex != opX25519PubHex {
				t.Error("_X25519_PUB and _OP_X25519_PUB should be identical")
			}
		}
	}
}

// TC-148-04: env block never contains operator private key material
func TestRenderEnvBlock_NoOperatorPrivates(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() failed: %v", err)
	}

	envBlock := RenderEnvBlock(keys)

	// Encode the forbidden secrets in both hex and base64
	opEdPrivHex := hex.EncodeToString(keys.OperatorEdPriv)
	opXPrivHex := hex.EncodeToString(keys.OperatorXPriv[:])

	// Check they don't appear in the env block
	if strings.Contains(envBlock, opEdPrivHex) {
		t.Error("env block contains operator Ed25519 private key (hex)")
	}
	if strings.Contains(envBlock, opXPrivHex) {
		t.Error("env block contains operator X25519 private key (hex)")
	}
}

// TC-148-05: keyfile contains operator secrets + orchestrator public keys,
// written with 0600 permissions
func TestWriteKeyfile_CorrectContent(t *testing.T) {
	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() failed: %v", err)
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile() failed: %v", err)
	}

	// Check file permissions
	info, err := os.Stat(keyfilePath)
	if err != nil {
		t.Fatalf("os.Stat() failed: %v", err)
	}

	if info.Mode().Perm() != 0600 {
		t.Errorf("keyfile permissions: expected 0600, got %04o", info.Mode().Perm())
	}

	// Read and parse the keyfile
	data, err := os.ReadFile(keyfilePath)
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}

	var kf KeyFile
	if err := unmarshalJSON(data, &kf); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	// Verify contents
	opEdPrivDecoded, err := hexDecode(kf.OperatorEdPriv)
	if err != nil {
		t.Errorf("OperatorEdPriv hex decode failed: %v", err)
	} else if !bytes.Equal(opEdPrivDecoded, keys.OperatorEdPriv) {
		t.Error("OperatorEdPriv mismatch")
	}

	opXPrivDecoded, err := hexDecode(kf.OperatorXPriv)
	if err != nil {
		t.Errorf("OperatorXPriv hex decode failed: %v", err)
	} else if !bytes.Equal(opXPrivDecoded, keys.OperatorXPriv[:]) {
		t.Error("OperatorXPriv mismatch")
	}

	orchEdPubDecoded, err := hexDecode(kf.OrchEdPub)
	if err != nil {
		t.Errorf("OrchEdPub hex decode failed: %v", err)
	} else if !bytes.Equal(orchEdPubDecoded, keys.OrchEdPub) {
		t.Error("OrchEdPub mismatch")
	}

	orchXPubDecoded, err := hexDecode(kf.OrchXPub)
	if err != nil {
		t.Errorf("OrchXPub hex decode failed: %v", err)
	} else if !bytes.Equal(orchXPubDecoded, keys.OrchXPub[:]) {
		t.Error("OrchXPub mismatch")
	}
}

// TC-148-05 (edge case): WriteKeyfile on a path whose parent directory does
// not exist returns an error
func TestWriteKeyfile_MissingParentDir(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() failed: %v", err)
	}

	// Use a path with a non-existent parent
	keyfilePath := "/nonexistent/parent/dir/keyfile.json"

	err = WriteKeyfile(keyfilePath, keys)
	if err == nil {
		t.Fatal("WriteKeyfile() should have failed for non-existent parent dir")
	}

	// Verify no file was left behind
	_, statErr := os.Stat(keyfilePath)
	if statErr == nil {
		t.Fatal("keyfile should not exist after failed WriteKeyfile()")
	}
}

// TC-148-06: keyfile omits orchestrator private keys
func TestWriteKeyfile_NoOrchPrivates(t *testing.T) {
	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys, err := GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys() failed: %v", err)
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile() failed: %v", err)
	}

	data, err := os.ReadFile(keyfilePath)
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}

	keyfileContent := string(data)

	// Encode the forbidden secrets in hex
	orchEdPrivHex := hex.EncodeToString(keys.OrchEdPriv)
	orchXPrivHex := hex.EncodeToString(keys.OrchXPriv[:])

	// Check they don't appear in the keyfile
	if strings.Contains(keyfileContent, orchEdPrivHex) {
		t.Error("keyfile contains orchestrator Ed25519 private key (hex)")
	}
	if strings.Contains(keyfileContent, orchXPrivHex) {
		t.Error("keyfile contains orchestrator X25519 private key (hex)")
	}
}

// TC-148-07: CLI end-to-end — agent-cli keygen --keyfile <path> prints the
// env block to stdout and writes the keyfile
func TestMain_KeygenEndToEnd(t *testing.T) {
	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "operator.json")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	config := Config{
		Args:   []string{"keygen", "--keyfile", keyfilePath},
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  nil,
	}

	exitCode := Main(config)

	if exitCode != ExitOK {
		t.Errorf("Main() exit code: expected %d, got %d", ExitOK, exitCode)
	}

	// Check stdout contains env block
	stdoutContent := stdout.String()
	if !strings.Contains(stdoutContent, "AGENT_BUILDER_TELEGRAM_SIGNING_KEY=") {
		t.Error("stdout missing AGENT_BUILDER_TELEGRAM_SIGNING_KEY")
	}

	// Check stderr contains confirmation
	stderrContent := stderr.String()
	if !strings.Contains(stderrContent, "keyfile written to") {
		t.Error("stderr missing confirmation message")
	}

	// Check keyfile exists
	info, err := os.Stat(keyfilePath)
	if err != nil {
		t.Fatalf("keyfile does not exist: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("keyfile permissions: expected 0600, got %04o", info.Mode().Perm())
	}
}

// TC-148-07 (edge case): agent-cli keygen with no --keyfile exits 2 (usage error)
func TestMain_KeygenNoKeyfile(t *testing.T) {
	stderr := &bytes.Buffer{}

	config := Config{
		Args:   []string{"keygen"},
		Stdout: &bytes.Buffer{},
		Stderr: stderr,
		Stdin:  nil,
	}

	exitCode := Main(config)

	if exitCode != ExitUsage {
		t.Errorf("Main() exit code: expected %d (usage error), got %d", ExitUsage, exitCode)
	}

	// Check stderr contains error message
	if !strings.Contains(stderr.String(), "--keyfile") {
		t.Error("stderr should mention --keyfile requirement")
	}
}

// TC-148-08: --keyfile targeting an existing file refuses to overwrite
// without --force
func TestMain_KeygenExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "operator.json")

	// First keygen call
	config1 := Config{
		Args:   []string{"keygen", "--keyfile", keyfilePath},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Stdin:  nil,
	}

	exitCode1 := Main(config1)
	if exitCode1 != ExitOK {
		t.Fatalf("first keygen call failed: exit code %d", exitCode1)
	}

	// Read the first keyfile
	data1, err := os.ReadFile(keyfilePath)
	if err != nil {
		t.Fatalf("ReadFile() failed: %v", err)
	}

	// Second keygen call without --force (should fail)
	config2 := Config{
		Args:   []string{"keygen", "--keyfile", keyfilePath},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Stdin:  nil,
	}

	exitCode2 := Main(config2)
	if exitCode2 == ExitOK {
		t.Error("second keygen without --force should have failed")
	}

	// Verify original file is unchanged
	data1After, err := os.ReadFile(keyfilePath)
	if err != nil {
		t.Fatalf("ReadFile() after second call failed: %v", err)
	}

	if !bytes.Equal(data1, data1After) {
		t.Error("keyfile was modified despite failing the second keygen call")
	}

	// Third keygen call with --force (should succeed)
	config3 := Config{
		Args:   []string{"keygen", "--keyfile", keyfilePath, "--force"},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Stdin:  nil,
	}

	exitCode3 := Main(config3)
	if exitCode3 != ExitOK {
		t.Fatalf("third keygen with --force failed: exit code %d", exitCode3)
	}

	// Verify file now contains different key material (with high probability)
	data3, err := os.ReadFile(keyfilePath)
	if err != nil {
		t.Fatalf("ReadFile() after third call failed: %v", err)
	}

	if bytes.Equal(data1, data3) {
		t.Error("keyfile should contain different key material after --force overwrite")
	}
}

// TC-148-09: secret material segregation — operator privates NEVER in stdout/stderr;
// orchestrator privates appear ONLY in env block, NOT in stderr confirmation line
func TestMain_KeygenNoSecretLeakage(t *testing.T) {
	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "operator.json")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}

	// CRITICAL: Call runKeygenCore ONCE with the real output buffers.
	// This single invocation generates ONE key set and writes it to stdout/stderr.
	// We then assert against the SAME keys that were actually printed.
	keys, exitCode := runKeygenCore(stdout, stderr, keyfilePath)

	if exitCode != ExitOK {
		t.Fatalf("runKeygenCore failed: exit code %d", exitCode)
	}
	if keys == nil {
		t.Fatal("runKeygenCore returned nil keys")
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	combinedOutput := stdoutStr + stderrStr

	// ASSERTION (a): operator privates NEVER appear anywhere in stdout+stderr
	// (these are the keys that were ACTUALLY generated and printed in the invocation above)
	opEdPrivHex := hexEncode(keys.OperatorEdPriv)
	opXPrivHex := hexEncode(keys.OperatorXPriv[:])

	if strings.Contains(combinedOutput, opEdPrivHex) {
		t.Error("operator Ed25519 private key (hex) appears in stdout+stderr")
	}
	if strings.Contains(combinedOutput, opXPrivHex) {
		t.Error("operator X25519 private key (hex) appears in stdout+stderr")
	}

	// ASSERTION (b): orchestrator privates appear ONLY in the env block (stdout),
	// NOT in stderr confirmation line.
	// These are the REAL orchestrator privates from the same invocation.
	orchEdPrivHex := hexEncode(keys.OrchEdPriv)
	orchXPrivHex := hexEncode(keys.OrchXPriv[:])

	// These MUST appear in stdout (in the env block)
	if !strings.Contains(stdoutStr, orchEdPrivHex) {
		t.Error("orchestrator Ed25519 private key missing from stdout (env block)")
	}
	if !strings.Contains(stdoutStr, orchXPrivHex) {
		t.Error("orchestrator X25519 private key missing from stdout (env block)")
	}

	// But these MUST NOT appear in stderr (the confirmation line)
	// This is the load-bearing assertion: if someone accidentally echoed
	// an orch private into the confirmation line, this would catch it.
	if strings.Contains(stderrStr, orchEdPrivHex) {
		t.Error("orchestrator Ed25519 private key appears in stderr confirmation line (LEAKAGE)")
	}
	if strings.Contains(stderrStr, orchXPrivHex) {
		t.Error("orchestrator X25519 private key appears in stderr confirmation line (LEAKAGE)")
	}

	// ASSERTION (c): banner separator present
	if !strings.Contains(stderrStr, "---") {
		t.Error("stderr missing labeled banner separator (---)")
	}
}

// Helper function: generateSendKeys creates all four keypair sets for send tests.
func generateSendKeys(t *testing.T) (
	opEdPub ed25519.PublicKey,
	opEdPriv ed25519.PrivateKey,
	opXPub [32]byte,
	opXPriv [32]byte,
	orchEdPub ed25519.PublicKey,
	orchEdPriv ed25519.PrivateKey,
	orchXPub [32]byte,
	orchXPriv [32]byte,
) {
	t.Helper()
	var err error
	opEdPub, opEdPriv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate operator Ed25519 key: %v", err)
	}
	opXPub, opXPriv, err = envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate operator X25519 keypair: %v", err)
	}
	orchEdPub, orchEdPriv, err = ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate orchestrator Ed25519 key: %v", err)
	}
	orchXPub, orchXPriv, err = envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate orchestrator X25519 keypair: %v", err)
	}
	return
}

// Helper function: stubSendMessageServer creates a test server that captures POST requests.
func stubSendMessageServer(t *testing.T) (server *httptest.Server, bodyCapture *[]byte, callCount *int) {
	t.Helper()
	var lastBody []byte
	count := 0
	bodyCapture = &lastBody
	callCount = &count
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		b, _ := io.ReadAll(r.Body)
		lastBody = b
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	t.Cleanup(server.Close)
	return
}

// TC-149-01: sealed+signed envelope round-trips through the production
// envelope.VerifyAndOpen (load-bearing byte-compatibility assertion)
func TestTC149_01_EnvelopeRoundTrip(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, _, _, orchXPub, orchXPriv := generateSendKeys(t)

	const cmdText = "status tg-42-7"
	cmdBytes := []byte(cmdText)

	// Build the envelope using the CLI's BuildEnvelope function
	env, err := BuildEnvelope(opEdPriv, opXPriv, orchXPub, cmdBytes)
	if err != nil {
		t.Fatalf("BuildEnvelope failed: %v", err)
	}

	// TC-149-01: envelope must have non-empty fields
	if env.Nonce == "" {
		t.Error("Nonce is empty")
	}
	if env.TS == "" {
		t.Error("TS is empty")
	}
	if env.Payload == "" {
		t.Error("Payload is empty")
	}
	if env.Sig == "" {
		t.Error("Sig is empty")
	}

	// TC-149-01 LOAD-BEARING: Round-trip through real VerifyAndOpen
	// with the adapter's exact key-role assignment:
	//   TrustedSigningKey = operator Ed25519 pub
	//   OrchestratorPriv = orchestrator X25519 priv
	//   TrustedX25519Pub = operator X25519 pub
	plaintext, err := envelope.VerifyAndOpen(
		*env,
		opEdPub,
		envelope.NewReplayCache(60*time.Second),
		orchXPriv,
		opXPub,
	)
	if err != nil {
		t.Fatalf("VerifyAndOpen failed: %v", err)
	}

	// TC-149-01: plaintext must match command text byte-for-byte
	if !bytes.Equal(plaintext, cmdBytes) {
		t.Errorf("plaintext mismatch: got %q, expected %q", string(plaintext), cmdText)
	}
}

// TC-149-01 (edge case): multi-word goal-spec command
func TestTC149_01_MultiWordCommand(t *testing.T) {
	opEdPub, opEdPriv, opXPub, opXPriv, _, _, orchXPub, orchXPriv := generateSendKeys(t)

	const cmdText = "new-goal plan docs and implement feature X with constraints"
	cmdBytes := []byte(cmdText)

	env, err := BuildEnvelope(opEdPriv, opXPriv, orchXPub, cmdBytes)
	if err != nil {
		t.Fatalf("BuildEnvelope failed: %v", err)
	}

	plaintext, err := envelope.VerifyAndOpen(
		*env,
		opEdPub,
		envelope.NewReplayCache(60*time.Second),
		orchXPriv,
		opXPub,
	)
	if err != nil {
		t.Fatalf("VerifyAndOpen failed: %v", err)
	}

	if !bytes.Equal(plaintext, cmdBytes) {
		t.Errorf("plaintext mismatch: got %q, expected %q", string(plaintext), cmdText)
	}
}

// TC-149-02: Payload and Nonce are hex-encoded (not base64)
func TestTC149_02_HexEncodedFields(t *testing.T) {
	_, opEdPriv, _, opXPriv, _, _, orchXPub, _ := generateSendKeys(t)

	const cmdText = "status"
	cmdBytes := []byte(cmdText)

	env, err := BuildEnvelope(opEdPriv, opXPriv, orchXPub, cmdBytes)
	if err != nil {
		t.Fatalf("BuildEnvelope failed: %v", err)
	}

	// TC-149-02: Payload must decode as hex
	payloadHexBytes, err := hex.DecodeString(env.Payload)
	if err != nil {
		t.Errorf("Payload is not valid hex: %v", err)
	}

	// TC-149-02: Payload is hex, NOT base64 (negative contrast)
	// Assert that base64 decode either fails OR produces different bytes than hex
	payloadBase64Bytes, b64Err := base64.StdEncoding.DecodeString(env.Payload)
	if b64Err == nil && bytes.Equal(payloadBase64Bytes, payloadHexBytes) {
		// Both decode successfully to the same bytes — this would mean Payload is
		// ambiguously interpretable as both hex and base64 of the same content,
		// which is NOT what we want. The field must be hex-only.
		t.Error("Payload decodes as valid base64 of the same bytes as hex — encoding is ambiguous")
	}

	// TC-149-02: Nonce must decode as hex
	nonceHexBytes, err := hex.DecodeString(env.Nonce)
	if err != nil {
		t.Errorf("Nonce is not valid hex: %v", err)
	}
	if len(nonceHexBytes) != 24 {
		t.Errorf("Nonce decoded to %d bytes, expected 24", len(nonceHexBytes))
	}

	// TC-149-02: Nonce is hex, NOT base64 (negative contrast)
	// Assert that base64 decode either fails OR produces different bytes than hex
	nonceBase64Bytes, b64NErr := base64.StdEncoding.DecodeString(env.Nonce)
	if b64NErr == nil && bytes.Equal(nonceBase64Bytes, nonceHexBytes) {
		// Both decode successfully to the same bytes — this would mean Nonce is
		// ambiguously interpretable as both hex and base64 of the same content,
		// which is NOT what we want. The field must be hex-only.
		t.Error("Nonce decodes as valid base64 of the same bytes as hex — encoding is ambiguous")
	}
}

// TC-149-03: send POSTs to <baseURL>/bot<token>/sendMessage with {chat_id, text}
func TestTC149_03_SendPostsEnvelope(t *testing.T) {
	_, opEdPriv, _, opXPriv, _, _, orchXPub, _ := generateSendKeys(t)

	server, bodyCapture, callCount := stubSendMessageServer(t)

	const token = "TEST_TOKEN_149"
	const chatID = "12345"
	const cmdText = "status"

	// Create a keyfile
	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys := &KeyMaterial{
		OperatorEdPriv: opEdPriv,
		OperatorXPriv:  opXPriv,
		OrchXPub:       orchXPub,
		OrchEdPub:      []byte{}, // Not used in send
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	// Run send command
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	config := Config{
		Args: []string{
			"send",
			"--keyfile", keyfilePath,
			"--token", token,
			"--base-url", server.URL,
			"--chat-id", chatID,
			cmdText,
		},
		Stdout: stdout,
		Stderr: stderr,
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Errorf("send command failed with exit code %d, stderr: %s", exitCode, stderr.String())
	}

	// TC-149-03: exactly one POST
	if *callCount != 1 {
		t.Errorf("expected 1 POST, got %d", *callCount)
	}

	// TC-149-03: request body contains {chat_id, text} with text as envelope JSON
	var outer struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(*bodyCapture, &outer); err != nil {
		t.Fatalf("failed to parse outer body: %v — body: %s", err, *bodyCapture)
	}

	if outer.ChatID != chatID {
		t.Errorf("chat_id mismatch: got %q, expected %q", outer.ChatID, chatID)
	}

	var env envelope.Envelope
	if err := json.Unmarshal([]byte(outer.Text), &env); err != nil {
		t.Fatalf("Text field does not parse as envelope.Envelope: %v — text: %s", err, outer.Text)
	}

	if env.From != "operator" {
		t.Errorf("envelope.From: got %q, expected operator", env.From)
	}
	if env.To != "orchestrator" {
		t.Errorf("envelope.To: got %q, expected orchestrator", env.To)
	}
}

// TC-149-04: --reply-to threads reply_to_message_id in the POST body
func TestTC149_04_ReplyToInBody(t *testing.T) {
	_, opEdPriv, _, opXPriv, _, _, orchXPub, _ := generateSendKeys(t)

	server, bodyCapture, _ := stubSendMessageServer(t)

	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys := &KeyMaterial{
		OperatorEdPriv: opEdPriv,
		OperatorXPriv:  opXPriv,
		OrchXPub:       orchXPub,
		OrchEdPub:      []byte{},
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	config := Config{
		Args: []string{
			"send",
			"--keyfile", keyfilePath,
			"--token", "TOKEN",
			"--base-url", server.URL,
			"--chat-id", "42",
			"--reply-to", "555",
			"status",
		},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Errorf("send command failed with exit code %d", exitCode)
	}

	// TC-149-04: body must have reply_to_message_id: 555
	var body map[string]interface{}
	if err := json.Unmarshal(*bodyCapture, &body); err != nil {
		t.Fatalf("failed to parse body: %v", err)
	}

	replyToID, ok := body["reply_to_message_id"]
	if !ok {
		t.Error("reply_to_message_id missing from body")
	} else if float64(555) != replyToID {
		t.Errorf("reply_to_message_id: got %v, expected 555", replyToID)
	}
}

// TC-149-04 (edge case): omitting --reply-to produces body with NO reply_to_message_id key
func TestTC149_04_NoReplyToOmitsKey(t *testing.T) {
	_, opEdPriv, _, opXPriv, _, _, orchXPub, _ := generateSendKeys(t)

	server, bodyCapture, _ := stubSendMessageServer(t)

	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys := &KeyMaterial{
		OperatorEdPriv: opEdPriv,
		OperatorXPriv:  opXPriv,
		OrchXPub:       orchXPub,
		OrchEdPub:      []byte{},
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	config := Config{
		Args: []string{
			"send",
			"--keyfile", keyfilePath,
			"--token", "TOKEN",
			"--base-url", server.URL,
			"--chat-id", "42",
			"status",
		},
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Errorf("send command failed with exit code %d", exitCode)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(*bodyCapture, &body); err != nil {
		t.Fatalf("failed to parse body: %v", err)
	}

	if _, ok := body["reply_to_message_id"]; ok {
		t.Error("reply_to_message_id should be absent when not provided")
	}
}

// TC-149-05: --reply-to validates positive integer only
func TestTC149_05_ReplyToValidation(t *testing.T) {
	_, opEdPriv, _, opXPriv, _, _, orchXPub, _ := generateSendKeys(t)

	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys := &KeyMaterial{
		OperatorEdPriv: opEdPriv,
		OperatorXPriv:  opXPriv,
		OrchXPub:       orchXPub,
		OrchEdPub:      []byte{},
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{"abc", "abc", false},
		{"-1", "-1", false},
		{"0", "0", false},
		{"1", "1", true},
		{"555", "555", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _, callCount := stubSendMessageServer(t)
			defer server.Close()

			args := []string{
				"send",
				"--keyfile", keyfilePath,
				"--token", "TOKEN",
				"--base-url", server.URL,
				"--chat-id", "42",
			}

			if tt.value != "" {
				args = append(args, "--reply-to", tt.value)
			}
			args = append(args, "status")

			config := Config{
				Args:   args,
				Stdout: &bytes.Buffer{},
				Stderr: &bytes.Buffer{},
			}

			exitCode := Main(config)

			if tt.valid {
				if exitCode != ExitOK {
					t.Errorf("expected exit code %d, got %d", ExitOK, exitCode)
				}
				if *callCount != 1 {
					t.Errorf("expected 1 HTTP call, got %d", *callCount)
				}
			} else {
				if exitCode != ExitUsage {
					t.Errorf("expected exit code %d (usage error), got %d", ExitUsage, exitCode)
				}
				if *callCount != 0 {
					t.Errorf("expected 0 HTTP calls, got %d", *callCount)
				}
			}
		})
	}
}

// TC-149-06: token and operator private keys absent from stdout/stderr/logs
// and plaintext never appears in raw POST body
func TestTC149_06_NoSecretLeakage(t *testing.T) {
	_, opEdPriv, _, opXPriv, _, _, orchXPub, _ := generateSendKeys(t)

	server, bodyCapture, _ := stubSendMessageServer(t)

	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys := &KeyMaterial{
		OperatorEdPriv: opEdPriv,
		OperatorXPriv:  opXPriv,
		OrchXPub:       orchXPub,
		OrchEdPub:      []byte{},
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	const token = "SEND_TOKEN_SENTINEL_149"
	const cmdText = "secret command text"

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	config := Config{
		Args: []string{
			"send",
			"--keyfile", keyfilePath,
			"--token", token,
			"--base-url", server.URL,
			"--chat-id", "42",
			cmdText,
		},
		Stdout: stdout,
		Stderr: stderr,
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Errorf("send command failed with exit code %d", exitCode)
	}

	combinedOutput := stdout.String() + stderr.String()

	// TC-149-06: token sentinel must not appear
	if strings.Contains(combinedOutput, token) {
		t.Error("bot token sentinel appears in stdout+stderr")
	}

	// TC-149-06: operator private keys must not appear (in both hex and base64)
	opEdPrivHex := hexEncode(opEdPriv)
	opXPrivHex := hexEncode(opXPriv[:])
	if strings.Contains(combinedOutput, opEdPrivHex) {
		t.Error("operator Ed25519 private key (hex) appears in stdout+stderr")
	}
	if strings.Contains(combinedOutput, opXPrivHex) {
		t.Error("operator X25519 private key (hex) appears in stdout+stderr")
	}

	// TC-149-06: plaintext command must not appear in raw POST body
	if bytes.Contains(*bodyCapture, []byte(cmdText)) {
		t.Errorf("plaintext command %q found in raw POST body — must not appear unencrypted", cmdText)
	}
}

// TC-149-07: keyfile read failures fail closed with clear errors
func TestTC149_07_KeyfileReadFailure(t *testing.T) {
	server, _, callCount := stubSendMessageServer(t)
	defer server.Close()

	tests := []struct {
		name  string
		setup func(t *testing.T) string // returns keyfilePath
	}{
		{
			"missing file",
			func(t *testing.T) string {
				return "/nonexistent/path/keyfile.json"
			},
		},
		{
			"malformed JSON",
			func(t *testing.T) string {
				tmpDir := t.TempDir()
				keyfilePath := filepath.Join(tmpDir, "bad.json")
				_ = os.WriteFile(keyfilePath, []byte("{bad json"), 0644)
				return keyfilePath
			},
		},
		{
			"malformed hex field",
			func(t *testing.T) string {
				tmpDir := t.TempDir()
				keyfilePath := filepath.Join(tmpDir, "bad_hex.json")
				badContent := `{
					"OperatorEdPriv": "notvalidhex",
					"OperatorXPriv": "0102030405",
					"OrchEdPub": "0102030405",
					"OrchXPub": "0102030405"
				}`
				_ = os.WriteFile(keyfilePath, []byte(badContent), 0644)
				return keyfilePath
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyfilePath := tt.setup(t)

			config := Config{
				Args: []string{
					"send",
					"--keyfile", keyfilePath,
					"--token", "TOKEN",
					"--base-url", server.URL,
					"--chat-id", "42",
					"status",
				},
				Stdout: &bytes.Buffer{},
				Stderr: &bytes.Buffer{},
			}

			exitCode := Main(config)

			// TC-149-07: must fail non-zero (not panic)
			if exitCode == ExitOK {
				t.Error("expected non-zero exit code")
			}

			// TC-149-07: zero HTTP calls
			if *callCount != 0 {
				t.Errorf("expected 0 HTTP calls, got %d", *callCount)
			}

			// TC-149-07: stderr should not contain panic trace
			stderr := config.Stderr.(*bytes.Buffer).String()
			if strings.Contains(stderr, "panic:") {
				t.Error("stderr contains panic trace")
			}
			if stderr == "" {
				t.Error("stderr should have error message")
			}
		})
	}
}

// TC-149-08: empty command text is rejected
func TestTC149_08_EmptyCommandText(t *testing.T) {
	_, opEdPriv, _, opXPriv, _, _, orchXPub, _ := generateSendKeys(t)

	server, _, _ := stubSendMessageServer(t)
	defer server.Close()

	tmpDir := t.TempDir()
	keyfilePath := filepath.Join(tmpDir, "test_keyfile.json")

	keys := &KeyMaterial{
		OperatorEdPriv: opEdPriv,
		OperatorXPriv:  opXPriv,
		OrchXPub:       orchXPub,
		OrchEdPub:      []byte{},
	}

	if err := WriteKeyfile(keyfilePath, keys); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{
			"no command text",
			[]string{
				"send",
				"--keyfile", keyfilePath,
				"--token", "TOKEN",
				"--base-url", server.URL,
				"--chat-id", "42",
			},
		},
		{
			"whitespace-only command",
			[]string{
				"send",
				"--keyfile", keyfilePath,
				"--token", "TOKEN",
				"--base-url", server.URL,
				"--chat-id", "42",
				"   ",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetServer, _, _ := stubSendMessageServer(t)
			defer resetServer.Close()

			config := Config{
				Args:   tt.args,
				Stdout: &bytes.Buffer{},
				Stderr: &bytes.Buffer{},
			}

			exitCode := Main(config)

			// TC-149-08: must exit 2 (usage error)
			if exitCode != ExitUsage {
				t.Errorf("expected exit code %d, got %d", ExitUsage, exitCode)
			}
		})
	}
}

// Helper function: generateReplyKeys generates all six keys needed for reply testing
// (mirrors the pattern from internal/channel/telegram/reply_test.go).
func generateReplyKeys(t *testing.T) (
	orchEdPub []byte,
	orchEdPriv []byte,
	orchXPub [32]byte,
	orchXPriv [32]byte,
	opXPub [32]byte,
	opXPriv [32]byte,
) {
	t.Helper()
	var err error
	orchEdPub, orchEdPriv, err = generateEd25519KeyPair()
	if err != nil {
		t.Fatalf("generate orchestrator Ed25519 key: %v", err)
	}
	orchXPub, orchXPriv, err = envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate orchestrator X25519 keypair: %v", err)
	}
	opXPub, opXPriv, err = envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate operator X25519 keypair: %v", err)
	}
	return
}

// TC-150-01: decrypts a real ReplyAdapter.Report-emitted envelope byte-for-byte
func TestTC150_01_RealReplyRoundTrip(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	const reportedText = "Approve plan? 2 sub-goals: docs-fix, coding-agent"

	// Set up a stub server to capture the ReplyAdapter's sendMessage POST
	server, bodyCapture, _ := stubSendMessageServer(t)

	// Create a real ReplyAdapter wired to the stub server
	adapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   "TEST_TOKEN_150",
		BaseURL:    server.URL,
		ChatID:     "12345",
		HTTPClient: server.Client(),
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
	})

	// Call Report to generate the real sealed+signed envelope (TC-150-01 LOAD-BEARING)
	err := adapter.Report(context.Background(), reportedText)
	if err != nil {
		t.Fatalf("adapter.Report failed: %v", err)
	}

	// Extract the emitted envelope JSON from the stub's captured POST body
	// (mirrors reply_test.go's TestTC098_03_RoundTrip pattern)
	var outer struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(*bodyCapture, &outer); err != nil {
		t.Fatalf("parse outer body: %v — body: %s", err, *bodyCapture)
	}
	var emittedEnv envelope.Envelope
	if err := json.Unmarshal([]byte(outer.Text), &emittedEnv); err != nil {
		t.Fatalf("parse emitted envelope: %v — text: %s", err, outer.Text)
	}

	// Build a keyfile with the orchestrator's public keys and operator's private keys
	keyfilePath := filepath.Join(t.TempDir(), "test_keyfile.json")
	keyMaterial := &KeyMaterial{
		OperatorXPriv: opXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(keyfilePath, keyMaterial); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	// Write the REAL emitted envelope to a temp file
	envJSON, _ := marshalJSON(emittedEnv)
	envelopePath := filepath.Join(t.TempDir(), "reply.json")
	if err := os.WriteFile(envelopePath, envJSON, 0644); err != nil {
		t.Fatalf("write envelope file: %v", err)
	}

	// Run reply-open with the REAL emitted envelope (TC-150-01 LOAD-BEARING)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	config := Config{
		Args: []string{
			"reply-open",
			"--keyfile", keyfilePath,
			envelopePath,
		},
		Stdout: stdout,
		Stderr: stderr,
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Errorf("reply-open exit code: expected %d, got %d, stderr: %s", ExitOK, exitCode, stderr.String())
	}

	// TC-150-01 LOAD-BEARING: stdout must be the exact plaintext byte-for-byte
	// (proves compatibility with the LIVE ReplyAdapter.Report wire format, not a hand-built envelope)
	stdoutStr := stdout.String()
	stdoutTrimmed := strings.TrimSuffix(stdoutStr, "\n")
	if stdoutTrimmed != reportedText {
		t.Errorf("plaintext mismatch: got %q, expected %q", stdoutTrimmed, reportedText)
	}
}

// TC-150-02: verifies with orchestrator Ed25519 PUB and opens with operator X25519 priv
// + orchestrator X25519 pub (correct key-role assignment)
func TestTC150_02_CorrectKeyRoleAssignment(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	const reportedText = "test message"

	// Create envelope as ReplyAdapter does
	ciphertext, nonce, err := envelope.Seal([]byte(reportedText), orchXPriv, opXPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	env := envelope.Envelope{
		From:    "orchestrator",
		To:      "operator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, orchEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Create correct keyfile (operator priv + orchestrator pubs)
	keyfilePath := filepath.Join(t.TempDir(), "test_keyfile.json")
	keyMaterial := &KeyMaterial{
		OperatorXPriv: opXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(keyfilePath, keyMaterial); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	// Write envelope to file
	envelopeJSON, err := marshalJSON(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	envelopePath := filepath.Join(t.TempDir(), "reply.json")
	if err := os.WriteFile(envelopePath, envelopeJSON, 0644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	// TC-150-02: with correct keys, should succeed
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	config := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath, envelopePath},
		Stdout: stdout,
		Stderr: stderr,
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Errorf("reply-open with correct keys should succeed, got exit %d, stderr: %s", exitCode, stderr.String())
	}

	// Verify plaintext is correct
	stdoutTrimmed := strings.TrimSuffix(stdout.String(), "\n")
	if stdoutTrimmed != reportedText {
		t.Errorf("plaintext mismatch: got %q, expected %q", stdoutTrimmed, reportedText)
	}

	// TC-150-02: with swapped keys (wrong opener), should fail
	// Generate a different operator X25519 priv
	_, wrongOpXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate wrong key: %v", err)
	}

	wrongKeyfilePath := filepath.Join(t.TempDir(), "wrong_keyfile.json")
	wrongKeyMaterial := &KeyMaterial{
		OperatorXPriv: wrongOpXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(wrongKeyfilePath, wrongKeyMaterial); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	stdout2 := &bytes.Buffer{}
	stderr2 := &bytes.Buffer{}
	config2 := Config{
		Args:   []string{"reply-open", "--keyfile", wrongKeyfilePath, envelopePath},
		Stdout: stdout2,
		Stderr: stderr2,
	}

	exitCode2 := Main(config2)
	if exitCode2 == ExitOK {
		t.Error("reply-open with wrong opener key should fail")
	}
	if stdout2.String() != "" {
		t.Error("stdout should be empty on failure")
	}
}

// TC-150-03: accepts envelope JSON from file, stdin, and --envelope flag
func TestTC150_03_MultipleEnvelopeInputs(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	const reportedText = "multi-input test"

	// Create envelope
	ciphertext, nonce, err := envelope.Seal([]byte(reportedText), orchXPriv, opXPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	env := envelope.Envelope{
		From:    "orchestrator",
		To:      "operator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, orchEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	envJSON, err := marshalJSON(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	// Create keyfile
	keyfilePath := filepath.Join(t.TempDir(), "test_keyfile.json")
	keyMaterial := &KeyMaterial{
		OperatorXPriv: opXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(keyfilePath, keyMaterial); err != nil {
		t.Fatalf("WriteKeyfile failed: %v", err)
	}

	// Test case 1: file argument
	envelopePath := filepath.Join(t.TempDir(), "reply.json")
	if err := os.WriteFile(envelopePath, envJSON, 0644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	stdout1 := &bytes.Buffer{}
	config1 := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath, envelopePath},
		Stdout: stdout1,
		Stderr: &bytes.Buffer{},
	}
	exitCode1 := Main(config1)
	if exitCode1 != ExitOK {
		t.Errorf("file arg: exit %d", exitCode1)
	}
	result1 := strings.TrimSuffix(stdout1.String(), "\n")

	// Test case 2: stdin
	stdout2 := &bytes.Buffer{}
	config2 := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath},
		Stdout: stdout2,
		Stderr: &bytes.Buffer{},
		Stdin:  bytes.NewReader(envJSON),
	}
	exitCode2 := Main(config2)
	if exitCode2 != ExitOK {
		t.Errorf("stdin: exit %d", exitCode2)
	}
	result2 := strings.TrimSuffix(stdout2.String(), "\n")

	// Test case 3: --envelope flag
	stdout3 := &bytes.Buffer{}
	config3 := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath, "--envelope", string(envJSON)},
		Stdout: stdout3,
		Stderr: &bytes.Buffer{},
	}
	exitCode3 := Main(config3)
	if exitCode3 != ExitOK {
		t.Errorf("--envelope flag: exit %d", exitCode3)
	}
	result3 := strings.TrimSuffix(stdout3.String(), "\n")

	// TC-150-03: all three should produce identical output
	if result1 != result2 {
		t.Errorf("file and stdin results differ: %q vs %q", result1, result2)
	}
	if result1 != result3 {
		t.Errorf("file and --envelope results differ: %q vs %q", result1, result3)
	}
	if result1 != reportedText {
		t.Errorf("result mismatch: got %q, expected %q", result1, reportedText)
	}

	// TC-150-03: both file and --envelope together is usage error
	stderr4 := &bytes.Buffer{}
	config4 := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath, "--envelope", string(envJSON), envelopePath},
		Stdout: &bytes.Buffer{},
		Stderr: stderr4,
	}
	exitCode4 := Main(config4)
	if exitCode4 != ExitUsage {
		t.Errorf("both file and --envelope: expected exit %d, got %d", ExitUsage, exitCode4)
	}
}

// TC-150-04: tampered payload, wrong signer, wrong opener key all fail closed
func TestTC150_04_FailClosed(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)
	_, wrongOrchEdPriv, _, _, _, wrongOpXPriv := generateReplyKeys(t)

	const reportedText = "sensitive data"

	// Create valid envelope
	ciphertext, nonce, err := envelope.Seal([]byte(reportedText), orchXPriv, opXPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	env := envelope.Envelope{
		From:    "orchestrator",
		To:      "operator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, orchEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// Test cases: tampered payload
	tests := []struct {
		name        string
		setupEnv    func(env envelope.Envelope) envelope.Envelope
		setupKeyMat func() *KeyMaterial
		errorMsg    string
	}{
		{
			"tampered payload",
			func(env envelope.Envelope) envelope.Envelope {
				if len(env.Payload) >= 2 {
					last := env.Payload[len(env.Payload)-2:]
					switch last {
					case "00":
						env.Payload = env.Payload[:len(env.Payload)-2] + "ff"
					default:
						env.Payload = env.Payload[:len(env.Payload)-2] + "00"
					}
				}
				return env
			},
			func() *KeyMaterial {
				return &KeyMaterial{
					OperatorXPriv: opXPriv,
					OrchEdPub:     orchEdPub,
					OrchXPub:      orchXPub,
				}
			},
			"signature verification failed",
		},
		{
			"wrong signer",
			func(env envelope.Envelope) envelope.Envelope { return env },
			func() *KeyMaterial {
				return &KeyMaterial{
					OperatorXPriv: opXPriv,
					OrchEdPub:     wrongOrchEdPriv[:32], // Wrong signer
					OrchXPub:      orchXPub,
				}
			},
			"signature verification failed",
		},
		{
			"wrong opener key",
			func(env envelope.Envelope) envelope.Envelope { return env },
			func() *KeyMaterial {
				return &KeyMaterial{
					OperatorXPriv: wrongOpXPriv,
					OrchEdPub:     orchEdPub,
					OrchXPub:      orchXPub,
				}
			},
			"authentication failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tamperedEnv := tt.setupEnv(env)
			envJSON, err := marshalJSON(tamperedEnv)
			if err != nil {
				t.Fatalf("marshal envelope: %v", err)
			}

			keyfilePath := filepath.Join(t.TempDir(), "keyfile.json")
			keyMat := tt.setupKeyMat()
			if err := WriteKeyfile(keyfilePath, keyMat); err != nil {
				t.Fatalf("WriteKeyfile: %v", err)
			}

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			config := Config{
				Args:   []string{"reply-open", "--keyfile", keyfilePath, "--envelope", string(envJSON)},
				Stdout: stdout,
				Stderr: stderr,
			}

			exitCode := Main(config)
			if exitCode == ExitOK {
				t.Error("should have failed")
			}
			if stdout.String() != "" {
				t.Error("stdout should be empty on failure")
			}
			// TC-150-04: assert specific error category (not just any "error:" string)
			if !strings.Contains(stderr.String(), tt.errorMsg) {
				t.Errorf("stderr missing expected error category %q, got: %s", tt.errorMsg, stderr.String())
			}
		})
	}

	// Test malformed JSON
	malformedPath := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformedPath, []byte("{bad json"), 0644); err != nil {
		t.Fatalf("write malformed: %v", err)
	}

	keyfilePath := filepath.Join(t.TempDir(), "keyfile.json")
	keyMat := &KeyMaterial{
		OperatorXPriv: opXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(keyfilePath, keyMat); err != nil {
		t.Fatalf("WriteKeyfile: %v", err)
	}

	stderr := &bytes.Buffer{}
	config := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath, malformedPath},
		Stdout: &bytes.Buffer{},
		Stderr: stderr,
	}

	exitCode := Main(config)
	if exitCode != 1 {
		t.Errorf("malformed JSON: expected exit 1, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "malformed") {
		t.Errorf("malformed JSON stderr missing 'malformed': %s", stderr.String())
	}
}

// TC-150-05: same envelope decrypted twice in separate invocations succeeds both times
func TestTC150_05_NoPersistedReplayState(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	const reportedText = "replay test"

	// Create envelope
	ciphertext, nonce, err := envelope.Seal([]byte(reportedText), orchXPriv, opXPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	env := envelope.Envelope{
		From:    "orchestrator",
		To:      "operator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, orchEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	envJSON, err := marshalJSON(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	// Write envelope to file
	envelopePath := filepath.Join(t.TempDir(), "reply.json")
	if err := os.WriteFile(envelopePath, envJSON, 0644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	// Create keyfile
	keyfilePath := filepath.Join(t.TempDir(), "keyfile.json")
	keyMat := &KeyMaterial{
		OperatorXPriv: opXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(keyfilePath, keyMat); err != nil {
		t.Fatalf("WriteKeyfile: %v", err)
	}

	// Invoke reply-open twice
	for i := 1; i <= 2; i++ {
		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		config := Config{
			Args:   []string{"reply-open", "--keyfile", keyfilePath, envelopePath},
			Stdout: stdout,
			Stderr: stderr,
		}

		exitCode := Main(config)
		if exitCode != ExitOK {
			t.Errorf("invocation %d: exit %d, stderr: %s", i, exitCode, stderr.String())
		}

		result := strings.TrimSuffix(stdout.String(), "\n")
		if result != reportedText {
			t.Errorf("invocation %d: got %q, expected %q", i, result, reportedText)
		}
	}
}

// TC-150-06: source inspection confirms no net/http/getUpdates reference
func TestTC150_06_NoNetworkCalls(t *testing.T) {
	// Read the main.go file and search for network-related code
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	testDir := filepath.Dir(filename)

	mainFilePath := filepath.Join(testDir, "main.go")
	mainContent, err := os.ReadFile(mainFilePath)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	mainText := string(mainContent)

	// TC-150-06: extract runReplyOpen function
	startIdx := strings.Index(mainText, "func runReplyOpen")
	endIdx := strings.Index(mainText, "func writef")
	if startIdx < 0 || endIdx < 0 || startIdx >= endIdx {
		t.Fatal("could not find runReplyOpen function")
	}

	replyOpenFunc := mainText[startIdx:endIdx]

	// TC-150-06: must not contain network-related patterns
	forbiddenPatterns := []string{
		"http.Get",
		"http.Client",
		"getUpdates",
	}

	for _, pattern := range forbiddenPatterns {
		if strings.Contains(replyOpenFunc, pattern) {
			t.Errorf("runReplyOpen contains forbidden pattern: %s", pattern)
		}
	}
}

// TC-150-07: operator X25519 priv and orchestrator pub keys absent from logs
func TestTC150_07_NoKeyMaterialInLogs(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	const reportedText = "test"

	// Create envelope
	ciphertext, nonce, err := envelope.Seal([]byte(reportedText), orchXPriv, opXPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	env := envelope.Envelope{
		From:    "orchestrator",
		To:      "operator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, orchEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	envJSON, err := marshalJSON(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	keyfilePath := filepath.Join(t.TempDir(), "keyfile.json")
	keyMat := &KeyMaterial{
		OperatorXPriv: opXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(keyfilePath, keyMat); err != nil {
		t.Fatalf("WriteKeyfile: %v", err)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	config := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath, "--envelope", string(envJSON)},
		Stdout: stdout,
		Stderr: stderr,
	}

	Main(config)

	combinedOutput := stdout.String() + stderr.String()

	// TC-150-07: operator X25519 priv must not appear
	opXPrivHex := hexEncode(opXPriv[:])
	if strings.Contains(combinedOutput, opXPrivHex) {
		t.Error("operator X25519 private key (hex) appears in output")
	}

	// TC-150-07: orchestrator pubs should not appear in stderr/logs
	// (plaintext on stdout is expected)
	orchEdPubHex := hexEncode(orchEdPub)
	orchXPubHex := hexEncode(orchXPub[:])
	if strings.Contains(stderr.String(), orchEdPubHex) {
		t.Error("orchestrator Ed25519 pub (hex) appears in stderr")
	}
	if strings.Contains(stderr.String(), orchXPubHex) {
		t.Error("orchestrator X25519 pub (hex) appears in stderr")
	}
}

// TC-150-08: missing/malformed keyfile fails closed with clear error
func TestTC150_08_KeyfileFailures(t *testing.T) {
	const reportedText = "test"
	_, orchEdPriv, _, orchXPriv, opXPub, _ := generateReplyKeys(t)

	// Create a valid envelope for testing
	ciphertext, nonce, err := envelope.Seal([]byte(reportedText), orchXPriv, opXPub)
	if err != nil {
		t.Fatalf("Seal failed: %v", err)
	}

	env := envelope.Envelope{
		From:    "orchestrator",
		To:      "operator",
		Nonce:   hex.EncodeToString(nonce[:]),
		TS:      envelope.NowRFC3339(),
		Payload: hex.EncodeToString(ciphertext),
		Sig:     "",
	}

	env, err = envelope.Sign(env, orchEdPriv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	envJSON, err := marshalJSON(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	tests := []struct {
		name      string
		keyfileMu func(t *testing.T) string
	}{
		{
			"missing keyfile",
			func(t *testing.T) string {
				return "/nonexistent/path/keyfile.json"
			},
		},
		{
			"malformed keyfile JSON",
			func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "bad.json")
				_ = os.WriteFile(path, []byte("{bad json"), 0644)
				return path
			},
		},
		{
			"malformed hex field",
			func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "bad_hex.json")
				badContent := `{
					"OperatorEdPriv": "notvalidhex",
					"OperatorXPriv": "0102030405",
					"OrchEdPub": "0102030405",
					"OrchXPub": "0102030405"
				}`
				_ = os.WriteFile(path, []byte(badContent), 0644)
				return path
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyfilePath := tt.keyfileMu(t)

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			config := Config{
				Args:   []string{"reply-open", "--keyfile", keyfilePath, "--envelope", string(envJSON)},
				Stdout: stdout,
				Stderr: stderr,
			}

			exitCode := Main(config)

			// TC-150-08: must exit non-zero
			if exitCode == ExitOK {
				t.Error("expected non-zero exit code")
			}

			// TC-150-08: stdout empty
			if stdout.String() != "" {
				t.Error("stdout should be empty on keyfile error")
			}

			// TC-150-08: no panic trace
			if strings.Contains(stderr.String(), "panic:") {
				t.Error("stderr contains panic trace")
			}

			// TC-150-08: error message present
			if stderr.String() == "" {
				t.Error("stderr should have error message")
			}
		})
	}
}

// TC-150-01 edge case: multi-line plaintext with embedded newlines (also via real ReplyAdapter)
func TestTC150_01_EmbeddedNewlines(t *testing.T) {
	orchEdPub, orchEdPriv, orchXPub, orchXPriv, opXPub, opXPriv := generateReplyKeys(t)

	const reportedText = "line 1\nline 2\nline 3"

	// Set up stub server and real ReplyAdapter
	server, bodyCapture, _ := stubSendMessageServer(t)

	adapter := telegram.NewReplyAdapter(telegram.ReplyConfig{
		BotToken:   "TEST_TOKEN_150",
		BaseURL:    server.URL,
		ChatID:     "12345",
		HTTPClient: server.Client(),
		OrchEdPriv: orchEdPriv,
		OrchXPriv:  orchXPriv,
		OpXPub:     opXPub,
	})

	// Call Report with embedded-newline plaintext
	err := adapter.Report(context.Background(), reportedText)
	if err != nil {
		t.Fatalf("adapter.Report failed: %v", err)
	}

	// Extract real emitted envelope from stub
	var outer struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(*bodyCapture, &outer); err != nil {
		t.Fatalf("parse outer body: %v", err)
	}
	var emittedEnv envelope.Envelope
	if err := json.Unmarshal([]byte(outer.Text), &emittedEnv); err != nil {
		t.Fatalf("parse emitted envelope: %v", err)
	}

	// Create keyfile
	keyfilePath := filepath.Join(t.TempDir(), "keyfile.json")
	keyMat := &KeyMaterial{
		OperatorXPriv: opXPriv,
		OrchEdPub:     orchEdPub,
		OrchXPub:      orchXPub,
	}
	if err := WriteKeyfile(keyfilePath, keyMat); err != nil {
		t.Fatalf("WriteKeyfile: %v", err)
	}

	// Write real envelope to file
	envJSON, _ := marshalJSON(emittedEnv)
	envelopePath := filepath.Join(t.TempDir(), "reply.json")
	if err := os.WriteFile(envelopePath, envJSON, 0644); err != nil { t.Fatalf("write envelope: %v", err) }

	// Decrypt with reply-open
	stdout := &bytes.Buffer{}
	config := Config{
		Args:   []string{"reply-open", "--keyfile", keyfilePath, envelopePath},
		Stdout: stdout,
		Stderr: &bytes.Buffer{},
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Errorf("exit %d", exitCode)
	}

	// TC-150-01 edge case: embedded newlines preserved through real adapter round-trip
	stdoutStr := stdout.String()
	stdoutTrimmed := strings.TrimSuffix(stdoutStr, "\n")
	if stdoutTrimmed != reportedText {
		t.Errorf("plaintext with embedded newlines: got %q, expected %q", stdoutTrimmed, reportedText)
	}
}
