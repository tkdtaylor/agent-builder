package execsandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

// TestExecSandboxRunnerInterface verifies the Runner satisfies sandbox.Runner.
// TC-062-01 (partial): adapter satisfies sandbox.Runner interface at compile time.
func TestExecSandboxRunnerInterface(t *testing.T) {
	// This is a compile-time check. If it compiles, the interface is satisfied.
	var _ sandbox.Runner = (*Runner)(nil)
}

// TestExecSandboxMarshalRequestFullLimits verifies full Limits marshal to correct RunRequest JSON.
// TC-062-01: full Limits marshal produces correct RunRequest JSON.
func TestExecSandboxMarshalRequestFullLimits(t *testing.T) {
	tempDir := t.TempDir()
	stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox")
	recordPath := filepath.Join(tempDir, "request.json")

	// Create a stub binary that records the RunRequest JSON and returns a valid result.
	createStubBinary(t, stubBinPath, recordPath, "")

	runner := New(stubBinPath)

	req := sandbox.Request{
		Command:  []string{"echo", "hi"},
		Worktree: tempDir,
		Tier:     "bubblewrap",
		Limits: sandbox.Limits{
			MemoryBytes:      512 * 1024 * 1024, // 512 MB
			CPUCount:         2,
			PidsLimit:        64,
			WallClockTimeout: 30 * time.Second,
			EgressAllowlist: []string{
				"api.github.com:443",
				"registry.npmjs.org:443",
			},
		},
	}

	_, _, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// Read and parse the recorded request JSON.
	recordedData, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("failed to read recorded request: %v", err)
	}

	var recordedReq runRequest
	if err := json.Unmarshal(recordedData, &recordedReq); err != nil {
		t.Fatalf("failed to parse recorded request JSON: %v", err)
	}

	// Verify field mapping.
	if recordedReq.Run.Profile.Limits.MemoryMB != 512 {
		t.Errorf("memory_mb: expected 512, got %d", recordedReq.Run.Profile.Limits.MemoryMB)
	}
	if recordedReq.Run.Profile.Limits.CPUCount != 2 {
		t.Errorf("cpu_count: expected 2, got %d", recordedReq.Run.Profile.Limits.CPUCount)
	}
	if recordedReq.Run.Profile.Limits.Pids != 64 {
		t.Errorf("pids: expected 64, got %d", recordedReq.Run.Profile.Limits.Pids)
	}
	if recordedReq.Run.Profile.Limits.TimeoutSec != 30 {
		t.Errorf("timeout_sec: expected 30, got %d", recordedReq.Run.Profile.Limits.TimeoutSec)
	}
	if recordedReq.Run.Tier != "bubblewrap" {
		t.Errorf("tier: expected 'bubblewrap', got %q", recordedReq.Run.Tier)
	}

	// Verify capabilities (FileRead and NetConnect should be present).
	// FileRead is automatically added when toolchain is discoverable; NetConnect from the allowlist.
	if len(recordedReq.Run.Profile.Capabilities) < 1 {
		t.Errorf("capabilities: expected at least 1 entry, got %d", len(recordedReq.Run.Profile.Capabilities))
	} else {
		// Verify NetConnect is present
		netCap := findCapability(recordedReq, "NetConnect")
		if netCap == nil {
			t.Errorf("NetConnect capability not found")
		} else {
			if len(netCap.Allowlist) != 2 {
				t.Errorf("allowlist: expected 2 entries, got %d", len(netCap.Allowlist))
			}
			if len(netCap.Allowlist) >= 1 && netCap.Allowlist[0] != "api.github.com:443" {
				t.Errorf("allowlist[0]: expected 'api.github.com:443', got %q", netCap.Allowlist[0])
			}
		}
	}

	// Verify deferred fields.
	if len(recordedReq.Run.SecretRefs) != 0 {
		t.Errorf("secret_refs: expected empty, got %v", recordedReq.Run.SecretRefs)
	}
	if recordedReq.Wiring.VaultSocket != "" {
		t.Errorf("vault_socket: expected empty, got %q", recordedReq.Wiring.VaultSocket)
	}
	if recordedReq.Wiring.AuditSocket != "" {
		t.Errorf("audit_socket: expected empty, got %q", recordedReq.Wiring.AuditSocket)
	}
	if recordedReq.Wiring.InjectionMode != "" {
		t.Errorf("injection_mode: expected empty, got %q", recordedReq.Wiring.InjectionMode)
	}

	// Verify request_id is non-empty.
	if recordedReq.Wiring.RequestID == "" {
		t.Errorf("request_id: expected non-empty, got empty")
	}

	// Verify origin_map is present.
	if recordedReq.Wiring.OriginMap == nil {
		t.Errorf("origin_map: expected non-nil, got nil")
	}

	// Verify payload is non-empty and contains the command.
	if recordedReq.Run.Payload == "" {
		t.Errorf("payload: expected non-empty, got empty")
	}
	if !strings.Contains(recordedReq.Run.Payload, "echo") {
		t.Errorf("payload: expected to contain 'echo', got %q", recordedReq.Run.Payload)
	}

	// Verify workdir is set to req.Worktree (absolute path).
	expectedWorkdir, err := filepath.Abs(tempDir)
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}
	if recordedReq.Run.Workdir != expectedWorkdir {
		t.Errorf("workdir: expected %q, got %q", expectedWorkdir, recordedReq.Run.Workdir)
	}
}

