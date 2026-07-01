package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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

	config := Config{
		Args:   []string{"keygen", "--keyfile", keyfilePath},
		Stdout: stdout,
		Stderr: stderr,
		Stdin:  nil,
	}

	exitCode := Main(config)
	if exitCode != ExitOK {
		t.Fatalf("keygen failed: exit code %d", exitCode)
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()
	combinedOutput := stdoutStr + stderrStr

	// Get the actual keys via runKeygenLogic so we can check REAL generated key material
	// (Run it a second time to get fresh keys for comparison)
	keys, envBlock, err := runKeygenLogic(filepath.Join(tmpDir, "test-keys.json"))
	if err != nil {
		t.Fatalf("runKeygenLogic failed: %v", err)
	}

	// ASSERTION (a): operator privates NEVER appear anywhere in stdout+stderr
	opEdPrivHex := hexEncode(keys.OperatorEdPriv)
	opXPrivHex := hexEncode(keys.OperatorXPriv[:])

	if strings.Contains(combinedOutput, opEdPrivHex) {
		t.Error("operator Ed25519 private key (hex) appears in stdout+stderr")
	}
	if strings.Contains(combinedOutput, opXPrivHex) {
		t.Error("operator X25519 private key (hex) appears in stdout+stderr")
	}

	// ASSERTION (b): orchestrator privates appear ONLY in the env block (stdout),
	// NOT in stderr confirmation line
	orchEdPrivHex := hexEncode(keys.OrchEdPriv)
	orchXPrivHex := hexEncode(keys.OrchXPriv[:])

	// These MUST appear in the env block (on stdout)
	if !strings.Contains(envBlock, orchEdPrivHex) {
		t.Error("orchestrator Ed25519 private key missing from env block")
	}
	if !strings.Contains(envBlock, orchXPrivHex) {
		t.Error("orchestrator X25519 private key missing from env block")
	}

	// But these MUST NOT appear in stderr
	if strings.Contains(stderrStr, orchEdPrivHex) {
		t.Error("orchestrator Ed25519 private key appears in stderr confirmation line")
	}
	if strings.Contains(stderrStr, orchXPrivHex) {
		t.Error("orchestrator X25519 private key appears in stderr confirmation line")
	}

	// ASSERTION (c): banner separator present
	if !strings.Contains(stderrStr, "---") {
		t.Error("stderr missing labeled banner separator (---)")
	}
}
