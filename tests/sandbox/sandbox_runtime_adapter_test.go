package sandbox_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
	"github.com/tkdtaylor/agent-builder/internal/sandbox/sandboxruntime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestSandboxRuntimeRunnerInvokesSRTAndCapturesOutput_TC001(t *testing.T) {
	worktree := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "srt.log")
	settingsCopy := filepath.Join(t.TempDir(), "settings.json")
	fakeSRT := writeFakeSRT(t, logPath, settingsCopy)

	runner := sandboxruntime.New(sandboxruntime.Config{CLIPath: fakeSRT})
	result, exitCode, err := runner.Run(sandbox.Request{
		Command:  []string{"sh", "-c", "printf hello; printf err >&2"},
		Worktree: worktree,
		Limits: sandbox.Limits{
			WallClockTimeout: 2 * time.Second,
			EgressAllowlist:  []string{"api.github.com:443"},
		},
	})
	if err != nil {
		t.Fatalf("TC-001: Run() error = %v, want nil", err)
	}
	if exitCode != 0 {
		t.Fatalf("TC-001: exit code = %d, want 0", exitCode)
	}
	if result.Stdout != "hello" {
		t.Fatalf("TC-001: stdout = %q, want hello", result.Stdout)
	}
	if result.Stderr != "err" {
		t.Fatalf("TC-001: stderr = %q, want err", result.Stderr)
	}
	if result.Duration <= 0 {
		t.Fatalf("TC-001: duration = %s, want positive duration", result.Duration)
	}

	log := readText(t, logPath)
	assertContains(t, log, "cwd="+worktree)
	assertContains(t, log, "argv=sh -c printf hello; printf err >&2")

	settings := readSettings(t, settingsCopy)
	if got := settings.Network.AllowedDomains; !reflect.DeepEqual(got, []string{"api.github.com"}) {
		t.Fatalf("TC-002: allowedDomains = %v, want [api.github.com]", got)
	}
	if !slices.Contains(settings.Filesystem.AllowWrite, worktree) {
		t.Fatalf("TC-001: allowWrite = %v, want worktree %s", settings.Filesystem.AllowWrite, worktree)
	}
}

func TestSandboxRuntimeRunnerSurfacesNonZeroExitCode_TC001(t *testing.T) {
	worktree := t.TempDir()
	fakeSRT := writeFakeSRT(t, filepath.Join(t.TempDir(), "srt.log"), filepath.Join(t.TempDir(), "settings.json"))

	runner := sandboxruntime.New(sandboxruntime.Config{CLIPath: fakeSRT})
	result, exitCode, err := runner.Run(sandbox.Request{
		Command:  []string{"sh", "-c", "printf failed >&2; exit 7"},
		Worktree: worktree,
	})
	if err != nil {
		t.Fatalf("TC-001: non-zero command returned error = %v, want nil backend error", err)
	}
	if exitCode != 7 {
		t.Fatalf("TC-001: exit code = %d, want 7", exitCode)
	}
	if result.Stderr != "failed" {
		t.Fatalf("TC-001: stderr = %q, want failed", result.Stderr)
	}
}

func TestSandboxRuntimeRunnerValidatesRequestsAndBackendErrors_TC001(t *testing.T) {
	runner := sandboxruntime.New(sandboxruntime.Config{CLIPath: "definitely-missing-srt"})

	_, _, err := runner.Run(sandbox.Request{Command: []string{" "}, Worktree: t.TempDir()})
	if !errors.Is(err, sandbox.ErrInvalidCommand) {
		t.Fatalf("TC-001: blank command error = %v, want ErrInvalidCommand", err)
	}

	_, _, err = runner.Run(sandbox.Request{Command: []string{"echo"}, Worktree: filepath.Join(t.TempDir(), "missing")})
	if !errors.Is(err, sandboxruntime.ErrInvalidWorktree) {
		t.Fatalf("TC-001: missing worktree error = %v, want ErrInvalidWorktree", err)
	}

	_, _, err = runner.Run(sandbox.Request{Command: []string{"echo", "hello"}, Worktree: t.TempDir()})
	if err == nil {
		t.Fatal("TC-001: missing srt binary returned nil error")
	}
	assertContains(t, err.Error(), "definitely-missing-srt")
}

