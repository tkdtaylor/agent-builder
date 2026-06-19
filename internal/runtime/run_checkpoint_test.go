package runtime

// Tests for checkpoint signer config wiring (task 068).
//
// TC-068-03: ConfigFromEnv reads four AGENT_BUILDER_AUDIT_CHECKPOINT_* env vars.
// TC-068-04: Fail-fast pre-dispatch for missing key file, unwritable output dir,
//            or unresolvable binary.
// TC-068-08: make check green + docs/spec/configuration.md updated with all four
//            AGENT_BUILDER_AUDIT_CHECKPOINT_* env vars (verified by make check and
//            spec file diff in same commit).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseCheckpointEnv returns a minimal valid getenv that has all required fields
// but none of the checkpoint vars set (checkpoint disabled by default).
func baseCheckpointEnv(overrides map[string]string) func(string) string {
	base := map[string]string{
		"AGENT_BUILDER_TASK_ROOT":     "/tmp/tasks",
		"AGENT_BUILDER_WORKTREE":      "/tmp/work",
		"CLAUDE_CODE_OAUTH_TOKEN":     "oauth-token",
		"AGENT_BUILDER_RUN_TIMEOUT":   "5m",
		"AGENT_BUILDER_MAX_ATTEMPTS":  "2",
		"AGENT_BUILDER_PUBLISH_REMOTE": "origin",
	}
	for k, v := range overrides {
		base[k] = v
	}
	return func(key string) string {
		return base[key]
	}
}

// TC-068-03: None of the checkpoint vars set → all four fields empty, no error.
func TestConfigFromEnvCheckpointDisabledByDefault(t *testing.T) {
	config, err := ConfigFromEnv(baseCheckpointEnv(nil))
	if err != nil {
		t.Fatalf("TC-068-03: ConfigFromEnv error = %v, want nil", err)
	}
	if config.AuditCheckpointKey != "" {
		t.Errorf("TC-068-03: AuditCheckpointKey = %q, want empty", config.AuditCheckpointKey)
	}
	if config.AuditCheckpointLogID != "" {
		t.Errorf("TC-068-03: AuditCheckpointLogID = %q, want empty", config.AuditCheckpointLogID)
	}
	if config.AuditCheckpointOut != "" {
		t.Errorf("TC-068-03: AuditCheckpointOut = %q, want empty", config.AuditCheckpointOut)
	}
	if config.AuditCheckpointPublicKey != "" {
		t.Errorf("TC-068-03: AuditCheckpointPublicKey = %q, want empty", config.AuditCheckpointPublicKey)
	}
}

// TC-068-03: Only KEY set → KEY field populated, others empty, no error.
func TestConfigFromEnvCheckpointKeyOnly(t *testing.T) {
	config, err := ConfigFromEnv(baseCheckpointEnv(map[string]string{
		EnvAuditCheckpointKey: "/tmp/key.pem",
	}))
	if err != nil {
		t.Fatalf("TC-068-03: ConfigFromEnv error = %v, want nil", err)
	}
	if config.AuditCheckpointKey != "/tmp/key.pem" {
		t.Errorf("TC-068-03: AuditCheckpointKey = %q, want %q", config.AuditCheckpointKey, "/tmp/key.pem")
	}
	if config.AuditCheckpointLogID != "" {
		t.Errorf("TC-068-03: AuditCheckpointLogID = %q, want empty", config.AuditCheckpointLogID)
	}
	if config.AuditCheckpointOut != "" {
		t.Errorf("TC-068-03: AuditCheckpointOut = %q, want empty", config.AuditCheckpointOut)
	}
	if config.AuditCheckpointPublicKey != "" {
		t.Errorf("TC-068-03: AuditCheckpointPublicKey = %q, want empty", config.AuditCheckpointPublicKey)
	}
}