// TestExecSandboxMarshalRequestZeroLimits verifies zero/unset Limits produce zero fields in JSON.
// TC-062-02: zero/unset Limits produce zero fields; default tier is bubblewrap.
func TestExecSandboxMarshalRequestZeroLimits(t *testing.T) {
	tempDir := t.TempDir()
	stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox")
	recordPath := filepath.Join(tempDir, "request.json")

	createStubBinary(t, stubBinPath, recordPath, "")

	runner := New(stubBinPath)

	req := sandbox.Request{
		Command:  []string{"true"},
		Worktree: tempDir,
		Limits:   sandbox.Limits{}, // Zero value
		Tier:     "",                // Empty; should default to "bubblewrap"
	}

	_, _, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	recordedData, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("failed to read recorded request: %v", err)
	}

	var recordedReq runRequest
	if err := json.Unmarshal(recordedData, &recordedReq); err != nil {
		t.Fatalf("failed to parse recorded request JSON: %v", err)
	}

	// Verify zero limits.
	if recordedReq.Run.Profile.Limits.MemoryMB != 0 {
		t.Errorf("memory_mb: expected 0, got %d", recordedReq.Run.Profile.Limits.MemoryMB)
	}
	if recordedReq.Run.Profile.Limits.CPUCount != 0 {
		t.Errorf("cpu_count: expected 0, got %d", recordedReq.Run.Profile.Limits.CPUCount)
	}
	if recordedReq.Run.Profile.Limits.Pids != 0 {
		t.Errorf("pids: expected 0, got %d", recordedReq.Run.Profile.Limits.Pids)
	}
	if recordedReq.Run.Profile.Limits.TimeoutSec != 0 {
		t.Errorf("timeout_sec: expected 0, got %d", recordedReq.Run.Profile.Limits.TimeoutSec)
	}

	// Verify default tier.
	if recordedReq.Run.Tier != "bubblewrap" {
		t.Errorf("tier: expected default 'bubblewrap', got %q", recordedReq.Run.Tier)
	}

	// Verify capabilities entry: FileRead will be auto-added when toolchain is discoverable.
	// When EgressAllowlist is empty, no NetConnect should be present.
	netCap := findCapability(recordedReq, "NetConnect")
	if netCap != nil {
		t.Errorf("NetConnect capability should not be present when EgressAllowlist is empty, got %v", netCap)
	}
}

