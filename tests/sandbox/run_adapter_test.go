package sandbox_test

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestRunInterfaceReturnsResultAndExitCode(t *testing.T) {
	limits := sandbox.Limits{
		WallClockTimeout: 2 * time.Minute,
		MemoryBytes:      256 * 1024 * 1024,
		CPUCount:         2,
		EgressAllowlist:  []string{"github.com"},
	}
	req := sandbox.Request{
		Command:  []string{"go", "test", "./..."},
		Worktree: "/worktree",
		Limits:   limits,
	}
	want := sandbox.Result{
		Stdout:   "ok\n",
		Stderr:   "",
		Duration: 250 * time.Millisecond,
	}
	var runner sandbox.Runner = sandbox.NewFakeRunner(sandbox.FakeResponse{
		Result:   want,
		ExitCode: 0,
	})

	got, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("TC-001: Run() error = %v, want nil", err)
	}
	if exitCode != 0 {
		t.Fatalf("TC-001: exit code = %d, want 0", exitCode)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TC-001: result = %#v, want %#v", got, want)
	}
}

func TestRunInterfaceSurfacesNonZeroExitCode(t *testing.T) {
	var runner sandbox.Runner = sandbox.NewFakeRunner(sandbox.FakeResponse{
		Result:   sandbox.Result{Stderr: "test failed\n"},
		ExitCode: 2,
	})

	_, exitCode, err := runner.Run(sandbox.Request{
		Command:  []string{"go", "test"},
		Worktree: "/worktree",
	})
	if err != nil {
		t.Fatalf("TC-001A: Run() error = %v, want nil", err)
	}
	if exitCode != 2 {
		t.Fatalf("TC-001A: exit code = %d, want 2", exitCode)
	}
}

func TestRunInterfaceRejectsEmptyCommandBeforeRecording(t *testing.T) {
	fake := sandbox.NewFakeRunner(sandbox.FakeResponse{ExitCode: 0})

	result, exitCode, err := fake.Run(sandbox.Request{
		Command:  []string{" "},
		Worktree: "/worktree",
	})
	if !errors.Is(err, sandbox.ErrInvalidCommand) {
		t.Fatalf("TC-001B: Run() error = %v, want ErrInvalidCommand", err)
	}
	if exitCode != 0 {
		t.Fatalf("TC-001B: exit code = %d, want 0", exitCode)
	}
	if !reflect.DeepEqual(result, sandbox.Result{}) {
		t.Fatalf("TC-001B: result = %#v, want empty result", result)
	}
	if fake.CallCount() != 0 {
		t.Fatalf("TC-001B: fake call count = %d, want 0", fake.CallCount())
	}
}

func TestRunLimitsAreTyped(t *testing.T) {
	field, ok := reflect.TypeOf(sandbox.Request{}).FieldByName("Limits")
	if !ok {
		t.Fatal("TC-001C: Request has no Limits field")
	}
	if field.Type.Kind() == reflect.Map {
		t.Fatalf("TC-001C: Limits field type = %s, want typed struct", field.Type)
	}
	if field.Type != reflect.TypeOf(sandbox.Limits{}) {
		t.Fatalf("TC-001C: Limits field type = %s, want sandbox.Limits", field.Type)
	}

	limitType := reflect.TypeOf(sandbox.Limits{})
	for _, name := range []string{"WallClockTimeout", "MemoryBytes", "CPUCount", "EgressAllowlist"} {
		if _, ok := limitType.FieldByName(name); !ok {
			t.Fatalf("TC-001C: sandbox.Limits missing field %s", name)
		}
	}
}

func TestFakeBackendRecordsRequestsAndReturnsCannedResult(t *testing.T) {
	wantResult := sandbox.Result{Stdout: "done\n", Duration: time.Second}
	wantRequest := sandbox.Request{
		Command:  []string{"agent", "run"},
		Worktree: "/repo",
		Limits: sandbox.Limits{
			EgressAllowlist: []string{"api.anthropic.com"},
		},
	}
	var runner sandbox.Runner = sandbox.NewFakeRunner(sandbox.FakeResponse{
		Result:   wantResult,
		ExitCode: 0,
	})

	gotResult, exitCode, err := runner.Run(wantRequest)
	if err != nil {
		t.Fatalf("TC-002: Run() error = %v, want nil", err)
	}
	if exitCode != 0 {
		t.Fatalf("TC-002: exit code = %d, want 0", exitCode)
	}
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Fatalf("TC-002: result = %#v, want %#v", gotResult, wantResult)
	}

	fake := runner.(*sandbox.FakeRunner)
	requests := fake.Requests()
	if len(requests) != 1 {
		t.Fatalf("TC-002: recorded request count = %d, want 1", len(requests))
	}
	if !reflect.DeepEqual(requests[0], wantRequest) {
		t.Fatalf("TC-002: recorded request = %#v, want %#v", requests[0], wantRequest)
	}
}

