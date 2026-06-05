package gate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGoChecksCleanFixtureRepoPassesAllFourSteps(t *testing.T) {
	// TC-001
	g, err := New(GoBuildStep{}, GoVetStep{}, GoTestStep{}, GoFmtStep{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	verdict := g.Verify(testdataPath(t, "clean"))

	if !verdict.OK {
		t.Fatalf("Verify().OK = false, want true; results = %#v", verdict.Results)
	}

	wantNames := []string{
		goBuildStepName,
		goVetStepName,
		goTestStepName,
		goFmtStepName,
	}
	gotNames := make([]string, 0, len(verdict.Results))
	for _, result := range verdict.Results {
		gotNames = append(gotNames, result.Name)
		if !result.OK {
			t.Fatalf("%s OK = false, want true; output:\n%s", result.Name, result.Output)
		}
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("step names = %v, want %v", gotNames, wantNames)
	}
}

func TestGoChecksFailingTestFailsWithCapturedOutput(t *testing.T) {
	// TC-002
	result := GoTestStep{}.Run(testdataPath(t, "failing-test"))

	if result.OK {
		t.Fatal("GoTestStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "TestIntentionalFailure")
	assertOutputContains(t, result.Output, "intentional failure from fixture")
}

func TestGoChecksUnformattedFileFailsGoFmtStep(t *testing.T) {
	// TC-003
	result := GoFmtStep{}.Run(unformattedFixturePath(t))

	if result.OK {
		t.Fatal("GoFmtStep.Run().OK = true, want false")
	}
	assertOutputContains(t, filepath.ToSlash(result.Output), "unformatted.go")
}

func TestGoChecksMissingToolIsHardFailure(t *testing.T) {
	// TC-004
	emptyPATH := t.TempDir()
	t.Setenv("PATH", emptyPATH)

	result := GoBuildStep{}.Run(testdataPath(t, "clean"))

	if result.OK {
		t.Fatal("GoBuildStep.Run().OK = true, want false")
	}
	assertOutputContains(t, result.Output, "go")
	assertOutputContains(t, result.Output, "missing tool")
}

func testdataPath(t *testing.T, name string) string {
	t.Helper()

	path := filepath.Join("testdata", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("testdata fixture %q not available: %v", name, err)
	}

	return path
}

func unformattedFixturePath(t *testing.T) string {
	t.Helper()

	path := t.TempDir()
	writeFile(t, filepath.Join(path, "go.mod"), "module example.com/unformatted\n\ngo 1.26.3\n")
	writeFile(t, filepath.Join(path, "unformatted.go"), "package unformatted\n\nfunc Message() string {\nreturn \"unformatted\"\n}\n")

	return path
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertOutputContains(t *testing.T, output, want string) {
	t.Helper()

	if !strings.Contains(output, want) {
		t.Fatalf("output = %q, want substring %q", output, want)
	}
}
