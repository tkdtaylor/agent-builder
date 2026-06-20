package policy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// pingTimeout bounds how long Start waits for the daemon to become reachable.
const pingTimeout = 5 * time.Second

// pingInterval is the poll interval while waiting for the daemon to come up.
const pingInterval = 100 * time.Millisecond

// ErrAlreadyStarted means Start was called on a daemon that is already running.
var ErrAlreadyStarted = errors.New("policy daemon: already started")

// PolicyDaemon manages a policy-engine daemon subprocess. It execs
// `policy-engine serve --socket <path> --allow <csv>` and waits for the socket
// to answer Ping, then can be stopped cleanly.
//
// BinPath, SocketPath, and Allow are set by the caller before Start. The zero
// value is not usable. This mirrors internal/vault.Daemon exactly (ADR 038).
type PolicyDaemon struct {
	BinPath    string
	SocketPath string
	Allow      []string // fed from sandbox.Limits.EgressAllowlist; passed as --allow CSV

	cmd *exec.Cmd
}

// Start execs the policy-engine daemon and blocks until Ping succeeds (up to 5
// seconds) or the context is cancelled. It fails loud before exec when the
// binary is empty, missing, a directory, or not executable, and when the socket
// path is empty.
//
// The daemon is launched with `policy-engine serve --socket <path>
// --allow <comma-CSV-of-hosts>`. The --allow value is built from the Allow
// slice (the caller feeds it from sandbox.Limits.EgressAllowlist so the policy
// engine's allowlist stays synchronized with the sandbox egress allowlist).
func (d *PolicyDaemon) Start(ctx context.Context) error {
	if d.cmd != nil {
		return ErrAlreadyStarted
	}

	binPath := strings.TrimSpace(d.BinPath)
	if binPath == "" {
		return errors.New("policy daemon: BinPath is empty (set AGENT_BUILDER_POLICY_BIN)")
	}
	info, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("policy daemon: binary %q does not exist or is not readable: %w", binPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("policy daemon: %q is a directory, not an executable", binPath)
	}
	if (info.Mode() & 0o111) == 0 {
		return fmt.Errorf("policy daemon: %q is not executable", binPath)
	}

	socketPath := strings.TrimSpace(d.SocketPath)
	if socketPath == "" {
		return errors.New("policy daemon: SocketPath is empty")
	}

	args := []string{"serve", "--socket", socketPath, "--allow", strings.Join(d.Allow, ",")}

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Stdout = os.Stderr // daemon diagnostics flow to our stderr, never stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("policy daemon: exec %q: %w", binPath, err)
	}
	d.cmd = cmd

	if err := d.waitForReady(ctx); err != nil {
		// Best-effort cleanup of the half-started daemon.
		_ = d.Stop()
		return err
	}
	return nil
}

// waitForReady polls Ping until it succeeds or pingTimeout elapses.
func (d *PolicyDaemon) waitForReady(ctx context.Context) error {
	client := NewClient(d.SocketPath)
	deadline := time.Now().Add(pingTimeout)
	for {
		if err := client.Ping(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("policy daemon: not reachable on %s within %s", d.SocketPath, pingTimeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("policy daemon: start cancelled: %w", ctx.Err())
		case <-time.After(pingInterval):
		}
	}
}

// Stop kills the daemon subprocess and removes the socket file. It is safe to
// call on a daemon that never started or already stopped.
func (d *PolicyDaemon) Stop() error {
	var firstErr error
	if d.cmd != nil && d.cmd.Process != nil {
		if err := d.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			firstErr = fmt.Errorf("policy daemon: kill: %w", err)
		}
		_, _ = d.cmd.Process.Wait()
		d.cmd = nil
	}
	if socketPath := strings.TrimSpace(d.SocketPath); socketPath != "" {
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = fmt.Errorf("policy daemon: remove socket: %w", err)
			}
		}
	}
	return firstErr
}