func TestSandboxRuntimeRunnerHonorsTimeout_TC001(t *testing.T) {
	worktree := t.TempDir()
	fakeSRT := writeFakeSRT(t, filepath.Join(t.TempDir(), "srt.log"), filepath.Join(t.TempDir(), "settings.json"))

	runner := sandboxruntime.New(sandboxruntime.Config{CLIPath: fakeSRT})
	result, exitCode, err := runner.Run(sandbox.Request{
		Command:  []string{"sh", "-c", "sleep 1"},
		Worktree: worktree,
		Limits: sandbox.Limits{
			WallClockTimeout: time.Nanosecond,
		},
	})
	if err == nil {
		t.Fatal("TC-001: timeout returned nil error")
	}
	if exitCode != -1 {
		t.Fatalf("TC-001: timeout exit code = %d, want -1", exitCode)
	}
	if result.Duration <= 0 {
		t.Fatalf("TC-001: timeout duration = %s, want positive duration", result.Duration)
	}
}

func TestSandboxRuntimeRunnerGeneratesNetworkAllowlist_TC002_TC003(t *testing.T) {
	worktree := t.TempDir()
	settingsCopy := filepath.Join(t.TempDir(), "settings.json")
	fakeSRT := writeFakeSRT(t, filepath.Join(t.TempDir(), "srt.log"), settingsCopy)

	runner := sandboxruntime.New(sandboxruntime.Config{CLIPath: fakeSRT})
	_, _, err := runner.Run(sandbox.Request{
		Command:  []string{"true"},
		Worktree: worktree,
		Limits: sandbox.Limits{
			EgressAllowlist: []string{
				"API.GitHub.com:443",
				"proxy.golang.org:443",
				"api.github.com:443",
			},
		},
	})
	if err != nil {
		t.Fatalf("TC-002: Run() error = %v, want nil", err)
	}

	settings := readSettings(t, settingsCopy)
	if got := settings.Network.AllowedDomains; !reflect.DeepEqual(got, []string{"api.github.com", "proxy.golang.org"}) {
		t.Fatalf("TC-002: allowedDomains = %v, want normalized/deduped domains", got)
	}
	if slices.Contains(settings.Network.AllowedDomains, "example.com") {
		t.Fatalf("TC-003: non-allowlisted domain appeared in allowedDomains: %v", settings.Network.AllowedDomains)
	}
	if settings.Network.AllowLocalBinding {
		t.Fatal("TC-003: allowLocalBinding = true, want false")
	}
}

func TestSandboxRuntimeRunnerEmptyAllowlistMeansNoNetwork_TC002_TC003(t *testing.T) {
	worktree := t.TempDir()
	settingsCopy := filepath.Join(t.TempDir(), "settings.json")
	fakeSRT := writeFakeSRT(t, filepath.Join(t.TempDir(), "srt.log"), settingsCopy)

	runner := sandboxruntime.New(sandboxruntime.Config{CLIPath: fakeSRT})
	_, _, err := runner.Run(sandbox.Request{Command: []string{"true"}, Worktree: worktree})
	if err != nil {
		t.Fatalf("TC-003: Run() error = %v, want nil", err)
	}

	settings := readSettings(t, settingsCopy)
	if len(settings.Network.AllowedDomains) != 0 {
		t.Fatalf("TC-003: empty allowlist allowedDomains = %v, want empty", settings.Network.AllowedDomains)
	}
}

func TestSandboxRuntimeRunnerRejectsMalformedAllowlist_TC002(t *testing.T) {
	runner := sandboxruntime.New(sandboxruntime.Config{CLIPath: "unused"})
	for _, entry := range []string{"https://example.com:443", "example.com:https"} {
		_, _, err := runner.Run(sandbox.Request{
			Command:  []string{"true"},
			Worktree: t.TempDir(),
			Limits: sandbox.Limits{
				EgressAllowlist: []string{entry},
			},
		})
		if !errors.Is(err, sandboxruntime.ErrInvalidAllowlistEntry) {
			t.Fatalf("TC-002: malformed allowlist %q error = %v, want ErrInvalidAllowlistEntry", entry, err)
		}
	}
}

