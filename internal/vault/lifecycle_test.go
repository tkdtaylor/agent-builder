package vault_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/vault"
)

// TC-066-02: VaultDaemon starts, becomes reachable via Ping, and stops cleanly.
// Gated on AGENT_BUILDER_LIVE_VAULT=1 (requires the real vault binary).
func TestVaultDaemonLifecycle(t *testing.T) {
	binPath, _ := requireLiveVault(t)
	socketPath := filepath.Join(t.TempDir(), "vault.sock")
	d := &vault.Daemon{BinPath: binPath, SocketPath: socketPath}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start err = %v", err)
	}

	client := vault.NewClient(socketPath)
	deadline := time.Now().Add(3 * time.Second)
	var pingErr error
	for time.Now().Before(deadline) {
		if pingErr = client.Ping(); pingErr == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pingErr != nil {
		t.Fatalf("daemon not reachable within 3s: %v", pingErr)
	}

	// Second Start on a running daemon errors.
	if err := d.Start(ctx); err == nil {
		t.Error("second Start on running daemon: expected error, got nil")
	}

	if err := d.Stop(); err != nil {
		t.Fatalf("Stop err = %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Errorf("socket file still present after Stop: stat err = %v", err)
	}
}

// TestVaultDaemonStartFailsLoud covers the edge cases that need no real binary:
// missing binary, non-executable path, and missing master key.
func TestVaultDaemonStartFailsLoud(t *testing.T) {
	// A valid master key is set so these tests isolate the binary-path failures
	// (the missing-key case sets its own empty env).
	t.Run("missing binary path", func(t *testing.T) {
		setValidMasterKey(t)
		d := &vault.Daemon{BinPath: filepath.Join(t.TempDir(), "does-not-exist"), SocketPath: filepath.Join(t.TempDir(), "v.sock")}
		err := d.Start(context.Background())
		if err == nil {
			t.Fatal("expected error for missing binary, got nil")
		}
	})

	t.Run("non-executable binary", func(t *testing.T) {
		setValidMasterKey(t)
		dir := t.TempDir()
		notExec := filepath.Join(dir, "vault")
		if err := os.WriteFile(notExec, []byte("not a binary"), 0o644); err != nil {
			t.Fatal(err)
		}
		d := &vault.Daemon{BinPath: notExec, SocketPath: filepath.Join(dir, "v.sock")}
		if err := d.Start(context.Background()); err == nil {
			t.Fatal("expected error for non-executable binary, got nil")
		}
	})

	t.Run("missing master key", func(t *testing.T) {
		// Make an executable stub so the binary checks pass and we reach the key check.
		dir := t.TempDir()
		stub := filepath.Join(dir, "vault")
		if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(vault.EnvMasterKey, "")
		t.Setenv(vault.EnvMasterKeyFile, "")
		d := &vault.Daemon{BinPath: stub, SocketPath: filepath.Join(dir, "v.sock")}
		if err := d.Start(context.Background()); err == nil {
			t.Fatal("expected error for missing master key, got nil")
		}
	})

	t.Run("master key wrong length", func(t *testing.T) {
		dir := t.TempDir()
		stub := filepath.Join(dir, "vault")
		if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv(vault.EnvMasterKeyFile, "")
		t.Setenv(vault.EnvMasterKey, "abcd") // 2 bytes, not 32
		d := &vault.Daemon{BinPath: stub, SocketPath: filepath.Join(dir, "v.sock")}
		if err := d.Start(context.Background()); err == nil {
			t.Fatal("expected error for short master key, got nil")
		}
	})
}

func setValidMasterKey(t *testing.T) {
	t.Helper()
	t.Setenv(vault.EnvMasterKeyFile, "")
	t.Setenv(vault.EnvMasterKey, repeatHex())
}

func repeatHex() string {
	// 32 bytes hex = 64 chars.
	s := ""
	for i := 0; i < 32; i++ {
		s += "ab"
	}
	return s
}