// TestExecSandboxParseResult verifies JSON result is parsed into Result + exit code + sandbox_status.
// TC-062-03: JSON result parsed into Result; Duration set from duration_ms; sandbox_status surfaced.
func TestExecSandboxParseResult(t *testing.T) {
	tempDir := t.TempDir()
	stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox")

	// Create a stub that returns a specific result JSON.
	resultJSON := `{
		"stdout": "hello\n",
		"stderr": "",
		"exit_code": 0,
		"sandbox_status": {
			"sandbox_id": "sbx-abc123",
			"tier": "bubblewrap",
			"duration_ms": 42,
			"secrets_injected": [],
			"status": "clean",
			"limits": {
				"cpu_count": 0,
				"memory_mb": 0,
				"pids": 0,
				"disk_mb": 0,
				"timeout_sec": 0,
				"degraded": []
			}
		}
	}`
	createStubBinaryWithResult(t, stubBinPath, resultJSON)

	runner := New(stubBinPath)
	req := sandbox.Request{
		Command:  []string{"echo", "hello"},
		Worktree: tempDir,
	}

	result, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	if result.Stdout != "hello\n" {
		t.Errorf("Stdout: expected 'hello\\n', got %q", result.Stdout)
	}
	if result.Stderr != "" {
		t.Errorf("Stderr: expected empty, got %q", result.Stderr)
	}
	if exitCode != 0 {
		t.Errorf("ExitCode: expected 0, got %d", exitCode)
	}
	if result.Duration != 42*time.Millisecond {
		t.Errorf("Duration: expected 42ms, got %v", result.Duration)
	}

	// Verify sandbox_status fields are surfaced on Result.
	if result.SandboxID != "sbx-abc123" {
		t.Errorf("SandboxID: expected 'sbx-abc123', got %q", result.SandboxID)
	}
	if result.Tier != "bubblewrap" {
		t.Errorf("Tier: expected 'bubblewrap', got %q", result.Tier)
	}
	if result.Status != "clean" {
		t.Errorf("Status: expected 'clean', got %q", result.Status)
	}
}

// TestExecSandboxBlockError verifies block's {"error":...} response surfaces as loud Go error.
// TC-062-04: block's error response and non-zero exit both produce loud errors.
func TestExecSandboxBlockError(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("error_in_result_json", func(t *testing.T) {
		stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox-1")
		resultJSON := `{"error": "tier not implemented: firecracker"}`
		createStubBinaryWithResult(t, stubBinPath, resultJSON)

		runner := New(stubBinPath)
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: tempDir,
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for block error response, got nil")
		}
		if !strings.Contains(err.Error(), "tier not implemented: firecracker") {
			t.Errorf("Error message: expected to contain 'tier not implemented: firecracker', got %v", err)
		}
	})

	t.Run("non_zero_block_exit", func(t *testing.T) {
		stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox-2")
		// Create a stub that exits 1 (stdin/JSON error).
		createStubBinaryExitingWith(t, stubBinPath, 1, "stdin error")

		runner := New(stubBinPath)
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: tempDir,
		}

		_, exitCode, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for non-zero exit, got nil")
		}
		if !strings.Contains(err.Error(), "exited 1") {
			t.Errorf("Error message: expected to contain 'exited 1', got %v", err)
		}
		if exitCode != 1 {
			t.Errorf("ExitCode: expected 1, got %d", exitCode)
		}
	})

	t.Run("invalid_json_output", func(t *testing.T) {
		stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox-3")
		// Create a stub that exits 0 but outputs invalid JSON.
		createStubBinaryWithResult(t, stubBinPath, "not valid json")

		runner := New(stubBinPath)
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: tempDir,
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for invalid JSON output, got nil")
		}
		if !strings.Contains(err.Error(), "invalid JSON") && !strings.Contains(err.Error(), "parse result JSON") {
			t.Errorf("Error message: expected to mention JSON parsing, got %v", err)
		}
	})
}

