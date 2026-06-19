package vault

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Env var names for the vault master key. At least one must be set when vault is
// enabled. VAULT_MASTER_KEY_FILE takes precedence over VAULT_MASTER_KEY.
const (
	EnvMasterKey     = "VAULT_MASTER_KEY"
	EnvMasterKeyFile = "VAULT_MASTER_KEY_FILE"
)

// masterKeyBytes is the required decoded length of the master key (32 bytes).
const masterKeyBytes = 32

// pingTimeout bounds how long Start waits for the daemon to become reachable.
const pingTimeout = 5 * time.Second

// pingInterval is the poll interval while waiting for the daemon to come up.
const pingInterval = 100 * time.Millisecond

var (
	// ErrAlreadyStarted means Start was called on a daemon that is already running.
	ErrAlreadyStarted = errors.New("vault daemon: already started")

	// ErrMissingMasterKey means neither VAULT_MASTER_KEY nor VAULT_MASTER_KEY_FILE
	// resolved to a usable 32-byte hex master key when vault was enabled.
	ErrMissingMasterKey = errors.New("vault daemon: no master key (set VAULT_MASTER_KEY hex or VAULT_MASTER_KEY_FILE); never auto-generated")
)

// Daemon manages a vault daemon subprocess. It execs `vault serve --socket
// <path>` and waits for the socket to answer Ping, then can be stopped cleanly.
//
// BinPath, SocketPath, and StorePath are set by the caller before Start. The
// zero value is not usable.
type Daemon struct {
	BinPath    string
	SocketPath string
	StorePath  string // optional; "" → in-memory store only

	cmd *exec.Cmd
}

// Start execs the vault daemon and blocks until Ping succeeds (up to 5 seconds)
// or the context is cancelled. It fails loud before exec when the binary is
// missing or not executable, and when no master key is available.
//
// The master key is sourced from VAULT_MASTER_KEY_FILE (preferred) or
// VAULT_MASTER_KEY; absence is a hard error — a key is never auto-generated,
// because an ephemeral in-memory key silently loses secrets across restarts.
func (d *Daemon) Start(ctx context.Context) error {
	if d.cmd != nil {
		return ErrAlreadyStarted
	}

	binPath := strings.TrimSpace(d.BinPath)
	if binPath == "" {
		return errors.New("vault daemon: BinPath is empty (set AGENT_BUILDER_VAULT_BIN)")
	}
	info, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("vault daemon: binary %q does not exist or is not readable: %w", binPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("vault daemon: %q is a directory, not an executable", binPath)
	}
	if (info.Mode() & 0o111) == 0 {
		return fmt.Errorf("vault daemon: %q is not executable", binPath)
	}

	socketPath := strings.TrimSpace(d.SocketPath)
	if socketPath == "" {
		return errors.New("vault daemon: SocketPath is empty")
	}

	masterKey, err := resolveMasterKey(os.Getenv)
	if err != nil {
		return err
	}

	args := []string{"serve", "--socket", socketPath}
	if store := strings.TrimSpace(d.StorePath); store != "" {
		args = append(args, "--store-path", store)
	}

	cmd := exec.CommandContext(ctx, binPath, args...)
	// Pass the master key to the daemon via its env. The key is never logged.
	cmd.Env = append(os.Environ(), EnvMasterKey+"="+masterKey)
	cmd.Stdout = os.Stderr // daemon diagnostics flow to our stderr, never stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("vault daemon: exec %q: %w", binPath, err)
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
func (d *Daemon) waitForReady(ctx context.Context) error {
	client := NewClient(d.SocketPath)
	deadline := time.Now().Add(pingTimeout)
	for {
		if err := client.Ping(); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("vault daemon: not reachable on %s within %s", d.SocketPath, pingTimeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("vault daemon: start cancelled: %w", ctx.Err())
		case <-time.After(pingInterval):
		}
	}
}

// Stop kills the daemon subprocess and removes the socket file. It is safe to
// call on a daemon that never started or already stopped.
func (d *Daemon) Stop() error {
	var firstErr error
	if d.cmd != nil && d.cmd.Process != nil {
		if err := d.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			firstErr = fmt.Errorf("vault daemon: kill: %w", err)
		}
		_, _ = d.cmd.Process.Wait()
		d.cmd = nil
	}
	if socketPath := strings.TrimSpace(d.SocketPath); socketPath != "" {
		if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			if firstErr == nil {
				firstErr = fmt.Errorf("vault daemon: remove socket: %w", err)
			}
		}
	}
	return firstErr
}

// resolveMasterKey returns the hex-encoded 32-byte master key from
// VAULT_MASTER_KEY_FILE (preferred) or VAULT_MASTER_KEY. It validates the key is
// 32 decoded bytes. It never returns the key in an error message.
func resolveMasterKey(getenv func(string) string) (string, error) {
	var raw string
	if file := strings.TrimSpace(getenv(EnvMasterKeyFile)); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("vault daemon: read %s %q: %w", EnvMasterKeyFile, file, err)
		}
		raw = strings.TrimSpace(string(data))
	} else if env := strings.TrimSpace(getenv(EnvMasterKey)); env != "" {
		raw = env
	}
	if raw == "" {
		return "", ErrMissingMasterKey
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		// Do NOT echo the key material in the error.
		return "", fmt.Errorf("vault daemon: master key is not valid hex")
	}
	if len(decoded) != masterKeyBytes {
		return "", fmt.Errorf("vault daemon: master key must decode to %d bytes, got %d", masterKeyBytes, len(decoded))
	}
	return raw, nil
}
