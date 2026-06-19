package cli_test

// Tests for the agent-builder verify-checkpoint subcommand.
//
// TC-069-03: verify-checkpoint exits 0/1/2 with a fake audit-trail binary.
// TC-069-04: --logfile flag threads through to binary argv; omitted when absent.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildFakeAuditTrail writes a shell-script fake audit-trail binary into dir
// and returns its path. The script checks that its first two argv are
// "checkpoint verify" and returns the given exit code plus JSON stdout.
// It also writes the full argv to a capture file so tests can assert on it.
func buildFakeAuditTrail(t *testing.T, dir string, exitCode int, jsonOut string) string {
	t.Helper()
	captureFile := filepath.Join(dir, "argv-capture.txt")
	script := "#!/bin/sh\n" +
		// Capture full argv to file for assertion
		"echo \"$@\" > " + captureFile + "\n" +
		// Emit the configured JSON to stdout
		"printf '%s\\n' '" + jsonOut + "'\n" +
		// Exit with the configured code
		"exit " + itoa(exitCode) + "\n"

	binPath := filepath.Join(dir, "audit-trail")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake audit-trail: %v", err)
	}
	return binPath
}

// itoa converts a small non-negative int to its decimal string representation.
// Avoids importing strconv in a test helper.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// readArgvCapture reads the argv capture file written by the fake binary and
// returns the space-separated argv string, trimmed.
func readArgvCapture(t *testing.T, dir string) string {
	t.Helper()
	captureFile := filepath.Join(dir, "argv-capture.txt")
	data, err := os.ReadFile(captureFile)
	if err != nil {
		t.Fatalf("read argv capture: %v (was the fake binary invoked?)", err)
	}
	return strings.TrimSpace(string(data))
}