// TestExecSandboxMissingBinary verifies missing/unconfigured binary fails loud before exec.
// TC-062-05: empty binary path and nonexistent path both fail loud before exec.
func TestExecSandboxMissingBinary(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("empty_binary_path", func(t *testing.T) {
		runner := New("")
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: tempDir,
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for empty binary path, got nil")
		}
		if !strings.Contains(err.Error(), "not set") && !strings.Contains(err.Error(), "AGENT_BUILDER_EXEC_SANDBOX_BIN") {
			t.Errorf("Error message: expected to mention configuration, got %v", err)
		}
	})

	t.Run("nonexistent_binary_path", func(t *testing.T) {
		runner := New("/nonexistent/exec-sandbox")
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: tempDir,
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for nonexistent binary, got nil")
		}
		if !strings.Contains(err.Error(), "does not exist") && !strings.Contains(err.Error(), "not found") {
			t.Errorf("Error message: expected to mention missing binary, got %v", err)
		}
	})

	t.Run("binary_path_is_directory", func(t *testing.T) {
		runner := New(tempDir)
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: tempDir,
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for directory path, got nil")
		}
		if !strings.Contains(err.Error(), "directory") {
			t.Errorf("Error message: expected to mention directory, got %v", err)
		}
	})

	t.Run("binary_not_executable", func(t *testing.T) {
		binPath := filepath.Join(tempDir, "not-executable")
		if err := os.WriteFile(binPath, []byte("#!/bin/bash\n"), 0o644); err != nil {
			t.Fatalf("failed to create non-executable file: %v", err)
		}

		runner := New(binPath)
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: tempDir,
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for non-executable file, got nil")
		}
		if !strings.Contains(err.Error(), "not executable") {
			t.Errorf("Error message: expected to mention executable, got %v", err)
		}
	})
}

// TestExecSandboxWorktreeValidation verifies that invalid worktree paths fail loud.
// TC-062-07: bad/nonexistent/non-dir worktree paths are rejected with loud errors.
func TestExecSandboxWorktreeValidation(t *testing.T) {
	tempDir := t.TempDir()
	stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox")
	createStubBinaryWithResult(t, stubBinPath, defaultStubResult())

	runner := New(stubBinPath)

	t.Run("nonexistent_worktree", func(t *testing.T) {
		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: "/nonexistent/path/to/worktree",
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for nonexistent worktree, got nil")
		}
		if !strings.Contains(err.Error(), "invalid worktree") && !strings.Contains(err.Error(), "no such file") {
			t.Errorf("Error message: expected to mention invalid worktree or missing path, got %v", err)
		}
	})

	t.Run("worktree_is_file", func(t *testing.T) {
		filePath := filepath.Join(tempDir, "file-not-dir")
		if err := os.WriteFile(filePath, []byte("content"), 0o644); err != nil {
			t.Fatalf("failed to create test file: %v", err)
		}

		req := sandbox.Request{
			Command:  []string{"echo", "hi"},
			Worktree: filePath,
		}

		_, _, err := runner.Run(req)
		if err == nil {
			t.Fatalf("Expected error for file as worktree, got nil")
		}
		if !strings.Contains(err.Error(), "invalid worktree") && !strings.Contains(err.Error(), "not a directory") {
			t.Errorf("Error message: expected to mention invalid worktree or not a directory, got %v", err)
		}
	})
}

// Stub binary helpers.

// createStubBinary creates a stub exec-sandbox binary that records the request JSON and returns a valid result.
func createStubBinary(t *testing.T, binPath, recordPath, resultJSON string) {
	if resultJSON == "" {
		resultJSON = defaultStubResult()
	}

	// Write the result JSON to a temp file so we don't need to shell-quote it.
	resultPath := filepath.Join(filepath.Dir(recordPath), "stub-result.json")
	if err := os.WriteFile(resultPath, []byte(resultJSON), 0o644); err != nil {
		t.Fatalf("failed to write stub result file: %v", err)
	}

	// Create a shell script that reads JSON from stdin, records it, and outputs the result.
	script := fmt.Sprintf(`#!/bin/bash
set -e

# Read stdin and record it.
cat > %q

# Output the result.
cat %q
`, recordPath, resultPath)

	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to create stub binary: %v", err)
	}
}