func TestFakeBackendReturnsConfiguredErrors(t *testing.T) {
	wantErr := errors.New("backend unavailable")
	fake := sandbox.NewFakeRunner(sandbox.FakeResponse{Err: wantErr})

	_, _, err := fake.Run(sandbox.Request{
		Command:  []string{"agent"},
		Worktree: "/repo",
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("TC-002A: Run() error = %v, want %v", err, wantErr)
	}
}

func TestFakeBackendQueuesDeterministicResponses(t *testing.T) {
	fake := sandbox.NewFakeRunner(
		sandbox.FakeResponse{Result: sandbox.Result{Stdout: "first"}, ExitCode: 0},
		sandbox.FakeResponse{Result: sandbox.Result{Stdout: "second"}, ExitCode: 7},
	)

	first, firstExit, err := fake.Run(sandbox.Request{Command: []string{"first"}, Worktree: "/repo-a"})
	if err != nil {
		t.Fatalf("TC-002B: first Run() error = %v, want nil", err)
	}
	second, secondExit, err := fake.Run(sandbox.Request{Command: []string{"second"}, Worktree: "/repo-b"})
	if err != nil {
		t.Fatalf("TC-002B: second Run() error = %v, want nil", err)
	}

	if first.Stdout != "first" || firstExit != 0 {
		t.Fatalf("TC-002B: first response = (%#v, %d), want stdout first exit 0", first, firstExit)
	}
	if second.Stdout != "second" || secondExit != 7 {
		t.Fatalf("TC-002B: second response = (%#v, %d), want stdout second exit 7", second, secondExit)
	}
	requests := fake.Requests()
	if got := []string{requests[0].Command[0], requests[1].Command[0]}; !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("TC-002B: recorded request order = %v, want [first second]", got)
	}
}

func TestSupervisorAcceptsOnlySandboxInterface(t *testing.T) {
	var runner sandbox.Runner = sandbox.NewFakeRunner(sandbox.FakeResponse{ExitCode: 0})
	s := supervisor.New(supervisor.WithSandboxRunner(runner))
	if s == nil {
		t.Fatal("TC-003: New() returned nil supervisor")
	}
	if err := s.Run(); !errors.Is(err, supervisor.ErrNotImplemented) {
		t.Fatalf("TC-003: Run() error = %v, want ErrNotImplemented while dispatch is out of scope", err)
	}

	field, ok := reflect.TypeOf(supervisor.Supervisor{}).FieldByName("sandboxRunner")
	if !ok {
		t.Fatal("TC-003: Supervisor has no sandboxRunner field")
	}
	if field.Type != reflect.TypeOf((*sandbox.Runner)(nil)).Elem() {
		t.Fatalf("TC-003: sandboxRunner field type = %s, want sandbox.Runner interface", field.Type)
	}
}

func TestSupervisorImportsNoConcreteSandboxBackend(t *testing.T) {
	imports := supervisorImports(t)
	if !slices.Contains(imports, "github.com/tkdtaylor/agent-builder/internal/sandbox") {
		t.Fatalf("TC-004: supervisor imports = %v, want internal/sandbox seam import", imports)
	}

	for _, path := range imports {
		switch path {
		case "github.com/tkdtaylor/agent-builder/internal/sandbox/runtime",
			"github.com/tkdtaylor/agent-builder/internal/sandbox/fake",
			"github.com/tkdtaylor/agent-builder/internal/sandboxruntime":
			t.Fatalf("TC-004: supervisor imports concrete backend package %q", path)
		}
	}
}

func supervisorImports(t *testing.T) []string {
	t.Helper()

	root := repoRoot(t)
	pkgDir := filepath.Join(root, "internal", "supervisor")
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("read supervisor dir: %v", err)
	}

	var imports []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || stringsHasSuffix(entry.Name(), "_test.go") {
			continue
		}
		filePath := filepath.Join(pkgDir, entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), filePath, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", filePath, err)
		}
		for _, spec := range file.Imports {
			imports = append(imports, spec.Path.Value[1:len(spec.Path.Value)-1])
		}
	}
	return imports
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}

	dir := filepath.Dir(file)
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

func stringsHasSuffix(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