// TC-069-03: valid checkpoint — exits 0.
func TestVerifyCheckpointExitsZeroWhenValid(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	fakeBin := buildFakeAuditTrail(t, dir, 0, `{"valid":true,"message":"signature ok"}`)

	// Create stub files so the CLI can stat them (AGENT_BUILDER_AUDIT_BIN check)
	cpPath := filepath.Join(dir, "cp.json")
	pubPath := filepath.Join(dir, "pub.pem")
	writeFile(t, cpPath, `{}`)
	writeFile(t, pubPath, "-----BEGIN PUBLIC KEY-----\n-----END PUBLIC KEY-----\n")

	stdout, stderr, code := runBinary(t, binary,
		[]string{"AGENT_BUILDER_AUDIT_BIN=" + fakeBin},
		"verify-checkpoint",
		"--checkpoint", cpPath,
		"--public-key", pubPath,
	)
	t.Logf("TC-069-03 valid: stdout=%q stderr=%q exit=%d", stdout, stderr, code)

	if code != 0 {
		t.Fatalf("TC-069-03 valid: exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "valid") {
		t.Fatalf("TC-069-03 valid: stdout = %q, want JSON with 'valid'", stdout)
	}
}

// TC-069-03: invalid checkpoint — exits 1.
func TestVerifyCheckpointExitsOneWhenInvalid(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	fakeBin := buildFakeAuditTrail(t, dir, 1, `{"valid":false,"message":"bad sig"}`)

	cpPath := filepath.Join(dir, "cp.json")
	pubPath := filepath.Join(dir, "pub.pem")
	writeFile(t, cpPath, `{}`)
	writeFile(t, pubPath, "-----BEGIN PUBLIC KEY-----\n-----END PUBLIC KEY-----\n")

	_, stderr, code := runBinary(t, binary,
		[]string{"AGENT_BUILDER_AUDIT_BIN=" + fakeBin},
		"verify-checkpoint",
		"--checkpoint", cpPath,
		"--public-key", pubPath,
	)
	t.Logf("TC-069-03 invalid: stderr=%q exit=%d", stderr, code)

	if code != 1 {
		t.Fatalf("TC-069-03 invalid: exit code = %d, want 1", code)
	}
}

// TC-069-03: missing --checkpoint flag — exits 2 with usage error.
func TestVerifyCheckpointExitsTwoMissingCheckpoint(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "pub.pem")
	writeFile(t, pubPath, "-----BEGIN PUBLIC KEY-----\n-----END PUBLIC KEY-----\n")

	_, stderr, code := runBinary(t, binary, nil,
		"verify-checkpoint",
		"--public-key", pubPath,
	)
	t.Logf("TC-069-03 missing-checkpoint: stderr=%q exit=%d", stderr, code)

	if code != 2 {
		t.Fatalf("TC-069-03 missing-checkpoint: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage") {
		t.Fatalf("TC-069-03 missing-checkpoint: stderr = %q, want usage error", stderr)
	}
}

// TC-069-03: missing --public-key flag — exits 2 with usage error.
func TestVerifyCheckpointExitsTwoMissingPublicKey(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")
	writeFile(t, cpPath, `{}`)

	_, stderr, code := runBinary(t, binary, nil,
		"verify-checkpoint",
		"--checkpoint", cpPath,
	)
	t.Logf("TC-069-03 missing-pubkey: stderr=%q exit=%d", stderr, code)

	if code != 2 {
		t.Fatalf("TC-069-03 missing-pubkey: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage") {
		t.Fatalf("TC-069-03 missing-pubkey: stderr = %q, want usage error", stderr)
	}
}

// TC-069-03: AGENT_BUILDER_AUDIT_BIN points at a nonexistent path — exits 2.
func TestVerifyCheckpointExitsTwoBinaryNotResolvable(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")
	pubPath := filepath.Join(dir, "pub.pem")
	writeFile(t, cpPath, `{}`)
	writeFile(t, pubPath, "-----BEGIN PUBLIC KEY-----\n-----END PUBLIC KEY-----\n")

	_, stderr, code := runBinary(t, binary,
		[]string{
			"AGENT_BUILDER_AUDIT_BIN=/nonexistent/path/audit-trail",
			// Remove PATH so audit-trail is also not found there
			"PATH=/nonexistent",
		},
		"verify-checkpoint",
		"--checkpoint", cpPath,
		"--public-key", pubPath,
	)
	t.Logf("TC-069-03 bad-bin: stderr=%q exit=%d", stderr, code)

	if code != 2 {
		t.Fatalf("TC-069-03 bad-bin: exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "usage") {
		t.Fatalf("TC-069-03 bad-bin: stderr = %q, want usage error mentioning binary resolution", stderr)
	}
}

// TC-069-04: --logfile is threaded through to the binary argv.
func TestVerifyCheckpointLogfileThreadedThrough(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	fakeBin := buildFakeAuditTrail(t, dir, 0, `{"valid":true,"message":"ok"}`)

	cpPath := filepath.Join(dir, "cp.json")
	pubPath := filepath.Join(dir, "pub.pem")
	logPath := filepath.Join(dir, "audit.log")
	writeFile(t, cpPath, `{}`)
	writeFile(t, pubPath, "-----BEGIN PUBLIC KEY-----\n-----END PUBLIC KEY-----\n")
	writeFile(t, logPath, "")

	stdout, stderr, code := runBinary(t, binary,
		[]string{"AGENT_BUILDER_AUDIT_BIN=" + fakeBin},
		"verify-checkpoint",
		"--checkpoint", cpPath,
		"--public-key", pubPath,
		"--logfile", logPath,
	)
	t.Logf("TC-069-04 with-logfile: stdout=%q stderr=%q exit=%d", stdout, stderr, code)

	if code != 0 {
		t.Fatalf("TC-069-04 with-logfile: exit code = %d, want 0", code)
	}

	// Confirm --logfile was passed to the fake binary.
	argv := readArgvCapture(t, dir)
	if !strings.Contains(argv, "--logfile") {
		t.Errorf("TC-069-04 with-logfile: --logfile not present in binary argv: %q", argv)
	}
	if !strings.Contains(argv, logPath) {
		t.Errorf("TC-069-04 with-logfile: logfile path %q not in binary argv: %q", logPath, argv)
	}
}

// TC-069-04: without --logfile, the flag is NOT passed to the binary argv.
func TestVerifyCheckpointLogfileOmittedWhenAbsent(t *testing.T) {
	binary := buildBinary(t)
	dir := t.TempDir()
	fakeBin := buildFakeAuditTrail(t, dir, 0, `{"valid":true,"message":"ok"}`)

	cpPath := filepath.Join(dir, "cp.json")
	pubPath := filepath.Join(dir, "pub.pem")
	writeFile(t, cpPath, `{}`)
	writeFile(t, pubPath, "-----BEGIN PUBLIC KEY-----\n-----END PUBLIC KEY-----\n")

	_, _, code := runBinary(t, binary,
		[]string{"AGENT_BUILDER_AUDIT_BIN=" + fakeBin},
		"verify-checkpoint",
		"--checkpoint", cpPath,
		"--public-key", pubPath,
		// no --logfile
	)

	if code != 0 {
		t.Fatalf("TC-069-04 no-logfile: exit code = %d, want 0", code)
	}

	argv := readArgvCapture(t, dir)
	if strings.Contains(argv, "--logfile") {
		t.Errorf("TC-069-04 no-logfile: --logfile present in binary argv but flag was not passed: %q", argv)
	}
}

// TC-069-06 (partial): existing verify subcommand is unaffected.
// This mirrors TestVerifySubcommand which is referenced in the test spec.
func TestVerifySubcommandUnaffected(t *testing.T) {
	binary := buildBinary(t)
	// A clean repo with no go.sum so all gate steps pass with shims.
	cleanRepo := writeRepo(t, "clean069", false)
	path := writeToolShims(t)

	stdout, stderr, code := runBinary(t, binary, []string{"PATH=" + path}, "verify", cleanRepo)
	t.Logf("TC-069-06 verify unaffected: stdout=%q stderr=%q exit=%d", stdout, stderr, code)
	if code != 0 {
		t.Fatalf("TC-069-06: verify exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "verification passed:") {
		t.Fatalf("TC-069-06: stdout = %q, want verification passed", stdout)
	}
}
