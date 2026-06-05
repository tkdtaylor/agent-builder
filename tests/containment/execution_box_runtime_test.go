package containment_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runRuntimePlan(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	runPath := filepath.Join(root, "containment", "execution-box", "run.sh")
	fullArgs := append(args, "--print-runtime-plan")
	cmd := exec.Command(runPath, fullArgs...)
	cmd.Dir = root
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestExecutionBoxRuntimePlan_TC001_TC003(t *testing.T) {
	output, err := runRuntimePlan(t, nil)
	if err != nil {
		t.Fatalf("default runtime plan should resolve: %v\n%s", err, output)
	}
	assertContains(t, output, "TC-016 PLAN: workload=agent runtime=runsc source=default")

	output, err = runRuntimePlan(t, nil, "--workload", "dev")
	if err != nil {
		t.Fatalf("dev runtime plan should resolve: %v\n%s", err, output)
	}
	assertContains(t, output, "TC-016 PLAN: workload=dev runtime=runc source=default")

	output, err = runRuntimePlan(t, nil, "--workload", "agent", "--runtime", "runc")
	if err != nil {
		t.Fatalf("explicit runtime override should resolve: %v\n%s", err, output)
	}
	assertContains(t, output, "TC-016 PLAN: workload=agent runtime=runc source=flag")

	output, err = runRuntimePlan(t, []string{"EXEC_BOX_WORKLOAD=dev", "EXEC_BOX_RUNTIME=runsc"})
	if err != nil {
		t.Fatalf("environment runtime override should resolve: %v\n%s", err, output)
	}
	assertContains(t, output, "TC-016 PLAN: workload=dev runtime=runsc source=env")
}

func TestExecutionBoxRuntimePlanRejectsUnknowns_TC001_TC003(t *testing.T) {
	output, err := runRuntimePlan(t, nil, "--runtime", "madeup")
	if err == nil {
		t.Fatalf("unknown runtime should fail, got success:\n%s", output)
	}
	assertContains(t, output, "unknown OCI runtime: madeup")

	output, err = runRuntimePlan(t, nil, "--workload", "batch")
	if err == nil {
		t.Fatalf("unknown workload should fail, got success:\n%s", output)
	}
	assertContains(t, output, "unknown workload tier: batch")
}

func TestExecutionBoxRuntimeLauncherContract_TC001_TC002_TC003_TC004(t *testing.T) {
	run := readFile(t, "containment", "execution-box", "run.sh")
	probe := readFile(t, "containment", "execution-box", "probe.sh")
	config := readFile(t, "docs", "spec", "configuration.md")
	interfaces := readFile(t, "docs", "spec", "interfaces.md")
	adr := readFile(t, "docs", "architecture", "decisions", "016-tiered-runtime-selection-seam.md")

	// TC-001: selected OCI runtime is passed to Podman and inspected for probes.
	assertContains(t, run, `--runtime "$runtime"`)
	assertContains(t, run, `--label "agent-builder.runtime=$runtime"`)
	assertContains(t, run, `runtime_inspect="$(podman inspect --format '{{.HostConfig.Runtime}}' "$cid")"`)
	assertContains(t, run, "TC-016 HOST: workload=%s runtime=%s")
	assertContains(t, run, "TC-016 FAIL: host inspect runtime=")

	// TC-001 edge: host runtime support is checked before the workload starts.
	assertContains(t, run, "validate_runtime_available")
	assertContains(t, run, "OCI runtime unavailable to Podman or PATH")

	// TC-002 and TC-004: runsc probes exercise and fail closed around a trivial Go build.
	assertContains(t, probe, "TC-016-RUNTIME")
	assertContains(t, probe, "TC-016-GO")
	assertContains(t, probe, `if [ "$runtime" = "runsc" ]; then`)
	assertContains(t, probe, "CGO_ENABLED=0 go build")
	assertContains(t, probe, "go build trivial module succeeded under runsc")
	assertContains(t, probe, "go build trivial module failed under runsc")

	// TC-003: docs expose workload defaults and override surfaces.
	for _, doc := range []string{config, interfaces, adr} {
		assertContains(t, doc, "agent")
		assertContains(t, doc, "runsc")
		assertContains(t, doc, "dev")
		assertContains(t, doc, "runc")
	}
	if strings.Contains(config, "localhost/agent-builder/execution-box:014") {
		t.Fatal("configuration spec still advertises the task 014 image tag")
	}
}
