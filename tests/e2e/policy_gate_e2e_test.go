package e2e_test

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
)

// TC-072-06 (covering TC-072-02/03 end-to-end): L5 fake-binary acceptance.
//
// A fake policy-engine binary serves the NDJSON decide protocol over a Unix
// socket. The real agent-builder binary starts it (proving the lifecycle wiring
// passes --socket/--allow), calls decide host-side BEFORE box.Create, and
// applies the response. No real policy-engine binary is required.
//
// tier_select and vault_injection_floor are observed through a fake exec-sandbox
// BLOCK binary (AGENT_BUILDER_EXEC_SANDBOX_BIN): the execsandbox runner
// serializes run.tier and wiring.injection_mode into the block's stdin JSON, so
// the fake block records exactly what the policy obligations set on the box
// request that box.Create issues.
func TestPolicyGateFakeBinaryE2E(t *testing.T) {
	binary := buildAgentBuilder(t)
	fakePolicy := buildFakePolicyEngine(t)

	// TC-072-05 (opt-in / zero-regression slice): with AGENT_BUILDER_POLICY_BIN
	// unset, the policy gate is skipped and the Phase-0 capstone is unchanged.
	// The authoritative TC-072-05 assertion lives in TestPhase0EndToEndAcceptance
	// (policy unset there); this case guards the gate stays opt-in.
	t.Run("TC-072-05_policy_unset_skips_gate", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		// fixture.env() does NOT set AGENT_BUILDER_POLICY_BIN.
		stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code != 0 {
			t.Fatalf("policy-unset run exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "run completed: task 001") {
			t.Fatalf("policy-unset stdout = %q, want unchanged completed run", stdout)
		}
	})

	t.Run("TC-072-02_deny_blocks_dispatch", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		argsLog := filepath.Join(t.TempDir(), "policy-args.log")
		socket := filepath.Join(t.TempDir(), "policy.sock")
		env := policyEnv(fixture, fakePolicy, socket, argsLog,
			`{"decision":"deny","context":{"reason":"test","obligations":[]}}`)

		stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("deny run exit code = %d, want 0 (deny is a terminal outcome, not an error); stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "run halted") || !strings.Contains(stdout, "denied") {
			t.Fatalf("deny stdout = %q, want a halt message containing 'denied'", stdout)
		}
		// Box never started -> executor never ran -> publisher never ran.
		assertNoPublishLog(t, fixture.publishLog)
		taskFile := readFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"))
		if !strings.Contains(taskFile, "**Status:** needs-human") {
			t.Fatalf("deny did not mark task needs-human:\n%s", taskFile)
		}
		assertPolicyArgsLogged(t, argsLog, socket)
	})

	t.Run("TC-072-06A_require_approval_routes_like_deny", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		argsLog := filepath.Join(t.TempDir(), "policy-args.log")
		socket := filepath.Join(t.TempDir(), "policy.sock")
		env := policyEnv(fixture, fakePolicy, socket, argsLog,
			`{"decision":"require_approval","context":{"reason":"needs ok","obligations":[]}}`)

		stdout, _, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("require_approval exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "run halted") || !strings.Contains(stdout, "approval") {
			t.Fatalf("require_approval stdout = %q, want halt mentioning approval (task 072 placeholder)", stdout)
		}
		assertNoPublishLog(t, fixture.publishLog)
		taskFile := readFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"))
		if !strings.Contains(taskFile, "**Status:** needs-human") {
			t.Fatalf("require_approval did not mark task needs-human:\n%s", taskFile)
		}
	})

	t.Run("TC-072-03_allow_tier_select_starts_box_with_tier", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		argsLog := filepath.Join(t.TempDir(), "policy-args.log")
		socket := filepath.Join(t.TempDir(), "policy.sock")
		boxLog := filepath.Join(t.TempDir(), "box.log")
		fakeBlock := buildFakeBlock(t, boxLog)

		env := policyEnv(fixture, fakePolicy, socket, argsLog,
			`{"decision":"allow","context":{"reason":"ok","obligations":[{"type":"tier_select","value":"gvisor"}]}}`)
		env[runtimewiring.EnvExecSandboxBin] = fakeBlock

		stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("allow+tier run exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "run completed: task 001") {
			t.Fatalf("allow+tier stdout = %q, want completed run (box started)", stdout)
		}
		recorded := readFile(t, boxLog)
		// The box.Create probe is the single block invocation; it must carry the
		// policy-selected tier.
		if got := strings.Count(recorded, "REQUEST"); got != 1 {
			t.Fatalf("block invoked %d times, want exactly 1 (the create probe); log=%q", got, recorded)
		}
		if !strings.Contains(recorded, `"tier":"gvisor"`) {
			t.Fatalf("block request = %q, want tier gvisor set by tier_select obligation", recorded)
		}
		assertPolicyArgsLogged(t, argsLog, socket)
	})

	t.Run("TC-072-06C_allow_vault_injection_floor_raised", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		argsLog := filepath.Join(t.TempDir(), "policy-args.log")
		socket := filepath.Join(t.TempDir(), "policy.sock")
		boxLog := filepath.Join(t.TempDir(), "box.log")
		fakeBlock := buildFakeBlock(t, boxLog)

		// No vault configured -> initial InjectionMode is "". The floor
		// obligation must raise it to proxy on the box request.
		env := policyEnv(fixture, fakePolicy, socket, argsLog,
			`{"decision":"allow","context":{"reason":"ok","obligations":[{"type":"vault_injection_floor","value":"proxy"}]}}`)
		env[runtimewiring.EnvExecSandboxBin] = fakeBlock

		stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("allow+floor run exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "run completed: task 001") {
			t.Fatalf("allow+floor stdout = %q, want completed run", stdout)
		}
		recorded := readFile(t, boxLog)
		if !strings.Contains(recorded, `"injection_mode":"proxy"`) {
			t.Fatalf("block request = %q, want injection_mode proxy raised by obligation (initial empty)", recorded)
		}
		assertPolicyArgsLogged(t, argsLog, socket)
	})
}

