package e2e_test

// L5 end-to-end audit-chain harness for task 041. A real `agent-builder run`
// drives the default wiring with AGENT_BUILDER_AUDIT_RECORD set and a resolvable
// audit-trail binary; the produced chain is then verified with task 040's
// VerifyChain (the block's own `verify`) and its action sequence asserted to
// match the run.
//
// TC-041-04: BlockSink is wired behind AGENT_BUILDER_AUDIT_RECORD; blank disables it;
//            an unresolvable binary / unwritable path fails before dispatch.
// TC-041-05: the produced chain verifies (Valid==true) and the action sequence
//            matches the run (pick -> attempt -> verify -> publish -> finish).
//
// CI-without-binary: when no audit-trail binary resolves, the chain-producing
// assertions are skipped (the real-binary path is the L5 evidence); the
// recorded-exec argv contract is already covered by the runtime/supervisor unit
// tests (TC-041-01..03) and the BlockSink unit tests (task 039).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
)

// auditBinPath resolves the audit-trail binary for the L5 path from the
// AGENT_BUILDER_AUDIT_BIN environment variable, or "" if unset.
func auditBinPath() string {
	return os.Getenv("AGENT_BUILDER_AUDIT_BIN")
}

// chainActions parses the audit-trail chain NDJSON file and returns the ordered
// action field of each entry.
func chainActions(t *testing.T, path string) []string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit chain %s: %v", path, err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	actions := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("parse audit chain line %q: %v", line, err)
		}
		actions = append(actions, entry.Action)
	}
	return actions
}

func TestAuditChainEndToEnd(t *testing.T) {
	binPath := auditBinPath()
	if binPath == "" {
		t.Skip("TC-041-04/05: no audit-trail binary resolves (set AGENT_BUILDER_AUDIT_BIN); " +
			"the real-binary chain path is the L5 evidence — recorded-exec argv is covered by unit tests")
	}
	t.Logf("L5 real-binary path: using audit-trail at %s", binPath)

	binary := buildAgentBuilder(t)

	t.Run("TC-041-05_run_produces_a_chain_that_verifies", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		auditPath := filepath.Join(filepath.Dir(fixture.recordPath), "audit-chain.log")

		env := fixture.env()
		env[runtimewiring.EnvAuditRecord] = auditPath
		env[runtimewiring.EnvAuditBin] = binPath

		stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("TC-041-04 run exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "run completed: task 001") {
			t.Fatalf("TC-041-04 stdout = %q, want completed task summary", stdout)
		}

		// The chain file must exist and be non-empty.
		info, err := os.Stat(auditPath)
		if err != nil {
			t.Fatalf("TC-041-04 audit chain not produced at %s: %v", auditPath, err)
		}
		if info.Size() == 0 {
			t.Fatal("TC-041-04 audit chain is empty")
		}

		// TC-041-05: the block's own verify reports Valid==true over the produced chain.
		result, err := audit.VerifyChain(binPath, auditPath)
		if err != nil {
			t.Fatalf("TC-041-05 VerifyChain error = %v, want nil", err)
		}
		if !result.Valid {
			t.Fatalf("TC-041-05 VerifyChain Valid = false (tampered_at=%v, msg=%q); want true", result.TamperedAt, result.Message)
		}

		// TC-041-05 (edge): the action sequence matches the run.
		want := []string{"containment", "pick", "attempt", "verify", "publish", "finish"}
		got := chainActions(t, auditPath)
		if len(got) != len(want) {
			t.Fatalf("TC-041-05 chain action sequence = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("TC-041-05 chain action[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
			}
		}
		t.Logf("TC-041-05 L5 PASS: VerifyChain valid=true, action sequence=%v", got)
	})

	t.Run("TC-041-04_blank_audit_record_writes_no_chain", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		auditPath := filepath.Join(filepath.Dir(fixture.recordPath), "should-not-exist.log")

		env := fixture.env() // AGENT_BUILDER_AUDIT_RECORD absent
		stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
		if code != 0 {
			t.Fatalf("TC-041-04 blank: exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if _, err := os.Stat(auditPath); !os.IsNotExist(err) {
			t.Fatalf("TC-041-04 blank: chain stat err = %v, want not-exist (no audit configured)", err)
		}
	})

	t.Run("TC-041-04_unresolvable_binary_fails_before_dispatch", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		auditPath := filepath.Join(filepath.Dir(fixture.recordPath), "audit-chain.log")

		env := fixture.env()
		env[runtimewiring.EnvAuditRecord] = auditPath
		env[runtimewiring.EnvAuditBin] = "/nonexistent/audit-trail-does-not-exist"

		stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
		if code == 0 {
			t.Fatalf("TC-041-04 bad-bin: exit code = 0, want non-zero config error; stdout=%q stderr=%q", stdout, stderr)
		}
		// Auditing is never silently skipped: the chain must not have been produced,
		// and the error must name the misconfiguration before dispatch.
		if _, err := os.Stat(auditPath); !os.IsNotExist(err) {
			t.Fatalf("TC-041-04 bad-bin: chain produced despite unresolvable binary; stat err = %v", err)
		}
		if !strings.Contains(stderr, runtimewiring.EnvAuditBin) && !strings.Contains(stderr, "audit-trail") {
			t.Fatalf("TC-041-04 bad-bin: stderr = %q, want a config error naming the audit binary", stderr)
		}
	})

	t.Run("TC-041-04_unwritable_path_fails_before_dispatch", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		// A path under a non-existent directory is unwritable.
		auditPath := filepath.Join(filepath.Dir(fixture.recordPath), "no-such-dir", "audit-chain.log")

		env := fixture.env()
		env[runtimewiring.EnvAuditRecord] = auditPath
		env[runtimewiring.EnvAuditBin] = binPath

		stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
		if code == 0 {
			t.Fatalf("TC-041-04 unwritable: exit code = 0, want non-zero config error; stdout=%q stderr=%q", stdout, stderr)
		}
		if !strings.Contains(stderr, runtimewiring.EnvAuditRecord) {
			t.Fatalf("TC-041-04 unwritable: stderr = %q, want a config error naming %s", stderr, runtimewiring.EnvAuditRecord)
		}
	})
}