// TC-068-03: All four checkpoint vars set → all four fields reflect the env values.
func TestConfigFromEnvCheckpointAllFourSet(t *testing.T) {
	config, err := ConfigFromEnv(baseCheckpointEnv(map[string]string{
		EnvAuditCheckpointKey:       "/tmp/key.pem",
		EnvAuditCheckpointLogID:     "prod-001",
		EnvAuditCheckpointOut:       "/tmp/checkpoint.json",
		EnvAuditCheckpointPublicKey: "/tmp/pub.pem",
	}))
	if err != nil {
		t.Fatalf("TC-068-03: ConfigFromEnv error = %v, want nil", err)
	}
	if config.AuditCheckpointKey != "/tmp/key.pem" {
		t.Errorf("TC-068-03: AuditCheckpointKey = %q, want %q", config.AuditCheckpointKey, "/tmp/key.pem")
	}
	if config.AuditCheckpointLogID != "prod-001" {
		t.Errorf("TC-068-03: AuditCheckpointLogID = %q, want %q", config.AuditCheckpointLogID, "prod-001")
	}
	if config.AuditCheckpointOut != "/tmp/checkpoint.json" {
		t.Errorf("TC-068-03: AuditCheckpointOut = %q, want %q", config.AuditCheckpointOut, "/tmp/checkpoint.json")
	}
	if config.AuditCheckpointPublicKey != "/tmp/pub.pem" {
		t.Errorf("TC-068-03: AuditCheckpointPublicKey = %q, want %q", config.AuditCheckpointPublicKey, "/tmp/pub.pem")
	}
}

// TC-068-04: KEY set but file does not exist → fail-fast error naming the path and env var.
func TestResolveCheckpointConfigMissingKeyFile(t *testing.T) {
	config := Config{
		AuditCheckpointKey: "/nonexistent/does-not-exist.pem",
		AuditBin:           "", // will be resolved from PATH; not reached when key check fails first
	}
	err := resolveCheckpointConfig(config)
	if err == nil {
		t.Fatal("TC-068-04: resolveCheckpointConfig returned nil, want error for missing key file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/does-not-exist.pem") {
		t.Errorf("TC-068-04: error %q does not name the missing key path", err.Error())
	}
	if !strings.Contains(err.Error(), EnvAuditCheckpointKey) {
		t.Errorf("TC-068-04: error %q does not name the env var %s", err.Error(), EnvAuditCheckpointKey)
	}
}

// TC-068-04: KEY set, binary set to non-executable → fail-fast error naming the binary.
func TestNewCheckpointSignerNonExecutableBinary(t *testing.T) {
	// Create a real key file so that passes, then test binary resolution.
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("fake-pem-content"), 0o644); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	config := Config{
		AuditCheckpointKey: keyPath,
		AuditBin:           "/nonexistent/no-such-audit-trail",
	}
	_, err := newCheckpointSigner(config)
	if err == nil {
		t.Fatal("TC-068-04: newCheckpointSigner returned nil error for non-executable binary")
	}
	// Error must come from binary resolution (resolveAuditBin).
	if !strings.Contains(err.Error(), "audit-trail") && !strings.Contains(err.Error(), "no-such-audit-trail") {
		t.Errorf("TC-068-04: error %q does not name binary resolution failure", err.Error())
	}
}

// TC-068-04: KEY set, binary resolves (on PATH), OUT in a non-existent directory → fail-fast.
func TestResolveCheckpointConfigUnwritableOutDir(t *testing.T) {
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "key.pem")
	if err := os.WriteFile(keyPath, []byte("fake-pem-content"), 0o644); err != nil {
		t.Fatalf("setup: WriteFile: %v", err)
	}

	config := Config{
		AuditCheckpointKey: keyPath,
		AuditCheckpointOut: "/nonexistent-dir-068/checkpoint.json",
	}
	err := resolveCheckpointConfig(config)
	if err == nil {
		t.Fatal("TC-068-04: resolveCheckpointConfig returned nil for unwritable OUT parent dir")
	}
	if !strings.Contains(err.Error(), EnvAuditCheckpointOut) {
		t.Errorf("TC-068-04: error %q does not name the env var %s", err.Error(), EnvAuditCheckpointOut)
	}
}

// TC-068-04: No checkpoint vars set → newCheckpointSigner returns nil, nil (no error, disabled).
func TestNewCheckpointSignerDisabledWhenKeyEmpty(t *testing.T) {
	config := Config{
		AuditCheckpointKey: "", // not set
	}
	cs, err := newCheckpointSigner(config)
	if err != nil {
		t.Fatalf("TC-068-04: newCheckpointSigner with empty key returned error: %v", err)
	}
	if cs != nil {
		t.Fatalf("TC-068-04: newCheckpointSigner with empty key returned non-nil signer, want nil")
	}
}