// TestPolicyGateRequireApprovalDistinctFromDeny (TC-073-01): require_approval
// produces a needs-human status with "approval" in the reason, observably
// different from the deny reason. Both block dispatch (box never starts).
func TestPolicyGateRequireApprovalDistinctFromDeny(t *testing.T) {
	binary := buildAgentBuilder(t)
	fakePolicy := buildFakePolicyEngine(t)

	var denyReason, approvalReason string

	t.Run("TC-073-01_deny_reason", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		argsLog := filepath.Join(t.TempDir(), "policy-args.log")
		socket := filepath.Join(t.TempDir(), "policy.sock")
		env := policyEnv(fixture, fakePolicy, socket, argsLog,
			`{"decision":"deny","context":{"reason":"forbidden","obligations":[]}}`)

		stdout, _, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("deny exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "run halted") {
			t.Fatalf("deny stdout = %q, want 'run halted'", stdout)
		}
		// Capture what the halt message says.
		denyReason = stdout
		// deny reason must NOT contain "approval".
		if strings.Contains(stdout, "approval") {
			t.Errorf("deny stdout = %q, must not contain 'approval'", stdout)
		}
		// Task marked needs-human.
		taskFile := readFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"))
		if !strings.Contains(taskFile, "**Status:** needs-human") {
			t.Fatalf("deny did not mark task needs-human:\n%s", taskFile)
		}
		// Box never started.
		assertNoPublishLog(t, fixture.publishLog)
	})

	t.Run("TC-073-01_require_approval_reason_contains_approval", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		argsLog := filepath.Join(t.TempDir(), "policy-args.log")
		socket := filepath.Join(t.TempDir(), "policy.sock")
		env := policyEnv(fixture, fakePolicy, socket, argsLog,
			`{"decision":"require_approval","context":{"reason":"high risk task","obligations":[]}}`)

		stdout, _, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("require_approval exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "run halted") {
			t.Fatalf("require_approval stdout = %q, want 'run halted'", stdout)
		}
		// Must contain "approval" to be observably distinct from deny.
		if !strings.Contains(stdout, "approval") {
			t.Fatalf("require_approval stdout = %q, want it to contain 'approval'", stdout)
		}
		approvalReason = stdout
		// Task marked needs-human.
		taskFile := readFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"))
		if !strings.Contains(taskFile, "**Status:** needs-human") {
			t.Fatalf("require_approval did not mark task needs-human:\n%s", taskFile)
		}
		// Box never started.
		assertNoPublishLog(t, fixture.publishLog)
	})

	// Final cross-sub-test assertion: the two reason strings are observably different.
	if denyReason != "" && approvalReason != "" && denyReason == approvalReason {
		t.Errorf("TC-073-01: deny and require_approval produced identical stdout %q — must differ", denyReason)
	}
}

