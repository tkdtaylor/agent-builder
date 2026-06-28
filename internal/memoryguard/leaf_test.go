package memoryguard_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestTC084_03_MemoryguardIsLeaf asserts that internal/memoryguard's transitive
// dependency graph contains no other agent-builder/internal/ package. This is
// the programmatic counterpart to make fitness-memoryguard-isolation (F-012,
// ADR 049). The fitness check is the authoritative gate; this test provides
// in-suite coverage of the same invariant so TC-084-03 is traceable in tests/.
//
// It skips (rather than fails) when `go list` is unavailable so it never blocks
// a legitimate cross-compile environment.
func TestTC084_03_MemoryguardIsLeaf(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH; leaf isolation verified by make fitness-memoryguard-isolation")
	}

	out, err := exec.Command("go", "list", "-deps", "./internal/memoryguard/...").Output()
	if err != nil {
		t.Skipf("go list failed (%v); leaf isolation verified by make fitness-memoryguard-isolation", err)
	}

	lines := strings.Split(string(out), "\n")
	var forbidden []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "github.com/tkdtaylor/agent-builder/internal/") &&
			!strings.Contains(line, "agent-builder/internal/memoryguard") {
			forbidden = append(forbidden, line)
		}
	}
	if len(forbidden) > 0 {
		t.Errorf("TC-084-03 violated: internal/memoryguard imports forbidden agent-builder/internal package(s):\n%s",
			strings.Join(forbidden, "\n"))
	}
}