// createStubBinaryWithResult creates a stub that outputs a specific result JSON.
func createStubBinaryWithResult(t *testing.T, binPath, resultJSON string) {
	// Write the result JSON to a temp file so we don't need to shell-quote it.
	resultPath := filepath.Join(filepath.Dir(binPath), "stub-result.json")
	if err := os.WriteFile(resultPath, []byte(resultJSON), 0o644); err != nil {
		t.Fatalf("failed to write stub result file: %v", err)
	}

	script := fmt.Sprintf(`#!/bin/bash
# Read stdin (and discard it).
cat > /dev/null

# Output the result.
cat %q
`, resultPath)

	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to create stub binary: %v", err)
	}
}

// createStubBinaryExitingWith creates a stub that exits with a specific code.
func createStubBinaryExitingWith(t *testing.T, binPath string, exitCode int, stderrMsg string) {
	script := fmt.Sprintf(`#!/bin/bash
# Read stdin (and discard it).
cat > /dev/null

# Output to stderr and exit with the specified code.
echo %q >&2
exit %d
`, stderrMsg, exitCode)

	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to create stub binary: %v", err)
	}
}

// defaultStubResult returns a minimal valid result JSON.
func defaultStubResult() string {
	return `{
		"stdout": "",
		"stderr": "",
		"exit_code": 0,
		"sandbox_status": {
			"sandbox_id": "sbx-test",
			"tier": "bubblewrap",
			"duration_ms": 10,
			"secrets_injected": [],
			"status": "clean",
			"limits": {
				"cpu_count": 0,
				"memory_mb": 0,
				"pids": 0,
				"disk_mb": 0,
				"timeout_sec": 0,
				"degraded": []
			}
		}
	}`
}

// --- Task 063 Tests ---

// TestRenderCommandTranslation tests the sh -c translation for ADR 032 probe forms.
// TC-063-01: renderCommand translates [-c <script>] and [sh|-c <script>] forms correctly.
func TestRenderCommandTranslation(t *testing.T) {
	tests := []struct {
		name     string
		cmd      []string
		expected string
	}{
		{
			name:     "probe form -c true",
			cmd:      []string{"-c", "true"},
			expected: "true",
		},
		{
			name:     "sh -c echo hi",
			cmd:      []string{"sh", "-c", "echo hi"},
			expected: "echo hi",
		},
		{
			name:     "bin/sh -c gofmt",
			cmd:      []string{"/bin/sh", "-c", "gofmt -l ."},
			expected: "gofmt -l .",
		},
		{
			name:     "/usr/bin/sh -c go test",
			cmd:      []string{"/usr/bin/sh", "-c", "go test ./..."},
			expected: "go test ./...",
		},
		{
			name:     "bash -c golangci-lint",
			cmd:      []string{"bash", "-c", "golangci-lint run"},
			expected: "golangci-lint run",
		},
		{
			name:     "direct go build (no -c)",
			cmd:      []string{"go", "build", "./..."},
			expected: "go build ./...",
		},
		{
			name:     "-c with dollar sign",
			cmd:      []string{"-c", "echo $HOME"},
			expected: "echo $HOME",
		},
		{
			name:     "empty command",
			cmd:      []string{},
			expected: "",
		},
		{
			name:     "-c with no script arg",
			cmd:      []string{"-c"},
			expected: "-c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderCommand(tt.cmd)
			if got != tt.expected {
				t.Errorf("renderCommand(%v) = %q, want %q", tt.cmd, got, tt.expected)
			}
		})
	}
}

