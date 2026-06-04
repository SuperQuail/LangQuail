package coverage_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var goCacheDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "langquail-lqcover-test-cache")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	goCacheDir = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func TestLQCoverMergesDuplicateBlocksAndWritesProfile(t *testing.T) {
	root := repoRoot(t)
	outPath := filepath.Join(t.TempDir(), "coverage.out")
	output, err := runLQCover(t, root, "-profile", filepath.Join(root, "tests", "coverage", "testdata", "duplicate.cover"), "-out", outPath)
	if err != nil {
		t.Fatalf("lqcover failed: %v\n%s", err, output)
	}
	if !strings.Contains(output, "coverage: 70.0% of statements (7/10)") {
		t.Fatalf("unexpected output:\n%s", output)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	merged := string(data)
	if strings.Count(merged, "example.com/repo/foo/foo.go:1.1,2.1") != 1 {
		t.Fatalf("duplicate block was not merged:\n%s", merged)
	}
	if !strings.Contains(merged, "example.com/repo/foo/foo.go:1.1,2.1 2 1") {
		t.Fatalf("covered duplicate block did not keep covered count:\n%s", merged)
	}
	if !strings.Contains(merged, "example.com/repo/foo/foo.go:3.1,4.1 3 0") {
		t.Fatalf("uncovered-only block missing:\n%s", merged)
	}
}

func TestLQCoverThresholdPassesAtCoverage(t *testing.T) {
	root := repoRoot(t)
	outPath := filepath.Join(t.TempDir(), "coverage.out")
	output, err := runLQCover(t, root, "-profile", filepath.Join(root, "tests", "coverage", "testdata", "duplicate.cover"), "-out", outPath, "-threshold", "70")
	if err != nil {
		t.Fatalf("lqcover threshold should pass: %v\n%s", err, output)
	}
}

func TestLQCoverThresholdFailsBelowCoverage(t *testing.T) {
	root := repoRoot(t)
	outPath := filepath.Join(t.TempDir(), "coverage.out")
	output, err := runLQCover(t, root, "-profile", filepath.Join(root, "tests", "coverage", "testdata", "duplicate.cover"), "-out", outPath, "-threshold", "80")
	if err == nil {
		t.Fatalf("lqcover threshold should fail:\n%s", output)
	}
	if !strings.Contains(output, "coverage 70.0% is below threshold 80.0%") {
		t.Fatalf("unexpected threshold failure output:\n%s", output)
	}
}

func TestLQCoverListsSourcePackagesForCoverpkg(t *testing.T) {
	root := repoRoot(t)
	output, err := runLQCover(t, root, "-list-packages")
	if err != nil {
		t.Fatalf("lqcover package listing failed: %v\n%s", err, output)
	}
	if strings.Contains(output, "github.com/superquail/langquail/tests/") {
		t.Fatalf("source packages include tests package:\n%s", output)
	}
	if strings.Contains(output, "github.com/superquail/langquail/cmd/lqcover") {
		t.Fatalf("source packages include lqcover package:\n%s", output)
	}
	if !strings.Contains(output, "github.com/superquail/langquail/runtime") {
		t.Fatalf("source packages did not include runtime package:\n%s", output)
	}
}

func runLQCover(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmdArgs := append([]string{"run", "./cmd/lqcover"}, args...)
	cmd := exec.Command("go", cmdArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOCACHE="+goCacheDir)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not inspect caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
