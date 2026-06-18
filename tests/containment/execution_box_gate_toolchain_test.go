package containment_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutionBoxGateToolchainPlan_TC001_TC002(t *testing.T) {
	root := repoRoot(t)
	runPath := filepath.Join(root, "containment", "execution-box", "run.sh")
	toolDir := writeGateToolchainFixture(t, "golangci-lint", "dep-scan", "code-scanner")

	cmd := exec.Command(runPath, "--gate-tools", toolDir, "--print-toolchain-plan")
	cmd.Dir = root
	outputBytes, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("TC-001 toolchain plan should resolve: %v\n%s", err, outputBytes)
	}
	output := string(outputBytes)

	assertContains(t, output, "TC-001 PLAN: base-image go on PATH")
	assertContains(t, output, "TC-001 PLAN: base-image gofmt on PATH")
	for _, tool := range []string{"golangci-lint", "dep-scan", "code-scanner"} {
		assertContains(t, output, "TC-001 PLAN: mount "+tool+"="+filepath.Join(toolDir, tool))
		assertContains(t, output, "TC-002 PLAN: "+tool+" version="+tool+" fixture version")
	}
	assertContains(t, output, "TC-002 PLAN: mounted Gate tools are read-only")
	assertContains(t, output, "require no in-box network fetch")
}

func TestExecutionBoxGateToolchainMissingToolFails_TC003(t *testing.T) {
	root := repoRoot(t)
	runPath := filepath.Join(root, "containment", "execution-box", "run.sh")
	toolDir := writeGateToolchainFixture(t, "golangci-lint", "code-scanner")

	cmd := exec.Command(runPath, "--gate-tools", toolDir, "--print-toolchain-plan")
	cmd.Dir = root
	outputBytes, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("TC-003 missing dep-scan should fail, got success:\n%s", outputBytes)
	}
	output := string(outputBytes)

	assertContains(t, output, "missing Gate tool dep-scan")
	if strings.Contains(output, "TC-001 PLAN:") {
		t.Fatalf("TC-003 missing tool must fail before success plan output, got:\n%s", output)
	}
}

func TestExecutionBoxGateToolchainLauncherContract_TC001_TC003_TC005(t *testing.T) {
	run := readFile(t, "containment", "execution-box", "run.sh")
	probe := readFile(t, "containment", "execution-box", "probe.sh")

	// TC-001: mounted scanner/linter tools are placed on the in-box PATH.
	assertContains(t, run, "gate_tool_mount=\"/opt/agent-builder/gate-tools\"")
	assertContains(t, run, "--mount \"type=bind,source=$gate_tools,target=$gate_tool_mount,ro")
	assertContains(t, run, "--env \"PATH=$gate_tool_path\"")
	assertContains(t, probe, "for tool in go gofmt golangci-lint dep-scan code-scanner")
	assertContains(t, probe, "Gate tool $tool path=$tool_path version=$version")

	// TC-003: the launcher validates mounted tools before Podman starts.
	assertContains(t, run, "resolve_gate_tools")
	assertContains(t, run, "missing Gate tool $tool in $gate_tools")
	assertContains(t, run, "Gate toolchain directory does not exist")

	// TC-005: the runtime probe reports an explicit Gate-ready success line.
	assertContains(t, probe, "TC-005")
	assertContains(t, probe, "execution-box Gate toolchain available for in-box verification")
}

func TestExecutionBoxGateToolchainDocs_TC002_TC004(t *testing.T) {
	config := readFile(t, "docs", "spec", "configuration.md")
	interfaces := readFile(t, "docs", "spec", "interfaces.md")
	manifest := readFile(t, "containment", "execution-box", "gate-toolchain.manifest")
	makefile := readFile(t, "Makefile")

	// TC-002: configuration/spec docs identify the tool source and version contract.
	assertContains(t, config, "File: `containment/execution-box/gate-toolchain.manifest`")
	assertContains(t, config, "`EXEC_BOX_GATE_TOOLS`")
	assertContains(t, config, "golangci-lint`, `dep-scan`, and `code-scanner`")
	assertContains(t, config, "version-reported")
	assertContains(t, interfaces, "--gate-tools PATH")
	assertContains(t, interfaces, "--print-toolchain-plan")
	assertContains(t, manifest, "go=base-image")
	assertContains(t, manifest, "golangci-lint=mounted-version-reported")
	assertContains(t, manifest, "dep-scan=mounted-version-reported")
	assertContains(t, manifest, "code-scanner=mounted-version-reported")

	// TC-004: the product containment artifact remains the sole image/profile exception.
	assertContains(t, makefile, "-path './containment'")
	assertContains(t, makefile, "fitness-no-docker")
}

func writeGateToolchainFixture(t *testing.T, tools ...string) string {
	t.Helper()

	dir := t.TempDir()
	for _, tool := range tools {
		path := filepath.Join(dir, tool)
		script := "#!/bin/sh\nprintf '" + tool + " fixture version\\n'\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatalf("write fixture tool %s: %v", tool, err)
		}
	}
	return dir
}