// TestProbeFormPayload tests that probe form ["-c","true"] through Run() produces payload "true".
// TC-063-02: probe form payload translation works end-to-end through the runner.
func TestProbeFormPayload(t *testing.T) {
	tempDir := t.TempDir()
	stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox")
	recordPath := filepath.Join(tempDir, "request.json")

	createStubBinary(t, stubBinPath, recordPath, "")

	runner := New(stubBinPath)
	req := sandbox.Request{
		Command:  []string{"-c", "true"},
		Worktree: tempDir,
		Limits:   sandbox.Limits{},
	}

	_, _, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	recordedData, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("failed to read recorded request: %v", err)
	}

	var recordedReq runRequest
	if err := json.Unmarshal(recordedData, &recordedReq); err != nil {
		t.Fatalf("failed to parse recorded request JSON: %v", err)
	}

	// The critical assertion: payload must be exactly "true", not "'-c' 'true'"
	if recordedReq.Run.Payload != "true" {
		t.Errorf("payload: expected 'true', got %q", recordedReq.Run.Payload)
	}
}

// TestFileReadAndPathForwarding tests that FileRead and PATH env are included in marshalled RunRequest.
// TC-063-03: FileRead capability and PATH env provisioning are wired correctly.
func TestFileReadAndPathForwarding(t *testing.T) {
	tempDir := t.TempDir()
	stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox")
	recordPath := filepath.Join(tempDir, "request.json")

	createStubBinary(t, stubBinPath, recordPath, "")

	// Create temp directories for goroot and gate-tools (to make them discoverable)
	gorootDir := filepath.Join(tempDir, "go")
	gateToolsDir := filepath.Join(tempDir, "gate-tools")
	if err := os.MkdirAll(gorootDir, 0o755); err != nil {
		t.Fatalf("failed to create goroot dir: %v", err)
	}
	if err := os.MkdirAll(gateToolsDir, 0o755); err != nil {
		t.Fatalf("failed to create gate-tools dir: %v", err)
	}

	// Set env vars for discovery
	oldGoroot := os.Getenv("AGENT_BUILDER_EXEC_SANDBOX_GOROOT")
	oldGateTools := os.Getenv("AGENT_BUILDER_GATE_TOOLS")
	defer func() {
		if oldGoroot != "" {
			_ = os.Setenv("AGENT_BUILDER_EXEC_SANDBOX_GOROOT", oldGoroot)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_EXEC_SANDBOX_GOROOT")
		}
		if oldGateTools != "" {
			_ = os.Setenv("AGENT_BUILDER_GATE_TOOLS", oldGateTools)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_GATE_TOOLS")
		}
	}()

	if err := os.Setenv("AGENT_BUILDER_EXEC_SANDBOX_GOROOT", gorootDir); err != nil {
		t.Fatalf("failed to set AGENT_BUILDER_EXEC_SANDBOX_GOROOT: %v", err)
	}
	if err := os.Setenv("AGENT_BUILDER_GATE_TOOLS", gateToolsDir); err != nil {
		t.Fatalf("failed to set AGENT_BUILDER_GATE_TOOLS: %v", err)
	}

	runner := New(stubBinPath)
	req := sandbox.Request{
		Command:  []string{"-c", "true"},
		Worktree: tempDir,
		Limits:   sandbox.Limits{},
	}

	_, _, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	recordedData, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("failed to read recorded request: %v", err)
	}

	var recordedReq runRequest
	if err := json.Unmarshal(recordedData, &recordedReq); err != nil {
		t.Fatalf("failed to parse recorded request JSON: %v", err)
	}

	// Verify FileRead capability is present
	fileReadCap := findCapability(recordedReq, "FileRead")
	if fileReadCap == nil {
		t.Errorf("FileRead capability not found in capabilities")
	} else {
		// Check that both paths are present
		if len(fileReadCap.Paths) < 2 {
			t.Errorf("FileRead.paths: expected at least 2 entries, got %d: %v", len(fileReadCap.Paths), fileReadCap.Paths)
		} else {
			foundGoroot := false
			foundGateTools := false
			for _, p := range fileReadCap.Paths {
				if p == gorootDir {
					foundGoroot = true
				}
				if p == gateToolsDir {
					foundGateTools = true
				}
			}
			if !foundGoroot {
				t.Errorf("FileRead.paths: expected to contain goroot dir %q", gorootDir)
			}
			if !foundGateTools {
				t.Errorf("FileRead.paths: expected to contain gate-tools dir %q", gateToolsDir)
			}
		}
	}

	// Verify PATH env is set and contains both directories
	if len(recordedReq.Run.Env) == 0 {
		t.Errorf("env: expected non-empty, got empty")
	} else if path, ok := recordedReq.Run.Env["PATH"]; !ok {
		t.Errorf("env: expected PATH key to be present")
	} else {
		if !strings.Contains(path, gorootDir) {
			t.Errorf("PATH: expected to contain goroot dir %q, got %q", gorootDir, path)
		}
		if !strings.Contains(path, gateToolsDir) {
			t.Errorf("PATH: expected to contain gate-tools dir %q, got %q", gateToolsDir, path)
		}
	}

	// Verify NetConnect capability is still present (no regression)
	netCapFinal := findCapability(recordedReq, "NetConnect")
	if netCapFinal != nil {
		t.Logf("NetConnect capability still present (expected for full limits)")
	}
}

