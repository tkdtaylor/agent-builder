package containment_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionBoxEgressPlan_TC001_TC002(t *testing.T) {
	root := repoRoot(t)
	runPath := filepath.Join(root, "containment", "execution-box", "run.sh")

	valid := filepath.Join(t.TempDir(), "valid-egress.allowlist")
	if err := os.WriteFile(valid, []byte(`
# comment-only lines are ignored
API.GitHub.com:443 # TC-002 fixture with mixed case
api.github.com:443 # TC-002 duplicate is de-duplicated
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(runPath, "--egress-allowlist", valid, "--print-egress-plan")
	cmd.Dir = root
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("valid allowlist should parse: %v\n%s", err, outputBytes)
	}
	output := string(outputBytes)

	assertContains(t, output, "TC-001 PLAN: defaultAction=deny enforcement=dns-hosts+nftables")
	assertContains(t, output, "TC-001 PLAN: allow api.github.com:443")
	if strings.Count(output, "TC-001 PLAN: allow api.github.com:443") != 1 {
		t.Fatalf("expected duplicate allowlist entries to be de-duplicated, got:\n%s", output)
	}

	invalid := filepath.Join(t.TempDir(), "invalid-egress.allowlist")
	if err := os.WriteFile(invalid, []byte("https://api.github.com:443 # TC-002 scheme must fail\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd = exec.Command(runPath, "--egress-allowlist", invalid, "--print-egress-plan")
	cmd.Dir = root
	outputBytes, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("malformed allowlist should fail, got success:\n%s", outputBytes)
	}
	assertContains(t, string(outputBytes), "malformed egress allowlist entry")
	assertContains(t, string(outputBytes), "must be plain host:port")
}

// printEgressPlan writes allowlist content to a temp file and runs the launcher
// in --print-egress-plan mode, returning combined output and the run error.
func printEgressPlan(t *testing.T, root, runPath, content string) (string, error) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "egress.allowlist")
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(runPath, "--egress-allowlist", f, "--print-egress-plan")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TC-001 edge cases: a comment-and-blank-only allowlist resolves to total deny
// and emits no allow rules.
func TestExecutionBoxEgressPlan_TC001_EmptyAllowlist(t *testing.T) {
	root := repoRoot(t)
	runPath := filepath.Join(root, "containment", "execution-box", "run.sh")

	output, err := printEgressPlan(t, root, runPath, "\n# comment-only line, no hosts\n\n   \n")
	if err != nil {
		t.Fatalf("comment/blank-only allowlist should parse cleanly: %v\n%s", err, output)
	}
	assertContains(t, output, "TC-001 PLAN: defaultAction=deny enforcement=dns-hosts+nftables")
	assertContains(t, output, "TC-001 PLAN: empty allowlist; total egress deny")
	if strings.Contains(output, "TC-001 PLAN: allow ") {
		t.Fatalf("comments and blank lines must not produce allow rules, got:\n%s", output)
	}
}

// TC-002 edge cases: missing or non-numeric ports are rejected before Podman.
func TestExecutionBoxEgressPlan_TC002_PortRejection(t *testing.T) {
	root := repoRoot(t)
	runPath := filepath.Join(root, "containment", "execution-box", "run.sh")

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"missing port", "api.github.com # TC-002 no explicit port\n", "must include an explicit port"},
		{"non-numeric port", "api.github.com:https # TC-002 port is not numeric\n", "has a non-numeric port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := printEgressPlan(t, root, runPath, tc.content)
			if err == nil {
				t.Fatalf("%s should be rejected, got success:\n%s", tc.name, output)
			}
			assertContains(t, output, tc.want)
		})
	}
}

// TC-002 edge cases: wildcard, IP-literal, and CIDR entries are rejected.
func TestExecutionBoxEgressPlan_TC002_WildcardIPCIDRRejection(t *testing.T) {
	root := repoRoot(t)
	runPath := filepath.Join(root, "containment", "execution-box", "run.sh")

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"wildcard", "*.github.com:443 # TC-002 wildcards are not exact hosts\n", "must be plain host:port"},
		{"ip literal", "192.168.1.1:443 # TC-002 IP literals bypass DNS pinning\n", "does not accept IP literals"},
		{"cidr", "10.0.0.0/8:443 # TC-002 CIDR ranges are not single hosts\n", "must be plain host:port"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output, err := printEgressPlan(t, root, runPath, tc.content)
			if err == nil {
				t.Fatalf("%s should be rejected, got success:\n%s", tc.name, output)
			}
			assertContains(t, output, tc.want)
		})
	}
}

func TestExecutionBoxEgressLauncherContract_TC001_TC003_TC004(t *testing.T) {
	run := readFile(t, "containment", "execution-box", "run.sh")

	// TC-001: workload joins a pod whose egress sidecar installs the network filter first.
	assertContains(t, run, "podman pod create")
	assertContains(t, run, "agent-builder.egress=default-deny")
	assertContains(t, run, "/usr/local/bin/execution-box-egress-sidecar")
	assertContains(t, run, "egress sidecar did not become ready")
	assertContains(t, run, "--dns none")

	// TC-001 and TC-004: the sidecar, not the workload, owns network-admin privilege.
	assertContains(t, run, "--cap-add=NET_ADMIN")
	assertContains(t, run, "--cap-drop=all")
	assertContains(t, run, "--security-opt=no-new-privileges")
	assertContains(t, run, "--user \"$host_uid:$host_gid\"")
	assertContains(t, run, "--user 0:0")

	// TC-003 and TC-004: runtime probe quotes both allow and deny paths.
	assertContains(t, run, "--egress-probe")
	assertContains(t, run, "EXEC_BOX_EGRESS_PROBE_ALLOW_HOST")
	assertContains(t, run, "EXEC_BOX_EGRESS_PROBE_DENY_HOST")
	assertContains(t, run, "EXEC_BOX_EGRESS_PROBE_DENY_IP")
}

func TestExecutionBoxEgressSidecarAndProbe_TC001_TC003_TC004(t *testing.T) {
	sidecar := readFile(t, "containment", "execution-box", "egress-sidecar.sh")

	// TC-001: the nftables layer is default-deny and only allows resolved host:port pairs.
	assertContains(t, sidecar, "nft -f")
	assertContains(t, sidecar, "type ipv4_addr . inet_service")
	assertContains(t, sidecar, "type filter hook output priority 0; policy drop;")
	assertContains(t, sidecar, "ip daddr . tcp dport @allowed_tcp4 accept")
	assertContains(t, sidecar, "TC-001 PASS")

	probe := readFile(t, "containment", "execution-box", "egress-probe.sh")

	// TC-003 and TC-004: allowlisted host must connect; deny host and direct IP must fail.
	assertContains(t, probe, "TC-003")
	assertContains(t, probe, "allowlisted connect succeeded")
	assertContains(t, probe, "TC-004")
	assertContains(t, probe, "non-allowlisted connect blocked")
	assertContains(t, probe, "direct IP bypass blocked")
	assertContains(t, probe, "nc -vz -w 5")
}

func TestExecutionBoxEgressConfigAndDocs_TC002(t *testing.T) {
	allowlist := readFile(t, "containment", "execution-box", "egress.allowlist")
	config := readFile(t, "docs", "spec", "configuration.md")
	containerfile := readFile(t, "containment", "execution-box", "Containerfile")

	// TC-002: default allowlist is plain text with justified exact host:port entries.
	for _, line := range strings.Split(allowlist, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, " # ") {
			t.Fatalf("allowlist entry lacks inline justification comment: %q", line)
		}
		entry := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.Contains(entry, "://") || strings.Contains(entry, "/") || strings.Contains(entry, "*") {
			t.Fatalf("allowlist entry must be exact host:port, got %q", entry)
		}
		if strings.Count(entry, ":") != 1 {
			t.Fatalf("allowlist entry must contain exactly one port separator, got %q", entry)
		}
	}

	assertContains(t, config, "File: `containment/execution-box/egress.allowlist`")
	assertContains(t, config, "one exact hostname plus explicit TCP port")
	assertContains(t, config, "Empty allowlist")
	assertContains(t, config, "Malformed entries")

	assertContains(t, containerfile, "nftables")
	assertContains(t, containerfile, "busybox-extras")
	assertContains(t, containerfile, "execution-box-egress-sidecar")
	assertContains(t, containerfile, "execution-box-egress-probe")
}
