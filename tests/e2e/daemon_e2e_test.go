package e2e_test

// TC-174-06 (L5): a real `agent-builder daemon` subprocess catches SIGTERM via
// signal.NotifyContext, shuts down gracefully, and removes its lock file. The
// daemon runs the Telegram inbound channel against a fake idle getUpdates server
// (the adapter's Next is context-aware, so SIGTERM → ctx-cancel → clean exit).

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/envelope"
)

func TestDaemonGracefulShutdown(t *testing.T) {
	binary := buildAgentBuilder(t)

	// Fake idle Telegram getUpdates server: always returns an empty result, so the
	// adapter re-polls (idle) until its context is cancelled.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": []any{}})
	}))
	defer srv.Close()

	dir := t.TempDir()

	// Worker signing key file (hex of a 64-byte Ed25519 private key), SEC-003 check.
	_, workerPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("gen worker key: %v", err)
	}
	workerKeyPath := filepath.Join(dir, "worker.key")
	if err := os.WriteFile(workerKeyPath, []byte(hex.EncodeToString(workerPriv)), 0o600); err != nil {
		t.Fatalf("write worker key: %v", err)
	}

	// Telegram key material (valid lengths; no messages arrive so they are only
	// length-validated at assembly).
	opEdPub, _, _ := ed25519.GenerateKey(rand.Reader)  // operator signing pub (32)
	_, orchEdPriv, _ := ed25519.GenerateKey(rand.Reader) // orchestrator ed priv (64)
	opXPub, _, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("gen op x25519: %v", err)
	}
	_, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("gen orch x25519: %v", err)
	}

	fixture := newPublicationFixture(t, publicationFixtureConfig{})
	lockPath := filepath.Join(dir, "daemon.lock")

	env := fixture.env()
	env["AGENT_BUILDER_WORKER_SIGNING_KEY"] = workerKeyPath
	env["AGENT_BUILDER_DAEMON_LOCK"] = lockPath
	env["AGENT_BUILDER_INBOUND"] = "telegram"
	env["AGENT_BUILDER_TELEGRAM_BOT_TOKEN"] = "test-token"
	env["AGENT_BUILDER_TELEGRAM_BASE_URL"] = srv.URL
	env["AGENT_BUILDER_TELEGRAM_CHAT_ID"] = "12345"
	env["AGENT_BUILDER_TELEGRAM_SIGNING_KEY"] = hex.EncodeToString(opEdPub)
	env["AGENT_BUILDER_TELEGRAM_X25519_PUB"] = hex.EncodeToString(opXPub[:])
	env["AGENT_BUILDER_TELEGRAM_ORCH_PRIV"] = hex.EncodeToString(orchXPriv[:])
	env["AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV"] = hex.EncodeToString(orchEdPriv)
	env["AGENT_BUILDER_TELEGRAM_OP_X25519_PUB"] = hex.EncodeToString(opXPub[:])
	env["AGENT_BUILDER_TELEGRAM_POLL_BACKOFF"] = "50ms"

	cmd := exec.Command(binary, "daemon")
	cmd.Env = filteredEnv()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	// Wait until the daemon has acquired its lock (it is now in the steady-state loop).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(lockPath); statErr == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, statErr := os.Stat(lockPath); statErr != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("daemon never acquired lock %q; output=%q", lockPath, out.String())
	}

	// Send SIGTERM; the daemon must catch it and shut down gracefully.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal SIGTERM: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case werr := <-done:
		if werr != nil {
			t.Fatalf("daemon exited non-zero after SIGTERM: %v; output=%q", werr, out.String())
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("daemon did not exit within 5s of SIGTERM; output=%q", out.String())
	}

	if !strings.Contains(out.String(), "graceful shutdown") {
		t.Errorf("daemon output %q, want a graceful-shutdown message", out.String())
	}
	if _, statErr := os.Stat(lockPath); !os.IsNotExist(statErr) {
		t.Errorf("lock file %q not removed after graceful shutdown (stat err=%v)", lockPath, statErr)
	}
}