func TestSandboxRuntimeRunnerIsSwapCompatible_TC004(t *testing.T) {
	var runner sandbox.Runner = sandboxruntime.New(sandboxruntime.Config{CLIPath: "srt"})
	s := supervisor.New(supervisor.WithSandboxRunner(runner))
	if s == nil {
		t.Fatal("TC-004: New() returned nil supervisor")
	}

	imports := supervisorImports(t)
	if !slices.Contains(imports, "github.com/tkdtaylor/agent-builder/internal/sandbox") {
		t.Fatalf("TC-004: supervisor imports = %v, want internal/sandbox seam import", imports)
	}
	if slices.Contains(imports, "github.com/tkdtaylor/agent-builder/internal/sandbox/sandboxruntime") {
		t.Fatalf("TC-004: supervisor imports concrete sandbox-runtime backend: %v", imports)
	}
}

func TestSandboxRuntimeLiveHarness_TC002_TC003(t *testing.T) {
	if os.Getenv("AGENT_BUILDER_LIVE_SRT") != "1" {
		t.Skip("set AGENT_BUILDER_LIVE_SRT=1 to run live @anthropic-ai/sandbox-runtime allow/deny evidence")
	}
	for _, binary := range []string{"srt", "bwrap", "curl"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s unavailable; live sandbox-runtime evidence cannot run", binary)
		}
	}

	allowHost := os.Getenv("AGENT_BUILDER_LIVE_SRT_ALLOW_HOST")
	if allowHost == "" {
		allowHost = "api.github.com"
	}
	denyHost := os.Getenv("AGENT_BUILDER_LIVE_SRT_DENY_HOST")
	if denyHost == "" {
		denyHost = "example.com"
	}

	runner := sandboxruntime.New(sandboxruntime.Config{})
	allowed, allowedExit, err := runner.Run(sandbox.Request{
		Command:  []string{"curl", "-fsS", "--max-time", "10", "https://" + allowHost},
		Worktree: t.TempDir(),
		Limits: sandbox.Limits{
			EgressAllowlist: []string{allowHost + ":443"},
		},
	})
	if err != nil {
		t.Fatalf("TC-002 live allow: Run() error = %v", err)
	}
	if allowedExit != 0 {
		t.Fatalf("TC-002 live allow: exit = %d, stderr = %q", allowedExit, allowed.Stderr)
	}

	denied, deniedExit, err := runner.Run(sandbox.Request{
		Command:  []string{"curl", "-fsS", "--max-time", "10", "https://" + denyHost},
		Worktree: t.TempDir(),
		Limits: sandbox.Limits{
			EgressAllowlist: []string{allowHost + ":443"},
		},
	})
	if err != nil {
		t.Fatalf("TC-003 live deny: Run() error = %v", err)
	}
	if deniedExit == 0 {
		t.Fatalf("TC-003 live deny: blocked host exited 0; stdout = %q stderr = %q", denied.Stdout, denied.Stderr)
	}
}

type capturedSettings struct {
	Network struct {
		AllowedDomains    []string `json:"allowedDomains"`
		AllowLocalBinding bool     `json:"allowLocalBinding"`
	} `json:"network"`
	Filesystem struct {
		AllowWrite []string `json:"allowWrite"`
	} `json:"filesystem"`
}

func writeFakeSRT(t *testing.T, logPath, settingsCopy string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "srt")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" != "--settings" ]; then
    echo "missing --settings" >&2
    exit 98
fi
settings="$2"
shift 2
printf 'cwd=%%s\n' "$PWD" > %s
printf 'argv=%%s\n' "$*" >> %s
cp "$settings" %s
exec "$@"
`, shellQuote(logPath), shellQuote(logPath), shellQuote(settingsCopy))
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readSettings(t *testing.T, path string) capturedSettings {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings capturedSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings JSON: %v\n%s", err, data)
	}
	return settings
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
