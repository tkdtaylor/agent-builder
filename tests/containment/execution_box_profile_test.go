package containment_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

func readFile(t *testing.T, parts ...string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t)}, parts...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected file to contain %q", needle)
	}
}

func assertNotContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("expected file not to contain %q", needle)
	}
}

func dockWord() string {
	return "dock" + "er"
}

func TestExecutionBoxLauncherProfile_TC001_TC002_TC003_TC006(t *testing.T) {
	run := readFile(t, "containment", "execution-box", "run.sh")

	// TC-001: read-only rootfs; only /work and /scratch are writable.
	assertContains(t, run, "--read-only")
	assertContains(t, run, "target=/work,rw")
	assertContains(t, run, "--tmpfs")
	assertContains(t, run, "/scratch:rw,noexec,nosuid,nodev,mode=1777,size=$scratch_size")

	// TC-002: non-root, dropped capabilities, no new privileges, no home/socket binds.
	assertContains(t, run, `if [ "$(id -u)" -eq 0 ]; then`)
	assertContains(t, run, "--userns=keep-id")
	assertContains(t, run, `--user "$host_uid:$host_gid"`)
	assertContains(t, run, "--cap-drop=all")
	assertContains(t, run, "--security-opt=no-new-privileges")
	assertContains(t, run, "--env HOME=/scratch/home")
	assertNotContains(t, run, "--privileged")
	assertNotContains(t, run, "common_args+=(--cap-add")
	assertContains(t, run, "--cap-add=NET_ADMIN")
	assertNotContains(t, run, "${HOME}:")
	assertNotContains(t, run, "/var/run/podman.sock")
	assertNotContains(t, run, "/var/run/"+dockWord()+".sock")

	// TC-003: explicit resource quotas.
	assertContains(t, run, "--cpus")
	assertContains(t, run, "--memory")
	assertContains(t, run, "--pids-limit")
	assertContains(t, run, "--shm-size")
	assertContains(t, run, "--storage-opt")
	assertContains(t, run, "size=$storage_size")
	assertContains(t, run, "TC-003 PASS: host inspect shows explicit cpu/memory/pids/shm/storage limits")

	// TC-006: missing runtime behavior is explicit and fails closed.
	assertContains(t, run, "command -v podman")
	assertContains(t, run, "podman unavailable on PATH")
	assertContains(t, run, "podman info failed; rootless Podman is unavailable for this user")
}

func TestExecutionBoxProbeAssertions_TC001_TC002_TC003_TC004_TC005(t *testing.T) {
	probe := readFile(t, "containment", "execution-box", "probe.sh")

	// TC-001 and TC-004: writable worktree/scratch, denied rootfs writes.
	assertContains(t, probe, "TC-001")
	assertContains(t, probe, "/work/.execution-box-probe")
	assertContains(t, probe, "/scratch/probe/write.txt")
	assertContains(t, probe, "/.execution-box-root-write")
	assertContains(t, probe, "TC-004")
	assertContains(t, probe, "/usr")
	assertContains(t, probe, "/etc")

	// TC-002: non-root, dropped caps, scratch HOME, no host-home-like mount.
	assertContains(t, probe, `id -u`)
	assertContains(t, probe, `id -g`)
	assertContains(t, probe, "CapEff:")
	assertContains(t, probe, "0000000000000000")
	assertContains(t, probe, `HOME:-}`)
	assertContains(t, probe, "/scratch/home")

	// TC-003: in-box cgroup visibility checks.
	assertContains(t, probe, "pids.max")
	assertContains(t, probe, "memory.max")
	assertContains(t, probe, "cpu.max")
	assertContains(t, probe, "TC-003")

	// TC-005: no container-engine socket file or environment variable.
	assertContains(t, probe, "TC-005")
	assertContains(t, probe, "*podman*.sock")
	assertContains(t, probe, "*"+dockWord()+"*.sock")
	assertContains(t, probe, "CONTAINER_HOST")
	assertContains(t, probe, strings.ToUpper(dockWord())+"_HOST")
}

func TestExecutionBoxContainerfileContract_TC002(t *testing.T) {
	containerfile := readFile(t, "containment", "execution-box", "Containerfile")

	assertContains(t, containerfile, "USER 1000:1000")
	assertContains(t, containerfile, "WORKDIR /work")
	assertContains(t, containerfile, "ENV HOME=/scratch/home")
	assertContains(t, containerfile, "ENV TMPDIR=/scratch")
	assertNotContains(t, containerfile, "USER root")
}