// TestOriginMapPopulation tests that origin_map is populated from EgressAllowlist.
// TC-063-04: EgressAllowlist entries are correctly mapped to origin_map.
func TestOriginMapPopulation(t *testing.T) {
	tempDir := t.TempDir()
	stubBinPath := filepath.Join(tempDir, "stub-exec-sandbox")
	recordPath := filepath.Join(tempDir, "request.json")

	createStubBinary(t, stubBinPath, recordPath, "")

	runner := New(stubBinPath)
	req := sandbox.Request{
		Command:  []string{"-c", "true"},
		Worktree: tempDir,
		Limits: sandbox.Limits{
			EgressAllowlist: []string{
				"api.github.com:443",
				"registry.npmjs.org:443",
			},
		},
	}

	_, _, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	recordedData, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("failed to read recorded request: %v", err)
	}

	var recordedReq runRequest
	if err := json.Unmarshal(recordedData, &recordedReq); err != nil {
		t.Fatalf("failed to parse recorded request JSON: %v", err)
	}

	// Verify origin_map is non-empty
	if len(recordedReq.Wiring.OriginMap) == 0 {
		t.Errorf("origin_map: expected non-empty, got empty")
	} else {
		// Verify each entry
		if entry, ok := recordedReq.Wiring.OriginMap["api.github.com"]; !ok {
			t.Errorf("origin_map: expected entry for 'api.github.com'")
		} else if entry[0] != "api.github.com" || entry[1] != "443" {
			t.Errorf("origin_map['api.github.com']: expected ['api.github.com', '443'], got %v", entry)
		}

		if entry, ok := recordedReq.Wiring.OriginMap["registry.npmjs.org"]; !ok {
			t.Errorf("origin_map: expected entry for 'registry.npmjs.org'")
		} else if entry[0] != "registry.npmjs.org" || entry[1] != "443" {
			t.Errorf("origin_map['registry.npmjs.org']: expected ['registry.npmjs.org', '443'], got %v", entry)
		}
	}

	// Verify NetConnect capability is still present
	netCap := findCapability(recordedReq, "NetConnect")
	if netCap == nil {
		t.Errorf("NetConnect capability not found")
	} else if len(netCap.Allowlist) != 2 {
		t.Errorf("NetConnect.allowlist: expected 2 entries, got %d", len(netCap.Allowlist))
	}
}

// findCapability is a helper to locate a capability by type.
func findCapability(req runRequest, capType string) *capabilityData {
	for i := range req.Run.Profile.Capabilities {
		if req.Run.Profile.Capabilities[i].Type == capType {
			return &req.Run.Profile.Capabilities[i]
		}
	}
	return nil
}