// policyEnv extends the publication fixture env with the policy gate vars and a
// fake-policy-engine binary launched as AGENT_BUILDER_POLICY_BIN.
func policyEnv(f publicationFixture, fakePolicyBin, socket, argsLog, response string) map[string]string {
	env := f.env()
	env[runtimewiring.EnvPolicyBin] = fakePolicyBin
	env[runtimewiring.EnvPolicySocket] = socket
	env["FAKE_POLICY_RESPONSE"] = response
	env["FAKE_POLICY_ARGS_LOG"] = argsLog
	return env
}

func assertPolicyArgsLogged(t *testing.T, argsLog, socket string) {
	t.Helper()
	logged := readFile(t, argsLog)
	if !strings.Contains(logged, "--socket "+socket) {
		t.Fatalf("policy args log = %q, want it launched with --socket %s", logged, socket)
	}
	if !strings.Contains(logged, "--allow") {
		t.Fatalf("policy args log = %q, want it launched with --allow", logged)
	}
}

// goBuildFakeBinary compiles a standalone main-package Go source to bin.
func goBuildFakeBinary(t *testing.T, src, bin string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake binary %s: %v\n%s", bin, err, out)
	}
}

// buildFakePolicyEngine compiles a tiny binary that mimics the policy-engine
// `serve` subcommand: it parses --socket/--allow, binds the Unix socket, answers
// {"op":"ping"} with {"ok":true}, and answers {"op":"decide",...} with the
// response from FAKE_POLICY_RESPONSE. It logs its argv to FAKE_POLICY_ARGS_LOG.
func buildFakePolicyEngine(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	writeFile(t, src, fakePolicyEngineSource)
	bin := filepath.Join(dir, "policy-engine")
	goBuildFakeBinary(t, src, bin)
	return bin
}

// buildFakeBlock compiles a fake exec-sandbox block binary that reads the
// RunRequest JSON from stdin, appends it to boxLog (prefixed REQUEST), and
// returns a successful result so the box.Create probe passes.
func buildFakeBlock(t *testing.T, boxLog string) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	writeFile(t, src, strings.ReplaceAll(fakeBlockSource, "__BOX_LOG__", boxLog))
	bin := filepath.Join(dir, "exec-sandbox")
	goBuildFakeBinary(t, src, bin)
	return bin
}

const fakePolicyEngineSource = `package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"net"
	"os"
	"strings"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		os.Exit(2)
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	socket := fs.String("socket", "", "")
	allow := fs.String("allow", "", "")
	_ = allow
	_ = fs.Parse(os.Args[2:])

	if logPath := os.Getenv("FAKE_POLICY_ARGS_LOG"); logPath != "" {
		_ = os.WriteFile(logPath, []byte(strings.Join(os.Args[1:], " ")+"\n"), 0o644)
	}

	_ = os.Remove(*socket)
	ln, err := net.Listen("unix", *socket)
	if err != nil {
		os.Exit(1)
	}
	defer ln.Close()

	response := os.Getenv("FAKE_POLICY_RESPONSE")
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handle(conn, response)
	}
}

func handle(conn net.Conn, decideResp string) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	line, err := r.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req map[string]any
	_ = json.Unmarshal(line, &req)
	op, _ := req["op"].(string)
	switch op {
	case "ping":
		_, _ = conn.Write([]byte("{\"ok\":true}\n"))
	case "decide":
		_, _ = conn.Write([]byte(decideResp + "\n"))
	default:
		_, _ = conn.Write([]byte("{\"error\":{\"code\":\"bad_op\",\"message\":\"unknown\"}}\n"))
	}
}
`

const fakeBlockSource = `package main

import (
	"io"
	"os"
)

func main() {
	// Only the "run" subcommand is exercised by the create probe.
	data, _ := io.ReadAll(os.Stdin)
	f, err := os.OpenFile("__BOX_LOG__", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		_, _ = f.Write([]byte("REQUEST "))
		_, _ = f.Write(data)
		_, _ = f.Write([]byte("\n"))
		_ = f.Close()
	}
	// Return a successful block result so box.Create's probe (exit 0) passes.
	os.Stdout.Write([]byte("{\"exit_code\":0,\"stdout\":\"\",\"stderr\":\"\",\"sandbox_status\":{\"sandbox_id\":\"fake\",\"tier\":\"bubblewrap\",\"status\":\"clean\",\"duration_ms\":1}}"))
}
`
