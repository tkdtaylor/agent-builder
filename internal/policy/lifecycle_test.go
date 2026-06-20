package policy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TC-072-01 (live): PolicyDaemon starts against the real policy-engine binary,
// becomes reachable via Ping, and stops cleanly. Gated on AGENT_BUILDER_LIVE_POLICY=1
// because it requires the real policy-engine binary.
func TestPolicyDaemonLifecycle(t *testing.T) {
	if os.Getenv("AGENT_BUILDER_LIVE_POLICY") != "1" {
		t.Skip("set AGENT_BUILDER_LIVE_POLICY=1 and AGENT_BUILDER_POLICY_BIN to run")
	}
	bin := os.Getenv("AGENT_BUILDER_POLICY_BIN")
	if bin == "" {
		t.Skip("AGENT_BUILDER_POLICY_BIN not set")
	}

	socket := filepath.Join(t.TempDir(), "policy.sock")
	d := &PolicyDaemon{BinPath: bin, SocketPath: socket, Allow: []string{"api.github.com"}}
	if err := d.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = d.Stop() }()

	if err := NewClient(socket).Ping(); err != nil {
		t.Fatalf("Ping() after Start error = %v", err)
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Fatalf("socket not removed after Stop: stat err = %v", err)
	}
}

// TC-072-01 edge: empty BinPath fails loud (always runs, no live binary needed).
func TestPolicyDaemonStartEmptyBinPath(t *testing.T) {
	d := &PolicyDaemon{BinPath: "", SocketPath: filepath.Join(t.TempDir(), "policy.sock")}
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("Start() with empty BinPath returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "BinPath is empty") {
		t.Fatalf("error = %v, want it to name empty BinPath", err)
	}
}

// TC-072-01 edge: a missing binary path fails loud naming the binary.
func TestPolicyDaemonStartMissingBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	d := &PolicyDaemon{BinPath: missing, SocketPath: filepath.Join(t.TempDir(), "policy.sock")}
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("Start() with missing binary returned nil error, want error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error = %v, want it to name the missing binary %q", err, missing)
	}
}

// TC-072-01 edge: a non-executable file fails loud.
func TestPolicyDaemonStartNonExecutable(t *testing.T) {
	dir := t.TempDir()
	notExec := filepath.Join(dir, "policy-engine")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write non-exec file: %v", err)
	}
	d := &PolicyDaemon{BinPath: notExec, SocketPath: filepath.Join(dir, "policy.sock")}
	err := d.Start(context.Background())
	if err == nil {
		t.Fatal("Start() with non-executable binary returned nil error, want error")
	}
	if !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("error = %v, want 'not executable'", err)
	}
}

// TC-072-01 edge: ping timeout when the binary starts but never binds the socket.
// Uses a tiny shell binary that sleeps without serving — fail-loud on timeout.
func TestPolicyDaemonStartPingTimeout(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "policy-engine")
	// A binary that ignores its args and sleeps; it never binds the socket, so
	// Ping never succeeds and Start must time out and fail loud.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatalf("write fake: %v", err)
	}
	d := &PolicyDaemon{BinPath: fake, SocketPath: filepath.Join(dir, "policy.sock")}

	// Bound the test independently of the 5s production timeout via context.
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	start := time.Now()
	err := d.Start(ctx)
	if err == nil {
		_ = d.Stop()
		t.Fatal("Start() with non-serving binary returned nil error, want timeout error")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Fatalf("error = %v, want 'not reachable' timeout", err)
	}
	if elapsed := time.Since(start); elapsed > 6*time.Second {
		t.Fatalf("Start took %s, want it bounded near the 5s ping timeout", elapsed)
	}
}

// TC-072-01 edge: a second Start on a running daemon returns ErrAlreadyStarted.
// White-box: a non-nil d.cmd is the "already started" signal the guard checks.
func TestPolicyDaemonSecondStartRejected(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper: %v", err)
	}
	d := &PolicyDaemon{
		BinPath:    "/bin/sh",
		SocketPath: filepath.Join(t.TempDir(), "policy.sock"),
		cmd:        cmd,
	}
	defer func() { _ = d.Stop() }()

	if err := d.Start(context.Background()); err != ErrAlreadyStarted {
		t.Fatalf("second Start error = %v, want ErrAlreadyStarted", err)
	}
}
